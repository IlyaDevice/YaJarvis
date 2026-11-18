//! Text inference through the selected module. Tool execution stays in the core.
use crate::{plugins::Host,settings::{Profile,Settings},tools::Tools};
use serde_json::{Value,json};
use std::time::Duration;

fn configured<'a>(s:&'a Settings,id:&str)->&'a str {
    match id{"openai"=>&s.openai_key,"gemini"=>&s.gemini_key,"grok"=>&s.grok_key,_=>""}
}
pub async fn ask(host:&Host,s:&Settings,p:&Profile,instructions:&str,text:&str,tools:&Tools,defs:Vec<Value>)->Result<String,String>{
    if !p.llm_plugin.is_empty() {
        return ask_plugin(host, &p.llm_plugin, instructions, text, tools, &defs).await;
    }
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
                let declarations=crate::gemini::declarations(&defs);
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
            let result = run_tool(tools, &defs, &call).await?;
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
async fn run_tool(tools: &Tools, defs: &[Value], call: &Call) -> Result<String, String> {
    if call.name.is_empty() || call.name.len() > 100 || call.arguments.len() > 256 * 1024 {
        return Err("Неверный вызов инструмента".into());
    }
    if !defs.iter().any(|def| def["name"] == call.name) {
        return Err("Модель вызвала необъявленный инструмент".into());
    }
    Ok(tokio::time::timeout(Duration::from_secs(15), tools.call(&call.name, &call.arguments))
        .await.unwrap_or_else(|_| "Инструмент не ответил за 15 с".into()))
}

async fn ask_plugin(host: &Host, id: &str, instructions: &str, text: &str, tools: &Tools, defs: &[Value]) -> Result<String, String> {
    let mut messages = vec![json!({"role":"system","content":instructions}), json!({"role":"user","content":text})];
    let schemas: Vec<_> = defs.iter().filter(|def| def["type"] == "function").map(|def| {
        json!({"type":"function","function":{"name":def["name"],"description":def["description"],"parameters":def["parameters"]}})
    }).collect();
    for _ in 0..5 {
        let response = host.plugin_chat(id, json!({"messages":messages,"tools":schemas})).await?;
        let answer = response.as_str().or_else(|| response["text"].as_str()).unwrap_or_default().trim();
        let Some(calls) = response.get("tool_calls").and_then(Value::as_array).filter(|calls| !calls.is_empty()) else {
            if answer.is_empty() || answer.len() > 32 * 1024 {
                return Err("Текстовый плагин вернул пустой или слишком длинный ответ".into());
            }
            return Ok(answer.to_owned());
        };
        if calls.len() > 16 { return Err("Плагин вернул слишком много вызовов инструментов".into()); }
        let mut parsed = Vec::with_capacity(calls.len());
        let mut ids = std::collections::HashSet::new();
        for call in calls {
            let field = |key| call[key].as_str().ok_or("Неверный вызов инструмента от плагина");
            let id = field("id")?;
            let name = field("name")?;
            let arguments = field("arguments")?;
            if id.is_empty() || id.len() > 128 || !ids.insert(id)
                || arguments.len() > 32 * 1024 || !serde_json::from_str::<Value>(arguments).is_ok_and(|v| v.is_object())
                || !schemas.iter().any(|schema| schema["function"]["name"] == name)
            {
                return Err("Плагин вернул неверный или необъявленный инструмент".into());
            }
            parsed.push(Call { id: id.into(), name: name.into(), arguments: arguments.into() });
        }
        let tool_calls: Vec<_> = parsed.iter().map(|call| json!({"id":call.id,"type":"function","function":{"name":call.name,"arguments":call.arguments}})).collect();
        messages.push(json!({"role":"assistant","content":answer,"tool_calls":tool_calls}));
        for call in parsed {
            let output = run_tool(tools, defs, &call).await?;
            messages.push(json!({"role":"tool","tool_call_id":call.id,"content":output}));
        }
    }
    Err("Текстовый плагин не закончил за 5 шагов".into())
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
}

    #[cfg(feature = "lua")]
    #[tokio::test]
    async fn selected_lua_provider_can_use_a_declared_tool() {
        let dir = std::env::temp_dir().join(format!("jarvis-llm-{}", crate::settings::rand_hex(6)));
        let plugin = dir.join("plugins/example-llm");
        std::fs::create_dir_all(&plugin).unwrap();
        std::fs::write(plugin.join("plugin.json"), json!({
            "id":"example-llm","name":"Example","version":"1.0.0","kind":"lua","category":"assistant",
            "main":"main.lua","api":1,"provides":["llm"],"permissions":["tools"]
        }).to_string()).unwrap();
        std::fs::write(plugin.join("main.lua"), r#"return {
            chat = function(request)
                local last = request.messages[#request.messages]
                if last.role == 'tool' then return last.content end
                assert(request.messages[1].content == 'system text')
                assert(request.tools[1]["function"].name == 'probe')
                local name = last.content == 'denied' and 'undeclared' or 'probe'
                return { tool_calls = {{ id = 'one', name = name, arguments = '{"text":"works"}' }} }
            end,
            tools = {{name = 'probe', description = 'Test', parameters = {type = 'object'},
                handler = function(args) return args.text end}}
        }"#).unwrap();
        let host = Host::new(dir.clone(), crate::plugins::Hooks::default());
        host.load().await;
        assert_eq!(host.text_plugins()[0]["id"], "example-llm");
        let store = crate::settings::Store::open(dir.join("settings.json")).unwrap();
        let tools = Tools::new(host.clone(), store);
        let profile = Profile { llm_plugin: "example-llm".into(), ..Default::default() };
        let reply = ask(&host, &Settings::default(), &profile, "system text", "test", &tools, host.tools()).await;
        assert_eq!(reply.as_deref(), Ok("works"));
        let denied = ask(&host, &Settings::default(), &profile, "system text", "denied", &tools, host.tools()).await;
        assert!(denied.unwrap_err().contains("необъявленный"));
        host.set_enabled("example-llm", false).await.unwrap();
        assert!(host.text_plugins().is_empty());
        assert!(ask(&host, &Settings::default(), &profile, "system text", "test", &tools, vec![]).await.is_err());
        std::fs::remove_dir_all(dir).unwrap();
    }
}
