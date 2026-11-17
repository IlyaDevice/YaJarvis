use super::*;
use tokio::io::{AsyncReadExt,AsyncWriteExt};
struct PeerHandler{gathered:Arc<tokio::sync::Notify>,channel:tokio::sync::mpsc::Sender<Arc<dyn DataChannel>>,audio:tokio::sync::mpsc::Sender<Vec<u8>>}
#[async_trait::async_trait]
impl PeerConnectionEventHandler for PeerHandler{
    async fn on_ice_gathering_state_change(&self,state:RTCIceGatheringState){if state==RTCIceGatheringState::Complete{self.gathered.notify_one();}}
    async fn on_data_channel(&self,channel:Arc<dyn DataChannel>){let _=self.channel.send(channel).await;}
    async fn on_track(&self,track:Arc<dyn TrackRemote>){let tx=self.audio.clone();tokio::spawn(async move{while let Some(event)=track.poll().await{if let TrackRemoteEvent::OnRtpPacket(packet)=event{if tx.send(packet.payload.to_vec()).await.is_err(){break;}}}});}
}
#[tokio::test]
async fn loopback_call_keeps_thread_and_carries_opus_and_delegations(){
    tokio::time::timeout(Duration::from_secs(25),async{
        let (events,event_rx)=tokio::sync::mpsc::channel(128);let mut event_rx=event_rx;
        let services:Callback=Arc::new(move|method,params|{let events=events.clone();Box::pin(async move{match method.as_str(){
            "network"=>Ok(json!({})),"auth.get"=>Ok(json!({"access":"test-access","account":"test-account","refresh":"test-refresh","expires":super::super::now()+3600})),
            "voice.event"=>{events.send(params).await.map_err(|e|e.to_string())?;Ok(json!(true))},"voice.brain"=>{assert_eq!(params["task"],"test task");Ok(json!("done"))},_=>Err("unexpected service".into()),
        }})});
        let mut provider=ChatGpt::new(services);let listener=tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();provider.call_url=format!("http://{}/call",listener.local_addr().unwrap());
        let (channels,mut channel_rx)=tokio::sync::mpsc::channel(1);let (audio,mut audio_rx)=tokio::sync::mpsc::channel(32);
        let gathered=Arc::new(tokio::sync::Notify::new());let handler=Arc::new(PeerHandler{gathered:gathered.clone(),channel:channels,audio});
        let mut media=MediaEngine::default();media.register_codec(RTCRtpCodecParameters{rtp_codec:RTCRtpCodec{mime_type:"audio/opus".into(),clock_rate:48000,channels:2,sdp_fmtp_line:"minptime=10;useinbandfec=1".into(),rtcp_feedback:vec![]},payload_type:111,..Default::default()},RtpCodecKind::Audio).unwrap();
        let remote:Arc<dyn PeerConnection>=Arc::new(PeerConnectionBuilder::new().with_media_engine(media).with_handler(handler).with_runtime(Arc::new(webrtc::runtime::TokioRuntime)).with_udp_addrs(vec!["0.0.0.0:0".into()]).with_tcp_addrs(Vec::<String>::new()).build().await.unwrap());
        let _guard=Guard(Some(remote.clone()));let pc=remote.clone();
        let server=tokio::spawn(async move{
            let (mut socket,_)=listener.accept().await.unwrap();let mut request=Vec::new();let mut chunk=[0;4096];
            let header_end=loop{let n=socket.read(&mut chunk).await.unwrap();assert!(n>0);request.extend(&chunk[..n]);if let Some(at)=request.windows(4).position(|v|v==b"\r\n\r\n"){break at+4;}assert!(request.len()<65536);};
            let headers=String::from_utf8_lossy(&request[..header_end]).to_lowercase();assert!(headers.contains("thread-id: thread-stable"));assert!(headers.contains("session-id: thread-stable"));
            let length:usize=headers.lines().find_map(|line|line.strip_prefix("content-length:").map(str::trim)).unwrap().parse().unwrap();while request.len()<header_end+length{let n=socket.read(&mut chunk).await.unwrap();assert!(n>0);request.extend(&chunk[..n]);}
            let body:Value=serde_json::from_slice(&request[header_end..header_end+length]).unwrap();assert_eq!(body["session"]["initial_items"][0]["role"],"developer");
            pc.set_remote_description(RTCSessionDescription::offer(body["sdp"].as_str().unwrap().to_string()).unwrap()).await.unwrap();let answer=pc.create_answer(None).await.unwrap();pc.set_local_description(answer).await.unwrap();let _=tokio::time::timeout(Duration::from_secs(3),gathered.notified()).await;let answer=pc.local_description().await.unwrap().sdp;
            socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",answer.len(),answer).as_bytes()).await.unwrap();
        });
        provider.request("voice.open",json!({"id":"local-call","profile":{"voice":"cove","thread":"thread-stable"},"instructions":"You are Jarvis"})).await.unwrap();server.await.unwrap();
        let channel=channel_rx.recv().await.unwrap();
        channel.send_text(&json!({"type":"session.input_transcript.added","item":{"text":"hello"}}).to_string()).await.unwrap();
        channel.send_text(&json!({"type":"delegation.created","item":{"id":"d1","content":[{"text":"test task"}]}}).to_string()).await.unwrap();
        loop{if let Some(DataChannelEvent::OnMessage(message))=channel.poll().await{let result:Value=serde_json::from_slice(&message.data).unwrap();if result["type"]=="delegation.context.append"{assert_eq!(result["content"][0]["text"],"done");break;}}}
        let mut heard=false;while let Some(event)=event_rx.recv().await{if event["kind"]=="user" && event["value"]=="hello"{heard=true;break;}}assert!(heard);
        provider.request("voice.feed",json!({"id":"local-call","pcm":B64.encode(vec![0;6400])})).await.unwrap();let packet=audio_rx.recv().await.unwrap();assert_eq!(opus::Decoder::new().unwrap().decode(Some(&packet),false,5760).unwrap().len(),960);
        provider.request("voice.close",json!({"id":"local-call"})).await.unwrap();assert!(provider.request("voice.feed",json!({"id":"local-call","pcm":""})).await.is_err());remote.close().await.unwrap();
    }).await.unwrap();
}
