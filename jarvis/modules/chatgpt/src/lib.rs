//! Subscription credentials and Codex requests. Credentials live in the host's atomic store.
use serde_json::{Value,json};
use std::{future::Future,pin::Pin,sync::{Arc,Mutex},time::{Duration,SystemTime,UNIX_EPOCH},net::SocketAddr};
use base64::{Engine,engine::general_purpose::URL_SAFE_NO_PAD};
pub type Callback=Arc<dyn Fn(String,Value)->Pin<Box<dyn Future<Output=Result<Value,String>>+Send>>+Send+Sync>;
const ISSUER:&str="https://auth.openai.com";
const CLIENT:&str="app_EMoamEEZ73f0CkXaXp7hrann";
mod opus;
mod rtc_runtime;
mod rtc_voice;
fn now()->i64{SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_secs() as i64}
pub struct ChatGpt {
    services:Callback,refresh:tokio::sync::Mutex<()>,login:Arc<Mutex<Value>>,
    generation:Arc<std::sync::atomic::AtomicU64>,poller:Mutex<Option<tokio::task::JoinHandle<()>>>,
    issuer:String,codex:String,call_url:String,
    voices:Mutex<std::collections::BTreeMap<String,Arc<rtc_voice::RtcVoice>>>,voice_lock:tokio::sync::Mutex<()>,
}
impl ChatGpt {
    pub fn new(services:Callback)->Self{Self{services,refresh:tokio::sync::Mutex::new(()),login:Arc::new(Mutex::new(Value::Null)),generation:Arc::new(std::sync::atomic::AtomicU64::new(0)),poller:Mutex::new(None),issuer:ISSUER.into(),codex:"https://chatgpt.com/backend-api/codex/responses".into(),call_url:"https://chatgpt.com/backend-api/codex/realtime/calls?intent=quicksilver&architecture=avas".into(),voices:Mutex::default(),voice_lock:tokio::sync::Mutex::new(())}}
    async fn client(&self)->Result<reqwest::Client,String>{client(&(self.services)("network".into(),json!({})).await?)}
    pub fn login_state(&self)->Value{self.login.lock().unwrap().clone()}
    pub fn stop(&self){self.generation.fetch_add(1,std::sync::atomic::Ordering::SeqCst);if let Some(task)=self.poller.lock().unwrap().take(){task.abort();}*self.login.lock().unwrap()=Value::Null;self.voices.lock().unwrap().clear();}
    pub async fn shutdown(&self){let voices=std::mem::take(&mut*self.voices.lock().unwrap());self.stop();for (_,voice) in voices{voice.close().await;}}
    pub async fn request(&self,method:&str,params:Value)->Result<Value,String>{
        match method{
            "auth.token"=>self.token().await,
            "login.status"=>Ok(self.login_state()),
            "login.start"=>self.start().await,
            "auth.logout"=>{self.stop();let before=(self.services)("auth.get".into(),Value::Null).await?;(self.services)("auth.set".into(),json!({"before":before,"after":{}})).await?;Ok(json!({}))},
            "codex.request"=>self.codex(params).await,
            "voice.open"=>{
                let _lock=self.voice_lock.lock().await;let id=params["id"].as_str().ok_or("Нужен ID звонка")?;
                if self.voices.lock().unwrap().contains_key(id){return Ok(json!({"rate":48000}));}
                if self.voices.lock().unwrap().len()>=2{return Err("Лимит: два звонка ChatGPT".into());}
                let generation=self.generation.load(std::sync::atomic::Ordering::SeqCst);let voice=rtc_voice::RtcVoice::open(self,&params).await?;
                if generation!=self.generation.load(std::sync::atomic::Ordering::SeqCst){voice.close().await;return Err("Звонок отменён".into());}
                self.voices.lock().unwrap().insert(voice.id.clone(),voice);Ok(json!({"rate":48000}))
            },
            "voice.feed"=>{let voice=self.voice(&params)?;voice.feed(params["pcm"].as_str().ok_or("Нет PCM")?)?;Ok(Value::Null)},
            "voice.say"=>{self.voice(&params)?.say(params["text"].as_str().unwrap_or("")).await?;Ok(Value::Null)},
            "voice.close"=>{let voice=self.voices.lock().unwrap().remove(params["id"].as_str().unwrap_or(""));if let Some(voice)=voice{voice.close().await;}Ok(Value::Null)},
            _=>Err(format!("ChatGPT: метод {method} не поддерживается")),
        }
    }
    fn voice(&self,params:&Value)->Result<Arc<rtc_voice::RtcVoice>,String>{self.voices.lock().unwrap().get(params["id"].as_str().unwrap_or("")).cloned().ok_or("Звонок ChatGPT закрыт".into())}
    async fn token(&self)->Result<Value,String>{
        let _lock=self.refresh.lock().await;let before=(self.services)("auth.get".into(),Value::Null).await?;
        let get=|k:&str|before[k].as_str().unwrap_or("");
        if get("refresh").is_empty() || get("account").is_empty(){return Err("Не выполнен вход в ChatGPT".into());}
        if !get("access").is_empty() && before["expires"].as_i64().unwrap_or(0)>now()+300{return Ok(before);}
        let client=self.client().await?;
        let response=client.post(format!("{}/oauth/token",self.issuer)).form(&[("grant_type","refresh_token"),("refresh_token",get("refresh")),("client_id",CLIENT),("scope","openid profile email")]).send().await.map_err(|_|"Не продлить вход в ChatGPT")?;
        let tokens=json_response(response).await?;let after=tokens_to_auth(&tokens,&before)?;
        (self.services)("auth.set".into(),json!({"before":before,"after":after})).await?;Ok(after)
    }
    async fn start(&self)->Result<Value,String>{
        self.stop();let http=self.client().await?;
        let response=http.post(format!("{}/api/accounts/deviceauth/usercode",self.issuer)).json(&json!({"client_id":CLIENT})).send().await.map_err(|_|"Не начать вход в ChatGPT")?;
        let device=json_response(response).await?;let id=device["device_auth_id"].as_str().ok_or("OpenAI не выдал device ID")?.to_string();
        let code=device["user_code"].as_str().or_else(||device["usercode"].as_str()).ok_or("OpenAI не выдал код")?.to_string();
        let interval=device["interval"].as_u64().or_else(||device["interval"].as_str().and_then(|v|v.parse().ok())).unwrap_or(5).clamp(1,30);
        let state=json!({"code":code,"url":format!("{}/codex/device",self.issuer),"until":now()+900});*self.login.lock().unwrap()=state.clone();
        let before=(self.services)("auth.get".into(),Value::Null).await?;
        let generation=self.generation.load(std::sync::atomic::Ordering::SeqCst);let serial=self.generation.clone();let services=self.services.clone();let login=self.login.clone();let issuer=self.issuer.clone();
        let task=tokio::spawn(async move{
            let result=async{
                let until=tokio::time::Instant::now()+Duration::from_secs(900);
                while tokio::time::Instant::now()<until{
                    tokio::time::sleep(Duration::from_secs(interval)).await;
                    if serial.load(std::sync::atomic::Ordering::SeqCst)!=generation{return Ok(());}
                    let http=client(&(services)("network".into(),json!({})).await?)?;
                    let response=match http.post(format!("{issuer}/api/accounts/deviceauth/token")).json(&json!({"device_auth_id":id,"user_code":code})).send().await{Ok(r)=>r,Err(_)=>continue};
                    if [403,404,429,500,502,503,504].contains(&response.status().as_u16()){continue;}
                    let grant=json_response(response).await?;let Some(code)=grant["authorization_code"].as_str().filter(|s|!s.is_empty())else{continue;};
                    let verifier=grant["code_verifier"].as_str().ok_or("OpenAI не выдал verifier")?;
                    let response=http.post(format!("{issuer}/oauth/token")).form(&[("grant_type","authorization_code"),("code",code),("client_id",CLIENT),("redirect_uri",&format!("{issuer}/deviceauth/callback")),("code_verifier",verifier)]).send().await.map_err(|_|"Не обменять код ChatGPT")?;
                    let after=tokens_to_auth(&json_response(response).await?,&json!({}))?;
                    if serial.load(std::sync::atomic::Ordering::SeqCst)!=generation{return Ok(());}
                    (services)("auth.set".into(),json!({"before":before,"after":after})).await?;*login.lock().unwrap()=Value::Null;return Ok(());
                }
                Err::<(),String>("Код ChatGPT устарел; начните вход заново".into())
            }.await;
            if serial.load(std::sync::atomic::Ordering::SeqCst)==generation{if let Err(error)=result{*login.lock().unwrap()=json!({"error":error});}}
        });*self.poller.lock().unwrap()=Some(task);Ok(state)
    }
    async fn codex(&self,params:Value)->Result<Value,String>{
        let auth=self.token().await?;let access=auth["access"].as_str().ok_or("Нет токена ChatGPT")?;let account=auth["account"].as_str().ok_or("Нет аккаунта ChatGPT")?;
        let session=params["session"].as_str().unwrap_or("");if session.len()>128{return Err("Неверный ID сессии".into());}
        let client=self.client().await?;let response=client.post(&self.codex).bearer_auth(access).header("chatgpt-account-id",account).header("Accept","text/event-stream").header("OpenAI-Beta","responses=experimental").header("originator","codex_cli_rs").header("session_id",session).json(&params["body"]).send().await.map_err(|_|"Нет соединения с ChatGPT")?;
        if !response.status().is_success(){return Err(format!("ChatGPT ответил {}",response.status().as_u16()));}
        sse(response).await
    }
}
impl Drop for ChatGpt{fn drop(&mut self){self.stop();}}
fn client(network:&Value)->Result<reqwest::Client,String>{
    if let Some(error)=network["error"].as_str(){return Err(error.into());}if !network.is_object(){return Err("Нет маршрута ChatGPT".into());}
    let mut builder=reqwest::Client::builder().no_proxy().redirect(reqwest::redirect::Policy::none()).connect_timeout(Duration::from_secs(10)).timeout(Duration::from_secs(60));
    if let Some(socks)=network["socks"].as_str(){let addr:SocketAddr=socks.parse().map_err(|_|"Неверный SOCKS VPN")?;let user=network["user"].as_str().unwrap_or("");let pass=network["pass"].as_str().unwrap_or("");if !addr.ip().is_loopback()||addr.port()==0||user.is_empty()||pass.is_empty(){return Err("Неверный SOCKS VPN".into());}builder=builder.proxy(reqwest::Proxy::all(format!("socks5h://{socks}")).map_err(|_|"Неверный SOCKS VPN")?.basic_auth(user,pass));}
    builder.build().map_err(|_|"Не создать клиент ChatGPT".into())
}
async fn json_response(mut response:reqwest::Response)->Result<Value,String>{
    let status=response.status();let mut bytes=Vec::new();while let Some(chunk)=response.chunk().await.map_err(|_|"Обрыв ответа OpenAI")?{if bytes.len()+chunk.len()>1024*1024{return Err("Слишком большой ответ OpenAI".into());}bytes.extend(chunk);}
    if !status.is_success(){return Err(format!("OpenAI ответил {}",status.as_u16()));}serde_json::from_slice(&bytes).map_err(|_|"Неверный JSON OpenAI".into())
}
fn claims(token:&str)->Value{token.split('.').nth(1).and_then(|v|URL_SAFE_NO_PAD.decode(v.trim_end_matches('=')).ok()).and_then(|b|serde_json::from_slice(&b).ok()).unwrap_or(Value::Null)}
fn tokens_to_auth(tokens:&Value,before:&Value)->Result<Value,String>{
    let access=tokens["access_token"].as_str().filter(|s|!s.is_empty()).ok_or("OpenAI не выдал access token")?;let mut after=if before.is_object(){before.clone()}else{json!({})};after["access"]=access.into();
    if let Some(refresh)=tokens["refresh_token"].as_str().filter(|s|!s.is_empty()){after["refresh"]=refresh.into();}
    for token in [tokens["id_token"].as_str().unwrap_or(""),access]{let claim=claims(token);let data=&claim["https://api.openai.com/auth"];for (to,from) in [("account","chatgpt_account_id"),("plan","chatgpt_plan_type")]{if let Some(value)=data[from].as_str().filter(|s|!s.is_empty()){after[to]=value.into();}}if let Some(email)=claim["email"].as_str(){after["email"]=email.into();}}
    after["expires"]=claims(access)["exp"].as_i64().unwrap_or(now()+tokens["expires_in"].as_i64().unwrap_or(3600)).into();
    if after["account"].as_str().unwrap_or("").is_empty() || after["refresh"].as_str().unwrap_or("").is_empty(){return Err("OpenAI не выдал аккаунт или refresh token".into());}Ok(after)
}
async fn sse(mut response:reqwest::Response)->Result<Value,String>{
    let mut pending=Vec::new();let mut output=Vec::new();let mut complete=false;let mut total=0;
    'stream: while let Some(chunk)=response.chunk().await.map_err(|_|"Обрыв ответа ChatGPT")?{
        total+=chunk.len();if total>16*1024*1024{return Err("Слишком большой ответ ChatGPT".into());}pending.extend(chunk);
        while let Some(end)=pending.iter().position(|b|*b==b'\n'){
            let line:Vec<_>=pending.drain(..=end).collect();let text=std::str::from_utf8(&line).map_err(|_|"Неверная кодировка ChatGPT")?.trim_end();
            let Some(data)=text.strip_prefix("data:").map(str::trim_start)else{continue;};if data=="[DONE]"{continue;}
            let event:Value=serde_json::from_str(data).map_err(|_|"Неверное SSE-событие ChatGPT")?;
            match event["type"].as_str(){Some("response.output_item.done")=>output.push(event["item"].clone()),Some("response.completed")=>{complete=true;if let Some(items)=event["response"]["output"].as_array(){output=items.clone();}},Some("response.failed"|"response.incomplete"|"error")=>return Err("ChatGPT не завершил запрос".into()),_=>{}}
            if output.len()>512{return Err("Слишком много элементов ответа ChatGPT".into());}if complete{break 'stream;}
        }
    }
    if !complete{return Err("Ответ ChatGPT оборван до завершения".into());}
    let mut answer=String::new();let mut calls=vec![];
    for item in &output{if item["type"]=="function_call"{calls.push(item.clone());}if item["type"]=="message"{for part in item["content"].as_array().into_iter().flatten(){if part["type"]=="output_text"{answer.push_str(part["text"].as_str().unwrap_or(""));}}}}
    Ok(json!({"answer":answer,"calls":calls,"output":output}))
}

