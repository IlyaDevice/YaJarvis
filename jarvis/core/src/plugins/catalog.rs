//! Каталог официальных пакетов и открытых репозиториев из GitHub.

use super::{Host, valid_category, valid_id};
use serde_json::{Value, json};
use std::{
    future::Future,
    time::{Duration, Instant},
};
use tokio::sync::Mutex;

pub const TOPIC: &str = "yajarvis-plugin";

pub fn official_url(path: &str) -> String {
    let reference = option_env!("JARVIS_CATALOG_REF").unwrap_or("rust");
    format!(
        "https://raw.githubusercontent.com/{}/{reference}/plugins/{path}",
        crate::DEFAULT_REPO
    )
}

pub fn github_api() -> String {
    std::env::var("JARVIS_GITHUB_API").unwrap_or_else(|_| "https://api.github.com".into())
}

pub fn valid_repo(r: &str) -> bool {
    let ok = |s: &str| {
        !s.is_empty()
            && s.len() <= 100
            && s.bytes()
                .all(|c| c.is_ascii_alphanumeric() || b"-_.".contains(&c))
            && s != "."
            && s != ".."
    };
    r.split_once('/').is_some_and(|(a, b)| ok(a) && ok(b))
}

#[derive(Default)]
pub struct Cache {
    official: Source,
    community: Source,
}

#[derive(Default)]
struct Source {
    state: Mutex<Option<Snapshot>>,
}

struct Snapshot {
    items: Vec<Value>,
    error: String,
    checked: Instant,
}

impl Source {
    async fn get(
        &self,
        fresh: bool,
        fetch: impl Future<Output = Result<Vec<Value>, String>>,
    ) -> (Vec<Value>, String) {
        let requested = Instant::now();
        let mut state = self.state.lock().await;
        if let Some(snapshot) = state.as_ref() {
            let ttl = Duration::from_secs(if snapshot.error.is_empty() { 3600 } else { 300 });
            if snapshot.checked >= requested || (!fresh && snapshot.checked.elapsed() < ttl) {
                return (snapshot.items.clone(), snapshot.error.clone());
            }
        }
        let (items, error) = match fetch.await {
            Ok(items) => (items, String::new()),
            Err(error) => (
                state
                    .take()
                    .map(|snapshot| snapshot.items)
                    .unwrap_or_default(),
                error,
            ),
        };
        *state = Some(Snapshot {
            items: items.clone(),
            error: error.clone(),
            checked: Instant::now(),
        });
        (items, error)
    }
}

fn official_items(index: &Value) -> Result<Vec<Value>, String> {
    let entries = index["plugins"]
        .as_array()
        .filter(|items| !items.is_empty())
        .ok_or("GitHub вернул пустой каталог")?;
    let mut items = Vec::new();
    for mut item in entries.iter().cloned() {
        let id = item["id"]
            .as_str()
            .filter(|id| valid_id(id))
            .ok_or("Неверный ID в каталоге")?;
        let supported = match item["kind"].as_str() {
            Some("rust") => true,
            Some("lua") => cfg!(feature = "lua"),
            _ => continue,
        };
        if !supported || items.iter().any(|known: &Value| known["id"] == id) {
            continue;
        }
        item["official"] = true.into();
        item["repo"] = crate::DEFAULT_REPO.into();
        if !item["category"].as_str().is_some_and(valid_category) {
            item["category"] = "other".into();
        }
        items.push(item);
    }
    if items.is_empty() {
        return Err("В каталоге нет пакетов для этой версии Джарвиса".into());
    }
    Ok(items)
}

async fn fetch_official() -> Result<Vec<Value>, String> {
    let url = official_url("index.json");
    let raw = tokio::time::timeout(
        Duration::from_secs(12),
        crate::net::download(&url, 128 << 10),
    )
    .await
    .map_err(|_| "GitHub не ответил вовремя")??;
    let index = serde_json::from_slice(&raw).map_err(|e| e.to_string())?;
    official_items(&index)
}

fn topic_category(topics: &[Value]) -> &str {
    topics
        .iter()
        .filter_map(Value::as_str)
        .filter_map(|t| t.strip_prefix("yajarvis-"))
        .find(|c| valid_category(c))
        .unwrap_or("other")
}

async fn fetch_community() -> Result<Vec<Value>, String> {
    let query = format!(
        "{}/search/repositories?per_page=60&sort=stars&q=topic%3A{TOPIC}",
        github_api()
    );
    let response = crate::net::get_json(&query).await?;
    let repos = response["items"]
        .as_array()
        .ok_or("GitHub не вернул список репозиториев")?;
    Ok(repos.iter().filter_map(|repo| {
        let full = repo["full_name"].as_str()?;
        if repo["archived"].as_bool() == Some(true) || full.eq_ignore_ascii_case(crate::DEFAULT_REPO) {
            return None;
        }
        let name = repo["name"].as_str()?;
        let id = name.trim_start_matches("yajarvis-").trim_start_matches("jarvis-").to_lowercase();
        let category = topic_category(repo["topics"].as_array().map(Vec::as_slice).unwrap_or(&[]));
        Some(json!({"id": id, "repo": full, "name": name, "description": repo["description"],
            "author": repo["owner"]["login"], "stars": repo["stargazers_count"],
            "updated": repo["pushed_at"], "url": repo["html_url"], "category": category, "official": false}))
    }).collect())
}

impl Host {
    pub async fn official_catalog(&self, fresh: bool) -> (Vec<Value>, String) {
        let (mut items, error) = self.catalog.official.get(fresh, fetch_official()).await;
        for (manifest, compiled) in super::modules::catalog() {
            items.retain(|item| item["id"] != manifest.id);
            let mut item = serde_json::to_value(manifest).expect("module manifest");
            item["official"] = true.into();
            item["repo"] = crate::DEFAULT_REPO.into();
            item["bundled"] = compiled.into();
            items.push(item);
        }
        (items, error)
    }

    pub async fn community_catalog(&self, fresh: bool) -> (Vec<Value>, String) {
        self.catalog.community.get(fresh, fetch_community()).await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::future::ready;

    #[test]
    fn available_catalog_keeps_the_voice_packages() {
        let index = serde_json::from_str(include_str!("../../../../plugins/index.json")).unwrap();
        let items = official_items(&index).unwrap();
        for id in ["chatgpt", "openai", "gemini", "grok", "elevenlabs"] {
            assert!(
                items
                    .iter()
                    .any(|item| item["id"] == id && item["category"] == "assistant")
            );
        }
        assert!(official_items(&json!({"plugins":[]})).is_err());
    }

    #[tokio::test]
    async fn failed_refresh_keeps_the_last_valid_catalog() {
        let source = Source::default();
        let original = vec![json!({"id":"gemini"})];
        assert_eq!(
            source.get(false, ready(Ok(original.clone()))).await,
            (original.clone(), String::new())
        );
        let (items, error) = source.get(true, ready(Err("offline".into()))).await;
        assert_eq!(items, original);
        assert_eq!(error, "offline");
        let added = vec![json!({"id":"gemini"}), json!({"id":"new-provider"})];
        assert_eq!(
            source.get(true, ready(Ok(added.clone()))).await,
            (added, String::new())
        );
    }
}
