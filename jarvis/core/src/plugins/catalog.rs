//! Каталог: официальные плагины (plugins/index.json в репозитории Джарвиса) и найденные на GitHub
//! по теме yajarvis-plugin (неофициальные).

use super::{Host, valid_category, valid_id};
use serde_json::{Value, json};
use std::{
    sync::Mutex,
    time::{Duration, Instant},
};

pub const TOPIC: &str = "yajarvis-plugin";

pub fn official_url(path: &str) -> String {
    let reference = option_env!("JARVIS_PLUGIN_REF").unwrap_or("rust");
    format!("https://raw.githubusercontent.com/{}/{reference}/plugins/{path}", crate::DEFAULT_REPO)
}

pub fn github_api() -> String {
    std::env::var("JARVIS_GITHUB_API").unwrap_or_else(|_| "https://api.github.com".into())
}

pub fn valid_repo(r: &str) -> bool {
    let ok = |s: &str| !s.is_empty() && s.len() <= 100 && s.bytes().all(|c| c.is_ascii_alphanumeric() || b"-_.".contains(&c)) && s != "." && s != "..";
    r.split_once('/').is_some_and(|(a, b)| ok(a) && ok(b))
}

#[derive(Default)]
pub struct Cache {
    v: Mutex<Option<(Vec<Value>, Instant, String)>>,
}

fn topic_category(topics: &[Value]) -> String {
    topics
        .iter()
        .filter_map(Value::as_str)
        .filter_map(|t| t.strip_prefix("yajarvis-"))
        .find(|c| valid_category(c))
        .unwrap_or("other")
        .to_string()
}

impl Host {
    /// Каталог; кеш на час (провал — на 5 минут).
    pub async fn catalog(&self, fresh: bool) -> (Vec<Value>, String) {
        if !fresh {
            if let Some((items, at, err)) = self.catalog.v.lock().unwrap().clone() {
                if at.elapsed() < Duration::from_secs(3600) {
                    return (items, err);
                }
            }
        }
        let mut out:Vec<Value> = super::modules::catalog().map(|(m, compiled)| {
            let mut v=serde_json::to_value(m).expect("manifest module");
            v["official"]=true.into();v["repo"]=crate::DEFAULT_REPO.into();v["bundled"]=compiled.into();v
        }).collect();
        let mut errs = vec![];
        let q = format!("{}/search/repositories?per_page=60&sort=stars&q=topic%3A{TOPIC}", github_api());
        let index_url = official_url("index.json");
        let (index, community) = tokio::join!(
            crate::net::download(&index_url, 128 << 10),
            crate::net::get_json(&q),
        );
        match index.and_then(|raw| serde_json::from_slice::<Value>(&raw).map_err(|e| e.to_string())) {
            Ok(idx) => {
                for mut it in idx.get("plugins").and_then(Value::as_array).cloned().unwrap_or_default() {
                    if out.iter().any(|m|m["id"]==it["id"]) {continue;}
                    let Some(o) = it.as_object_mut() else { continue };
                    if !o.get("id").and_then(Value::as_str).is_some_and(valid_id) {
                        continue;
                    }
                    if o.get("kind").and_then(Value::as_str) != Some("module")
                        && !(cfg!(feature = "lua") && o.get("kind").and_then(Value::as_str) == Some("lua")) {
                        continue;
                    }
                    o.insert("official".into(), true.into());
                    o.insert("repo".into(), crate::DEFAULT_REPO.into());
                    if !o.get("category").and_then(Value::as_str).is_some_and(valid_category) {
                        o.insert("category".into(), "other".into());
                    }
                    out.push(it);
                }
            }
            Err(error) => errs.push(format!("официальный каталог: {error}")),
        }
        match community {
            Ok(found) => {
                for r in found.get("items").and_then(Value::as_array).cloned().unwrap_or_default() {
                    let full = r.get("full_name").and_then(Value::as_str).unwrap_or("");
                    if r.get("archived").and_then(Value::as_bool) == Some(true) || full.eq_ignore_ascii_case(crate::DEFAULT_REPO) {
                        continue;
                    }
                    let name = r.get("name").and_then(Value::as_str).unwrap_or("");
                    let id = name.trim_start_matches("yajarvis-").trim_start_matches("jarvis-").to_lowercase();
                    out.push(json!({"id": id, "repo": full, "name": name, "description": r.get("description").cloned().unwrap_or(Value::Null),
                        "author": r.pointer("/owner/login").cloned().unwrap_or(Value::Null), "stars": r.get("stargazers_count").cloned().unwrap_or(0.into()),
                        "updated": r.get("pushed_at").cloned().unwrap_or(Value::Null), "url": r.get("html_url").cloned().unwrap_or(Value::Null),
                        "category": topic_category(r.get("topics").and_then(Value::as_array).map(Vec::as_slice).unwrap_or(&[])), "official": false}));
                }
            }
            Err(e) => errs.push(format!("поиск на GitHub: {e}")),
        }
        let err = errs.join("; ");
        let mut at = Instant::now();
        if out.is_empty() && !err.is_empty() {
            at = at.checked_sub(Duration::from_secs(55 * 60)).unwrap_or(at); // не кешируем провал надолго
        }
        *self.catalog.v.lock().unwrap() = Some((out.clone(), at, err.clone()));
        (out, err)
    }


}
