//! Text inference through the selected module. Tool execution stays in the core.
use crate::{plugins::Host,settings::{Profile,Settings},tools::Tools};
use serde_json::{Value,json};
use std::time::Duration;

fn configured<'a>(s:&'a Settings,id:&str)->&'a str {
    match id{"openai"=>&s.openai_key,"gemini"=>&s.gemini_key,"grok"=>&s.grok_key,_=>""}
}
pub async fn ask(host:&Host,s:&Settings,p:&Profile,instructions:&str,text:&str,tools:&Tools,defs:Vec<Value>)->Result<String,String>{
    let preferred=if ["openai","live",""].contains(&p.engine.as_str()){"openai"}else{p.engine.as_str()};
    if preferred=="chatgpt" && crate::chatgpt::signed_in(&s.chatgpt){
        let auth=host.provider("chatgpt")?.request("auth.token",Value::Null,Duration::from_secs(20)).await?;
        return crate::chatgpt::ask_codex(host,auth,p,instructions,text,Some(tools),defs).await;
    }
    let id=if !configured(s,preferred).is_empty(){preferred}else{["openai","gemini","grok"].into_iter().find(|id|!configured(s,id).is_empty()).unwrap_or("chatgpt")};
    if id=="chatgpt" {
        if !crate::chatgpt::signed_in(&s.chatgpt){return Err("Добавьте API-ключ или войдите в ChatGPT в «Подключениях»".into());}
        let auth=host.provider(id)?.request("auth.token",Value::Null,Duration::from_secs(20)).await?;
        return crate::chatgpt::ask_codex(host,auth,p,instructions,text,Some(tools),defs).await;
    }
    let provider=host.provider(id)?;let key=configured(s,id);
    let model=match id {
        "gemini"=>if p.backend_model.starts_with("gemini-"){p.backend_model.as_str()}else{"gemini-3.8-flash"},
        "grok"=>if p.backend_model.starts_with("grok-"){p.backend_model.as_str()}else{"grok-4-1-fast-non-reasoning"},
        _=>if p.backend_model.is_empty(){crate::settings::BACKEND_MODELS[0]}else{p.backend_model.as_str()},
    };
    let mut input=vec![json!({"role":"user","content":text})];
    let mut contents=vec![json!({"role":"user","parts":[{"text":text}]})];
    let mut messages=vec![json!({"role":"system","content":instructions}),json!({"role":"user","content":text})];
    for _ in 0..5 {
        let body=match id {
            "gemini"=>{
                let mut declarations=Vec::new();for def in &defs {if def["type"]=="function"{declarations.push(json!({"name":def["name"],"description":def["description"],"parameters":def["parameters"]}));}}
                let mut body=json!({"contents":contents,"systemInstruction":{"parts":[{"text":instructions}]}});
                let mut tools=vec![];if !declarations.is_empty(){tools.push(json!({"functionDeclarations":declarations}));}if p.web_search{tools.push(json!({"googleSearch":{}}));}if !tools.is_empty(){body["tools"]=tools.into();}body
            },
            "grok"=>{let tools:Vec<_>=defs.iter().filter(|d|d["type"]=="function").map(|d|json!({"type":"function","function":{"name":d["name"],"description":d["description"],"parameters":d["parameters"]}})).collect();let mut body=json!({"model":model,"messages":messages,"stream":false});if !tools.is_empty(){body["tools"]=tools.into();}body},
            _=>{let mut tools=defs.clone();if p.web_search{tools.push(json!({"type":"web_search"}));}json!({"model":model,"instructions":instructions,"input":input,"tools":tools,"reasoning":{"effort":crate::chatgpt::effort(model,&p.effort)},"store":false,"stream":false})},
        };
        let response=provider.request(id,json!({"op":"text","key":key,"model":model,"body":body}),Duration::from_secs(60)).await?;
        let (answer,calls)=decode(id,&response)?;
        if calls.is_empty(){return if answer.trim().is_empty(){Err("Модель ничего не ответила".into())}else{Ok(answer)};}
        match id {
            "gemini"=>contents.push(response["candidates"][0]["content"].clone()),
            "grok"=>messages.push(response["choices"][0]["message"].clone()),
            _=>input.extend(response["output"].as_array().cloned().unwrap_or_default()),
        }
        let mut gemini_results=vec![];
        for call in calls {
            if call.name.is_empty() || call.name.len()>100 || call.arguments.len()>256*1024{return Err("Неверный вызов инструмента".into());}
            if !defs.iter().any(|def|def["name"]==call.name){return Err("Модель вызвала необъявленный инструмент".into());}
            let result=tokio::time::timeout(Duration::from_secs(15),tools.call(&call.name,&call.arguments)).await.unwrap_or_else(|_|"Инструмент не ответил за 15 с".into());
            match id {
                "gemini"=>{let mut value=json!({"name":call.name,"response":{"result":result}});if !call.id.is_empty(){value["id"]=call.id.into();}gemini_results.push(json!({"functionResponse":value}));},
                "grok"=>messages.push(json!({"role":"tool","tool_call_id":call.id,"content":result})),
                _=>input.push(json!({"type":"function_call_output","call_id":call.id,"output":result})),
            }
        }
        if !gemini_results.is_empty(){contents.push(json!({"role":"user","parts":gemini_results}));}
    }
    Err("Модель не закончила за 5 шагов".into())
}
struct Call {name:String,arguments:String,id:String}
fn decode(id:&str,response:&Value)->Result<(String,Vec<Call>),String>{
    let mut answer=String::new();let mut calls=vec![];
    let field=|v:&Value,k:&str|v[k].as_str().unwrap_or("").to_string();
    match id {
        "gemini"=>{
            let parts=response["candidates"][0]["content"]["parts"].as_array().ok_or("Gemini не вернул ответ")?;
            for part in parts {if let Some(text)=part["text"].as_str().filter(|_|part["thought"]!=true){answer.push_str(text);}if part["functionCall"].is_object(){let c=&part["functionCall"];calls.push(Call{name:field(c,"name"),arguments:c["args"].to_string(),id:field(c,"id")});}}
        },
        "grok"=>{let message=&response["choices"][0]["message"];answer=field(message,"content");for call in message["tool_calls"].as_array().into_iter().flatten(){calls.push(Call{name:field(&call["function"],"name"),arguments:field(&call["function"],"arguments"),id:field(call,"id")});}},
        _=>{for item in response["output"].as_array().ok_or("OpenAI не вернул output")?{match item["type"].as_str(){Some("function_call")=>calls.push(Call{name:field(item,"name"),arguments:field(item,"arguments"),id:field(item,"call_id")}),Some("message")=>for part in item["content"].as_array().into_iter().flatten(){if part["type"]=="output_text"{answer.push_str(&field(part,"text"));}},_=>{}}}},
    }
    if calls.len()>32{return Err("Модель вернула слишком много инструментов".into());}Ok((answer,calls))
}
#[cfg(test)]mod tests{use super::*;#[test]fn decodes_provider_tool_calls_without_speaking_thoughts(){
    let (answer,calls)=decode("gemini",&json!({"candidates":[{"content":{"parts":[{"text":"private","thought":true},{"text":"Ответ"},{"functionCall":{"name":"test","args":{"a":1},"id":"x"}}]}}]})).unwrap();assert_eq!(answer,"Ответ");assert_eq!(calls[0].name,"test");assert_eq!(calls[0].arguments,"{\"a\":1}");
    let (_,calls)=decode("openai",&json!({"output":[{"type":"function_call","name":"x","arguments":"{}","call_id":"id"}]})).unwrap();assert_eq!(calls[0].id,"id");
}}
