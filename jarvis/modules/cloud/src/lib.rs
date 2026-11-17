//! Optional cloud providers. Each provider is independently selected at compile time.
use serde_json::{Value,json};
use std::{time::Duration,net::SocketAddr};
use base64::{Engine,engine::general_purpose::STANDARD};
pub type Socket=tokio_tungstenite::WebSocketStream<reqwest::Upgraded>;

pub struct Provider {id:&'static str,base:reqwest::Url}
impl Provider {
    pub fn new(id:&str)->Result<Self,String> {
        let (id,url):(&'static str,&str)=match id {
            #[cfg(feature="openai")] "openai"=>("openai","https://api.openai.com/"),
            #[cfg(feature="gemini")] "gemini"=>("gemini","https://generativelanguage.googleapis.com/"),
            #[cfg(feature="grok")] "grok"=>("grok","https://api.x.ai/"),
            #[cfg(feature="elevenlabs")] "elevenlabs"=>("elevenlabs","https://api.elevenlabs.io/"),
            _=>return Err("Провайдер не включён в сборку".into()),
        };
        Ok(Self{id,base:reqwest::Url::parse(url).map_err(|e|e.to_string())?})
    }
    pub fn id(&self)->&'static str{self.id}
    pub async fn socket(&self,key:&str,network:&Value)->Result<Socket,String>{
        use tokio_tungstenite::tungstenite::{handshake::{client::generate_key,derive_accept_key},protocol::{Role,WebSocketConfig}};
        if key.is_empty() || key.len()>4096 || key.bytes().any(|b|b<33 || b>126){return Err("Неверный API-ключ".into());}
        let path=match self.id{
            #[cfg(feature="openai")] "openai"=>"v1/live/sessions",
            #[cfg(feature="grok")] "grok"=>"v1/realtime",
            #[cfg(feature="gemini")] "gemini"=>"ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent",
            _=>return Err("Нет голосового WebSocket у этого провайдера".into()),
        };
        let mut url=self.base.join(path).map_err(|_|"Неверный адрес провайдера")?;
        if self.id=="gemini"{url.query_pairs_mut().append_pair("key",key);}
        let nonce=generate_key();let expected=derive_accept_key(nonce.as_bytes());
        let request=self.client(network)?.get(url).version(reqwest::Version::HTTP_11).header("Connection","Upgrade").header("Upgrade","websocket").header("Sec-WebSocket-Version","13").header("Sec-WebSocket-Key",nonce);
        let response=self.authenticate(request,key).send().await.map_err(|_|"Не подключиться к голосовому провайдеру")?;
        if response.status()!=101 || response.headers().get("Sec-WebSocket-Accept").and_then(|h|h.to_str().ok())!=Some(expected.as_str()) || response.headers().contains_key("Sec-WebSocket-Extensions") || response.headers().contains_key("Sec-WebSocket-Protocol"){
            return Err(format!("Голосовой провайдер не подтвердил WebSocket ({})",response.status().as_u16()));
        }
        let upgraded=response.upgrade().await.map_err(|_|"Обрыв голосового соединения")?;
        let config=WebSocketConfig::default().max_message_size(Some(4*1024*1024)).max_frame_size(Some(4*1024*1024)).write_buffer_size(16*1024).max_write_buffer_size(256*1024);
        Ok(tokio_tungstenite::WebSocketStream::from_raw_socket(upgraded,Role::Client,Some(config)).await)
    }
    fn client(&self,network:&Value)->Result<reqwest::Client,String> {
        if let Some(error)=network["error"].as_str(){return Err(error.into());}
        if !network.is_object(){return Err("Хост не предоставил сетевой маршрут".into());}
        let mut builder=reqwest::Client::builder().no_proxy().redirect(reqwest::redirect::Policy::none()).timeout(Duration::from_secs(60)).connect_timeout(Duration::from_secs(10));
        if let Some(socks)=network["socks"].as_str(){
            let addr:SocketAddr=socks.parse().map_err(|_|"Неверный SOCKS VPN")?;
            let user=network["user"].as_str().unwrap_or("");let pass=network["pass"].as_str().unwrap_or("");
            if !addr.ip().is_loopback() || addr.port()==0 || user.is_empty() || pass.is_empty(){return Err("Неверный SOCKS VPN".into());}
            builder=builder.proxy(reqwest::Proxy::all(format!("socks5h://{socks}")).map_err(|_|"Неверный SOCKS VPN")?.basic_auth(user,pass));
        }
        builder.build().map_err(|_|"Не создать HTTP-клиент".into())
    }
    fn authenticate(&self,request:reqwest::RequestBuilder,key:&str)->reqwest::RequestBuilder {
        match self.id {"gemini"=>request.header("x-goog-api-key",key),"elevenlabs"=>request.header("xi-api-key",key),_=>request.bearer_auth(key)}
    }
    pub async fn request(&self,params:Value,network:Value)->Result<Value,String> {
        let key=params["key"].as_str().unwrap_or("").trim();
        if key.is_empty() || key.len()>4096 || key.bytes().any(|b|b<33 || b>126){return Err("Неверный API-ключ".into());}
        let client=self.client(&network)?;
        let op=params["op"].as_str().unwrap_or("");
        if matches!(op,"text"|"tts") && !params["body"].is_object(){return Err("Нужен объект body".into());}
        let model=params["model"].as_str().unwrap_or("");
        let segment=|value:&str| !value.is_empty() && value.len()<=180 && value.bytes().all(|b|b.is_ascii_alphanumeric()||b"-_.".contains(&b)) && !matches!(value,"."|"..");
        let mut binary=false;
        let request=match (self.id,op) {
            (_,"settings.validate"|"models")=>{
                let path=match self.id {"gemini"=>"v1beta/models","elevenlabs"=>"v1/user/subscription",_=>"v1/models"};
                client.get(self.base.join(path).map_err(|e|e.to_string())?)
            },
            #[cfg(feature="openai")]
            ("openai","text")=>client.post(self.base.join("v1/responses").unwrap()).json(&params["body"]),
            #[cfg(feature="grok")]
            ("grok","text")=>client.post(self.base.join("v1/chat/completions").unwrap()).json(&params["body"]),
            #[cfg(feature="gemini")]
            ("gemini","text")=>{
                if !segment(model){return Err("Неверная модель Gemini".into());}
                client.post(self.base.join(&format!("v1beta/models/{model}:generateContent")).unwrap()).json(&params["body"])
            },
            #[cfg(feature="openai")]
            ("openai","tts")=>{
                binary=true;let mut body=params["body"].clone();body["response_format"]=json!("pcm");
                client.post(self.base.join("v1/audio/speech").unwrap()).json(&body)
            },
            #[cfg(feature="elevenlabs")]
            ("elevenlabs","tts")=>{
                let voice=params["voice"].as_str().unwrap_or("");if !segment(voice){return Err("Неверный ID голоса".into());}
                binary=true;client.post(self.base.join(&format!("v1/text-to-speech/{voice}")).unwrap()).query(&[("output_format","pcm_24000")]).json(&params["body"])
            },
            #[cfg(feature="elevenlabs")]
            ("elevenlabs","voices")=>client.get(self.base.join("v2/voices").unwrap()).query(&[("page_size","100")]),
            #[cfg(any(feature="openai",feature="elevenlabs"))]
            ("openai"|"elevenlabs","stt")=>{
                let data=STANDARD.decode(params["audio"].as_str().unwrap_or("")).map_err(|_|"Неверный звук STT")?;
                if data.len()>8*1024*1024 || data.is_empty(){return Err("Звук STT: от 1 байта до 8 МБ".into());}
                let path=if self.id=="openai" {"v1/audio/transcriptions"}else{"v1/speech-to-text"};
                let field=if self.id=="openai" {"model"}else{"model_id"};
                if !segment(model){return Err("Неверная модель STT".into());}
                let mut form=reqwest::multipart::Form::new().text(field,model.to_string()).part("file",reqwest::multipart::Part::bytes(data).file_name("speech.wav").mime_str("audio/wav").unwrap());
                if let Some(language)=params["language"].as_str().filter(|s|!s.is_empty()) {form=form.text(if self.id=="openai"{"language"}else{"language_code"},language.to_string());}
                client.post(self.base.join(path).unwrap()).multipart(form)
            },
            _=>return Err(format!("{} не поддерживает операцию {op}",self.id)),
        };
        let mut response=self.authenticate(request,key).send().await.map_err(|e|if e.is_timeout(){"Провайдер не ответил вовремя"}else{"Нет соединения с провайдером"}.to_string())?;
        let status=response.status();let mut bytes=Vec::new();
        let limit=if binary {16*1024*1024}else{4*1024*1024};
        while let Some(chunk)=response.chunk().await.map_err(|_|"Оборван ответ провайдера")? {if bytes.len()+chunk.len()>limit{return Err("Слишком большой ответ провайдера".into());}bytes.extend(chunk);}
        if !status.is_success(){
            let data:Value=serde_json::from_slice(&bytes).unwrap_or(Value::Null);
            let message=data.pointer("/error/message").or_else(||data.pointer("/detail/message")).and_then(Value::as_str).unwrap_or("");
            let message=message.replace(key,"***").chars().take(240).collect::<String>();
            return Err(format!("{} ответил {}: {message}",self.id,status.as_u16()));
        }
        if binary {if bytes.len()%2!=0{return Err("Неверный PCM от провайдера".into());}return Ok(json!({"audio":STANDARD.encode(bytes),"rate":24000}));}
        let value:Value=serde_json::from_slice(&bytes).map_err(|_|"Провайдер вернул неверный JSON")?;
        if op=="settings.validate" {
            let valid=match self.id {"gemini"=>value["models"].is_array(),"elevenlabs"=>value["character_limit"].is_number(),_=>value["data"].is_array()};
            if !valid{return Err("Провайдер не подтвердил API-ключ".into());}
        }
        Ok(value)
    }
}

#[cfg(all(test,feature="openai"))]
mod tests {
    use super::*;
    use tokio::io::{AsyncReadExt,AsyncWriteExt};
    #[tokio::test]
    async fn authenticates_and_preserves_response_and_never_bypasses_blocked_vpn(){
        let listener=tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();let addr=listener.local_addr().unwrap();
        let mut provider=Provider::new("openai").unwrap();provider.base=format!("http://{addr}/").parse().unwrap();
        assert!(provider.request(json!({"key":"test-key","op":"models"}),json!({"error":"VPN blocked"})).await.unwrap_err().contains("VPN blocked"));
        let server=tokio::spawn(async move {let (mut socket,_)=listener.accept().await.unwrap();let mut request=[0;8192];let n=socket.read(&mut request).await.unwrap();let text=String::from_utf8_lossy(&request[..n]).to_lowercase();assert!(text.contains("authorization: bearer test-key"));assert!(text.starts_with("get /v1/models "));socket.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\n{\"data\":[]}").await.unwrap();});
        assert_eq!(provider.request(json!({"key":"test-key","op":"settings.validate"}),json!({})).await.unwrap(),json!({"data":[]}));server.await.unwrap();
    }
    #[tokio::test]
    async fn websocket_upgrade_uses_provider_route_and_validates_handshake(){
        use futures_util::{SinkExt,StreamExt};
        use tokio_tungstenite::tungstenite::{Message,handshake::server::{Request,Response}};
        let listener=tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();let addr=listener.local_addr().unwrap();
        let mut provider=Provider::new("openai").unwrap();provider.base=format!("http://{addr}/").parse().unwrap();
        let server=tokio::spawn(async move {
            let (socket,_)=listener.accept().await.unwrap();
            let mut ws=tokio_tungstenite::accept_hdr_async(socket,|request:&Request,response:Response|{assert_eq!(request.uri().path(),"/v1/live/sessions");assert_eq!(request.headers()["authorization"],"Bearer test-key");Ok(response)}).await.unwrap();
            assert_eq!(ws.next().await.unwrap().unwrap().into_text().unwrap(),"hello");ws.send(Message::Text("ok".into())).await.unwrap();
        });
        let mut ws=provider.socket("test-key",&json!({})).await.unwrap();ws.send(Message::Text("hello".into())).await.unwrap();assert_eq!(ws.next().await.unwrap().unwrap().into_text().unwrap(),"ok");server.await.unwrap();
    }
}
