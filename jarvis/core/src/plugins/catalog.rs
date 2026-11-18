//! Каталог: официальные плагины (plugins/index.json в репозитории Джарвиса) и найденные на GitHub
//! по теме yajarvis-plugin (неофициальные). Плюс автоустановка официальных плагинов, которые нужны
//! версии ядра при текущих настройках (правило auto в index.json).

use super::{Host, valid_category, valid_id};
use serde_json::{Map, Value, json};
use std::{
    path::Path,
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

pub const TOPIC: &str = "yajarvis-plugin";

pub fn github_api() -> String {
    std::env::var("JARVIS_GITHUB_API").unwrap_or_else(|_| "https://api.github.com".into())
}

pub fn github_raw() -> String {
    std::env::var("JARVIS_GITHUB_RAW").unwrap_or_else(|_| "https://raw.githubusercontent.com".into())
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

/// Нужен ли официальный плагин при таких настройках; from — версия ядра, с которой переходим ("" — свежая установка).
pub fn auto_needed(auto: &Value, settings: &Map<String, Value>, from: &str) -> bool {
    if !auto.is_object() {
        return false;
    }
    if let Some(before) = auto.get("before").and_then(Value::as_str).filter(|b| !b.is_empty()) {
        if from.is_empty() || !super::newer(before, from) {
            return false;
        }
    }
    if auto.get("always").and_then(Value::as_bool) == Some(true) {
        return true;
    }
    fn set(v: &Value) -> bool {
        match v {
            Value::Null => false,
            Value::String(s) => !s.is_empty(),
            Value::Bool(b) => *b,
            Value::Number(n) => n.as_f64() != Some(0.0),
            Value::Object(m) => m.values().any(set),
            Value::Array(a) => !a.is_empty(),
        }
    }
    auto.get("settings").and_then(Value::as_array).into_iter().flatten().filter_map(Value::as_str).any(|k| settings.get(k).is_some_and(set))
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
        let mut out:Vec<Value> = super::modules::manifests().into_iter().map(|m| {
            let mut v=serde_json::to_value(m).expect("manifest module");
            v["official"]=true.into();v["repo"]=crate::DEFAULT_REPO.into();v["bundled"]=true.into();v
        }).collect();
        let mut errs = vec![];
        let index = format!("{}/{}/main/plugins/index.json", github_raw(), crate::DEFAULT_REPO);
        match crate::net::get_json(&index).await {
            Ok(idx) => {
                for mut it in idx.get("plugins").and_then(Value::as_array).cloned().unwrap_or_default() {
                    if out.iter().any(|m|m["id"]==it["id"]) {continue;}
                    let Some(o) = it.as_object_mut() else { continue };
                    if !o.get("id").and_then(Value::as_str).is_some_and(valid_id) {
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
            Err(e) => errs.push(format!("официальный список: {e}")),
        }
        let q = format!("{}/search/repositories?per_page=60&sort=stars&q=topic%3A{TOPIC}", github_api());
        match crate::net::get_json(&q).await {
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

    /// Поставить официальные плагины, которые нужны версии tag при текущих настройках. Удалённые руками не ставит.
    pub async fn ensure_for(self: &Arc<Self>, tag: &str, for_next: bool, settings: &Map<String, Value>, upgrade_from: &str) -> Result<Vec<String>, String> {
        let r#ref = if tag.is_empty() || tag == "vdev" || tag.ends_with("-rs") { "main" } else { tag };
        let link = format!("{}/{}/{}/plugins/index.json", github_raw(), crate::DEFAULT_REPO, r#ref);
        let idx = match crate::net::get_json(&link).await {
            Ok(v) => v,
            Err(e) if e.contains("404") => return Ok(vec![]),
            Err(e) => return Err(format!("список плагинов версии {}: {e}", r#ref)),
        };
        let mut done = vec![];
        for it in idx.get("plugins").and_then(Value::as_array).cloned().unwrap_or_default() {
            let id = it.get("id").and_then(Value::as_str).unwrap_or("");
            let from = if for_next { crate::version() } else { upgrade_from };
            let mut selected = settings.clone();
            if id=="home" {
                let g=self.inner.lock().unwrap();
                for (module,field) in [("homeassistant","home_assistant"),("yandexhome","yandex_token")] {
                    if g.plugins.get(module).is_some_and(|p|p.manifest.kind=="module") || g.state.get(module).is_some_and(|s|s.removed) {selected.remove(field);}
                }
            }
            if !valid_id(id) || matches!(id, "js" | "training") || !auto_needed(it.get("auto").unwrap_or(&Value::Null), &selected, from) {
                continue;
            }
            let skip = {
                let g = self.inner.lock().unwrap();
                g.plugins.contains_key(id) || g.state.get(id).is_some_and(|s| s.removed)
            };
            if skip {
                continue;
            }
            let rel_tag = if r#ref == "main" { "" } else { tag };
            let name = it.get("name").and_then(Value::as_str).unwrap_or(id);
            self.install_repo(crate::DEFAULT_REPO, id, rel_tag, for_next).await.map_err(|e| format!("плагин «{name}»: {e}"))?;
            log::info!("плагины: {id} поставлен сам — он нужен Джарвису {}", r#ref);
            done.push(id.to_string());
        }
        Ok(done)
    }
}

/// С какой версии ядра обновилась колонка: "" — свежая установка, "0" — старше 0.9.71. Вызывать до Store::open.
pub fn detect_upgrade(data: &Path) -> String {
    if let Ok(v) = std::fs::read_to_string(data.join("core-version")) {
        if !v.trim().is_empty() {
            return v.trim().into();
        }
    }
    if data.join("settings.json").exists() { "0".into() } else { String::new() }
}

pub fn mark_core_version(data: &Path) {
    let v = crate::version();
    if v != "dev" && !v.ends_with("-rs") {
        let _ = std::fs::write(data.join("core-version"), v);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn auto_rules() {
        let s: Map<String, Value> = serde_json::from_str(r#"{"vless":"x","dev_mode":false,"vpn":{"a":false}}"#).unwrap();
        assert!(auto_needed(&json!({"settings":["vless"]}), &s, "0"));
        assert!(!auto_needed(&json!({"settings":["dev_mode","vpn"]}), &s, "0"));
        assert!(!auto_needed(&json!({"always":true,"before":"0.9.71"}), &s, ""));
        assert!(auto_needed(&json!({"always":true,"before":"0.9.71"}), &s, "0.9.70"));
        assert!(valid_repo("newfpv/YaJavis") && !valid_repo("a/../b") && !valid_repo("x"));
    }
}
