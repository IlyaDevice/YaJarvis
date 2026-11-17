//! Classic STT → tools/LLM → TTS, without a Go process.
use crate::{plugins::Host,settings::{Settings,Profile},tools::Tools,journal::Journal,session::{Session,Dialog}};
use std::{sync::{Arc,Mutex,atomic::{AtomicBool,AtomicU64,Ordering},mpsc::{Receiver,SyncSender,sync_channel}},time::Duration};
use tokio::sync::mpsc;
use serde_json::json;
use base64::{Engine as _,engine::general_purpose::STANDARD as B64};

pub struct Classic {
    dialog:Dialog,frames:mpsc::Sender<Vec<i16>>,say:mpsc::Sender<String>,
    out:Mutex<Option<SyncSender<Vec<i16>>>>,audio:Mutex<Option<Receiver<Vec<i16>>>>,
    closed:AtomicBool,generation:AtomicU64,interrupted:AtomicBool,stop:tokio::sync::Notify,
}
struct Context {host:Arc<Host>,tools:Arc<Tools>,settings:Settings,profile:Profile,instructions:String}
impl Classic {
    pub fn open(rt:&tokio::runtime::Handle,host:Arc<Host>,tools:Arc<Tools>,journal:Arc<Journal>,settings:Settings,profile:Profile,instructions:String)->Arc<Self>{
        let (out,audio)=sync_channel(64);let (frames,mut input)=mpsc::channel(64);let (say,mut messages)=mpsc::channel(8);
        let me=Arc::new(Self{dialog:Dialog::new(journal,"классический",&profile.llm_model),frames,say,out:Mutex::new(Some(out)),audio:Mutex::new(Some(audio)),closed:AtomicBool::new(false),generation:AtomicU64::new(0),interrupted:AtomicBool::new(false),stop:tokio::sync::Notify::new()});
        let actor=me.clone();let context=Arc::new(Context{host,tools,settings,profile,instructions});
        rt.spawn(async move {
            let mut vad=Capture::default();let mut tasks=tokio::task::JoinSet::new();
            loop {tokio::select! {
                biased;
                _=actor.stop.notified()=>break,
                Some(text)=messages.recv()=>{
                    tasks.abort_all();let generation=actor.generation.fetch_add(1,Ordering::SeqCst)+1;
                    let me=actor.clone();let context=context.clone();tasks.spawn(async move{me.speak(&context,&text,generation).await});
                },
                result=tasks.join_next(),if !tasks.is_empty()=>{if let Some(Ok(Err(error)))=result{actor.dialog.finish_turn(&error);}actor.dialog.working(-1);},
                Some(pcm)=input.recv()=>{
                    let starting=!vad.speaking && crate::engine::loud(&pcm);
                    if starting {tasks.abort_all();actor.generation.fetch_add(1,Ordering::SeqCst);actor.interrupted.store(true,Ordering::SeqCst);}
                    if let Some(pcm)=vad.feed(&pcm){
                        let me=actor.clone();let context=context.clone();let generation=actor.generation.load(Ordering::SeqCst);
                        actor.dialog.working(1);tasks.spawn(async move{me.turn(&context,pcm,generation).await});
                    }
                },
            }}
            tasks.shutdown().await;actor.out.lock().unwrap().take();
        });me
    }
    async fn turn(&self,context:&Context,pcm:Vec<i16>,generation:u64)->Result<(),String>{
        let stt=match context.profile.stt_engine.as_str(){"openai"=>"openai",_=>"elevenlabs"};
        let key=if stt=="openai"{&context.settings.openai_key}else{&context.settings.elevenlabs_key};
        let model=if context.profile.stt_model.is_empty(){if stt=="openai"{"gpt-4o-mini-transcribe"}else{"scribe_v2"}}else{&context.profile.stt_model};
        let response=context.host.provider(stt)?.request(stt,json!({"op":"stt","key":key,"model":model,"language":context.settings.language,"audio":B64.encode(wav(&pcm))}),Duration::from_secs(45)).await?;
        let text=response["text"].as_str().unwrap_or("").trim();if text.is_empty(){return Ok(());}
        self.dialog.on_user(text);
        if crate::fastcmd::is_stop(text){
            if crate::fastcmd::is_stop_all(text){if let Some(control)=&context.tools.music_ctl{control("стоп".into()).await;}}
            self.dialog.annotate("стоп","");self.dialog.finish_turn("");self.close();return Ok(());
        }
        if let Some(control)=&context.tools.music_ctl {
            let answer=control(text.to_string()).await;
            if !answer.is_empty(){self.dialog.on_bot(&answer);self.speak(context,&answer,generation).await?;self.dialog.finish_turn("");return Ok(());}
        }
        if let Some(command)=crate::fastcmd::alice_skill_for(text,context.settings.use_home("yandexmusic")) {
            tokio::task::spawn_blocking(move||crate::alice::text(&command,false)).await.map_err(|e|e.to_string())??;
            self.dialog.on_bot("Передал Алисе");self.dialog.finish_turn("");return Ok(());
        }
        let answer=if let Some(answer)=context.host.command(text).await{answer}else if let Some(answer)=context.tools.ha_vacuum(text).await{answer}else if let Some(answer)=context.tools.smartlife_fast(text).await{answer}else if let Some(answer)=context.tools.fast(text).await{answer}else{
            let (extra,defs)=context.tools.prepare().await;
            crate::llm::ask(&context.host,&context.settings,&context.profile,&format!("{}\n{}\n{extra}",context.instructions,crate::rules::BACKEND_RULES),text,&context.tools,defs).await?
        };
        if self.generation.load(Ordering::SeqCst)!=generation{return Ok(());}
        self.dialog.on_bot(&answer);self.speak(context,&answer,generation).await?;self.dialog.finish_turn("");Ok(())
    }
    async fn speak(&self,context:&Context,text:&str,generation:u64)->Result<(),String>{
        let provider=if context.profile.engine=="openai"{"openai"}else{"elevenlabs"};
        let key=if provider=="openai"{&context.settings.openai_key}else{&context.settings.elevenlabs_key};
        let voice=if context.profile.tts_voice.is_empty(){context.profile.voice.as_str()}else{context.profile.tts_voice.as_str()};
        let model=if context.profile.tts_model.is_empty(){if provider=="openai"{"gpt-4o-mini-tts"}else{crate::settings::ELEVEN_DEFAULT_MODEL}}else{&context.profile.tts_model};
        let body=if provider=="openai"{json!({"model":model,"voice":voice,"input":text})}else{json!({"text":text,"model_id":model})};
        let result=context.host.provider(provider)?.request(provider,json!({"op":"tts","key":key,"voice":voice,"body":body}),Duration::from_secs(60)).await?;
        let bytes=B64.decode(result["audio"].as_str().ok_or("Нет голоса TTS")?).map_err(|_|"Неверный голос TTS")?;
        if result["rate"]!=24000 || bytes.len()%2!=0{return Err("Неверный формат TTS".into());}
        for chunk in bytes.chunks(2400){
            let mut pcm=chunk.chunks_exact(2).map(|b|i16::from_le_bytes([b[0],b[1]])).collect();
            loop {
                if self.closed.load(Ordering::SeqCst) || self.generation.load(Ordering::SeqCst)!=generation{return Ok(());}
                let tx=self.out.lock().unwrap().clone().ok_or("Разговор закрыт")?;
                match tx.try_send(pcm){Ok(())=>break,Err(std::sync::mpsc::TrySendError::Full(data))=>pcm=data,Err(_)=>return Ok(())}
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        }
        Ok(())
    }
}
impl Session for Classic {
    fn handles_commands(&self)->bool{true}
    fn feed(&self,pcm:&[i16]){if !self.closed.load(Ordering::SeqCst){let _=self.frames.try_send(pcm.to_vec());}}
    fn take_audio(&self)->Option<Receiver<Vec<i16>>>{self.audio.lock().unwrap().take()}
    fn rate(&self)->usize{24000}
    fn ready(&self,_:Duration)->Result<(),String>{Ok(())}
    fn say(&self,text:&str)->Result<(),String>{self.say.try_send(text.to_string()).map_err(|e|e.to_string())}
    fn close(&self){self.closed.store(true,Ordering::SeqCst);self.stop.notify_one();}
    fn dialog(&self)->&Dialog{&self.dialog}
    fn interrupted(&self)->bool{self.interrupted.swap(false,Ordering::SeqCst)}
}
#[derive(Default)]struct Capture{preroll:Vec<i16>,speech:Vec<i16>,silence:usize,speaking:bool}
impl Capture {
    fn feed(&mut self,pcm:&[i16])->Option<Vec<i16>>{
        let voiced=crate::engine::loud(pcm);
        if !self.speaking && !voiced {self.preroll.extend(pcm);if self.preroll.len()>4800{self.preroll.drain(..self.preroll.len()-4800);}return None;}
        if !self.speaking {self.speaking=true;self.speech=std::mem::take(&mut self.preroll);}
        self.speech.extend(pcm);if voiced{self.silence=0;}else{self.silence+=pcm.len();}
        if self.silence>=11200 || self.speech.len()>=16000*45 {self.speaking=false;self.silence=0;let result=std::mem::take(&mut self.speech);if result.len()>=3200{return Some(result);}}
        None
    }
}
fn wav(pcm:&[i16])->Vec<u8>{
    let size=(pcm.len()*2) as u32;let mut out=Vec::with_capacity(size as usize+44);out.extend(b"RIFF");out.extend((size+36).to_le_bytes());out.extend(b"WAVEfmt ");out.extend(16u32.to_le_bytes());out.extend(1u16.to_le_bytes());out.extend(1u16.to_le_bytes());out.extend(16000u32.to_le_bytes());out.extend(32000u32.to_le_bytes());out.extend(2u16.to_le_bytes());out.extend(16u16.to_le_bytes());out.extend(b"data");out.extend(size.to_le_bytes());out.extend(pcm.iter().flat_map(|p|p.to_le_bytes()));out
}
#[cfg(test)]mod tests{use super::*;#[test]fn speech_capture_is_bounded_and_wav_has_real_lengths(){let mut capture=Capture::default();for _ in 0..30{assert!(capture.feed(&[0;1600]).is_none());}assert_eq!(capture.preroll.len(),4800);assert!(capture.feed(&[2000;3200]).is_none());let data=capture.feed(&[0;11200]).unwrap();assert!(!capture.speaking);let encoded=wav(&data);assert_eq!(&encoded[..4],b"RIFF");assert_eq!(encoded.len(),44+data.len()*2);}}