#[cfg(test)]mod tests{
    use super::*;use tokio::io::{AsyncReadExt,AsyncWriteExt};
    fn jwt(claim:Value)->String{format!("e30.{}.signature",URL_SAFE_NO_PAD.encode(serde_json::to_vec(&claim).unwrap()))}
    fn services(auth:Arc<Mutex<Value>>)->Callback{Arc::new(move|method,params|{let auth=auth.clone();Box::pin(async move{match method.as_str(){"network"=>Ok(json!({})),"auth.get"=>Ok(auth.lock().unwrap().clone()),"auth.set"=>{let mut current=auth.lock().unwrap();if *current!=params["before"]{return Err("account changed".into());}*current=params["after"].clone();Ok(json!(true))},_=>Err("unknown service".into())}})})}
    #[tokio::test]
    async fn refresh_serializes_and_commits_rotated_tokens_atomically(){
        let auth=Arc::new(Mutex::new(json!({"refresh":"old-refresh","account":"account","access":"expired","expires":0})));
        let mut provider=ChatGpt::new(services(auth.clone()));let listener=tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();provider.issuer=format!("http://{}",listener.local_addr().unwrap());
        let access=jwt(json!({"exp":now()+3600,"email":"test@example.org","https://api.openai.com/auth":{"chatgpt_account_id":"account","chatgpt_plan_type":"plus"}}));let expected=access.clone();
        let server=tokio::spawn(async move{let (mut socket,_)=listener.accept().await.unwrap();let mut request=[0;8192];let n=socket.read(&mut request).await.unwrap();let request=String::from_utf8_lossy(&request[..n]);assert!(request.starts_with("POST /oauth/token "));let data=json!({"access_token":access,"refresh_token":"rotated-refresh"}).to_string();socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",data.len(),data).as_bytes()).await.unwrap();});
        let (first,second)=tokio::join!(provider.token(),provider.token());assert_eq!(first.unwrap()["access"],expected);assert_eq!(second.unwrap()["refresh"],"rotated-refresh");assert_eq!(auth.lock().unwrap()["plan"],"plus");server.await.unwrap();
    }
    #[tokio::test]
    async fn sse_preserves_tool_calls_and_rejects_an_incomplete_response(){
        for complete in [true,false]{
            let listener=tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();let url=format!("http://{}/",listener.local_addr().unwrap());
            let mut events=String::from("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Привет\"}]}}\n\n");
            if complete{events.push_str("data: {\"type\":\"response.completed\"}\n\n");}
            let server=tokio::spawn(async move{let (mut socket,_)=listener.accept().await.unwrap();let mut request=[0;1024];socket.read(&mut request).await.unwrap();socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",events.len()).as_bytes()).await.unwrap();for part in events.as_bytes().chunks(3){if socket.write_all(part).await.is_err(){break;}tokio::task::yield_now().await;}});
            let response=client(&json!({})).unwrap().get(url).send().await.unwrap();let result=sse(response).await;if complete{assert_eq!(result.unwrap()["answer"],"Привет");}else{assert!(result.unwrap_err().contains("оборван"));}server.await.unwrap();
        }
    }
}
