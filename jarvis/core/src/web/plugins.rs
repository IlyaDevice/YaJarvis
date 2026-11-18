//! Плагины в панели: список, каталог, установка, включение, настройки и удаление.

use super::{App, bad, ok, reply};
use crate::plugins::{PLUGIN_API, arch, catalog::TOPIC, categories_json, install::free_mb};
use axum::{
    Json,
    body::Bytes,
    extract::{Path, Query, State},
    http::StatusCode,
    response::Response,
};
use serde_json::{Map, Value, json};
use std::collections::HashMap;

pub async fn list(State(app): State<App>) -> Json<Value> {
    Json(json!({
        "installed": app.plugins.list(), "composition_pending": app.data_dir.join(jarvis_station::PENDING).exists(),
        "modules": crate::plugins::compiled_modules(), "arch": arch(), "api": PLUGIN_API,
        "free_mb": free_mb(&app.data_dir), "categories": categories_json(), "warned": app.store.with(|s| s.plugins_warned),
        "topic": TOPIC, "repo": crate::DEFAULT_REPO,
    }))
}

pub async fn catalog(State(app): State<App>, Query(q): Query<HashMap<String, String>>) -> Json<Value> {
    let fresh = q.get("fresh").is_some_and(|v| v == "1");
    let (items, err) = match q.get("scope").map(String::as_str) {
        Some("official") => app.plugins.official_catalog(fresh).await,
        Some("community") => app.plugins.community_catalog(fresh).await,
        _ => {
            let ((mut official, official_error), (community, community_error)) = tokio::join!(
                app.plugins.official_catalog(fresh),
                app.plugins.community_catalog(fresh),
            );
            official.extend(community);
            let error = [official_error, community_error].into_iter()
                .filter(|error| !error.is_empty()).collect::<Vec<_>>().join("; ");
            (official, error)
        }
    };
    Json(json!({"items": items, "error": err}))
}

fn field<'a>(v: &'a Value, k: &str) -> Option<&'a Value> {
    v.as_object()?.iter().find(|(key, _)| key.eq_ignore_ascii_case(k)).map(|(_, v)| v)
}

pub async fn install(State(app): State<App>, body: Bytes) -> Response {
    let Ok(req) = serde_json::from_slice::<Value>(&body) else { return bad("нет репозитория") };
    let repo = field(&req, "repo").and_then(Value::as_str).unwrap_or("");
    let id = field(&req, "id").and_then(Value::as_str).unwrap_or("");
    let repo = if repo.is_empty() { crate::DEFAULT_REPO } else { repo };
    if repo.eq_ignore_ascii_case(crate::DEFAULT_REPO) {
        if crate::plugins::compiled_modules().iter().any(|module| module == id) {
            return match app.plugins.install_module(id).await {Ok(m)=>reply(StatusCode::OK,json!({"ok":true,"plugin":m})),Err(e)=>bad(e)};
        }
    }
    match app.plugins.install_repo(repo, id, "").await {
        Ok(m) => reply(StatusCode::OK, json!({"ok": true, "plugin": m})),
        Err(e) => bad(e),
    }
}

pub async fn upload(State(app): State<App>, body: Bytes) -> Response {
    match app.plugins.install(&body, "file", false).await {
        Ok(m) => reply(StatusCode::OK, json!({"ok": true, "plugin": m})),
        Err(e) => bad(e),
    }
}

pub async fn enable(State(app): State<App>, Path(id): Path<String>, body: Bytes) -> Response {
    let req: Value = serde_json::from_slice(&body).unwrap_or(Value::Null);
    let on = field(&req, "on").and_then(Value::as_bool).unwrap_or(false);
    match app.plugins.set_enabled(&id, on).await {
        Ok(()) => {
            if id == "training" && !on { app.engine.set_background_training(None); }
            ok()
        },
        Err(e) => bad(e),
    }
}

pub async fn settings(State(app): State<App>, Path(id): Path<String>, body: Bytes) -> Response {
    let Ok(req) = serde_json::from_slice::<Map<String, Value>>(&body) else { return bad("настройки не JSON") };
    match app.plugins.set_settings(&id, &req) {
        Ok(()) => ok(),
        Err(e) => bad(e),
    }
}

pub async fn remove(State(app): State<App>, Path(id): Path<String>) -> Response {
    match app.plugins.uninstall(&id).await {
        Ok(()) => {
            if id == "training" { app.engine.set_background_training(None); }
            app.updater.invalidate();
            ok()
        },
        Err(e) => bad(e),
    }
}

pub async fn warned(State(app): State<App>) -> Response {
    let _ = app.store.update(|s| {
        s.plugins_warned = true;
        Ok::<(), std::io::Error>(())
    });
    ok()
}

/// Мастер знакомства пройден (или пропущен); ?again=1 — показать снова.
pub async fn onboarded(State(app): State<App>, Query(q): Query<HashMap<String, String>>) -> Response {
    let done = q.get("again").is_none_or(|v| v != "1");
    let _ = app.store.update(|s| {
        s.onboarded = done;
        Ok::<(), std::io::Error>(())
    });
    ok()
}
