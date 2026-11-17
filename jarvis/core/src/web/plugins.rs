//! Плагины в панели: список, каталог, установка, включение, настройки, удаление, страницы плагинов.

use super::{App, bad, ok, reply};
use crate::plugins::{PLUGIN_API, arch, catalog::TOPIC, categories_json, install::free_mb};
use axum::{
    Json,
    body::{Body, Bytes},
    extract::{Path, Query, Request, State},
    http::{HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde_json::{Map, Value, json};
use std::collections::HashMap;

pub async fn list(State(app): State<App>) -> Json<Value> {
    Json(json!({
        "installed": app.plugins.list(), "composition_pending": false, "modules": crate::plugins::compiled_modules(), "arch": arch(), "api": PLUGIN_API,
        "free_mb": free_mb(&app.data_dir), "categories": categories_json(), "warned": app.store.with(|s| s.plugins_warned),
        "topic": TOPIC, "repo": crate::DEFAULT_REPO,
    }))
}

pub async fn catalog(State(app): State<App>, Query(q): Query<HashMap<String, String>>) -> Json<Value> {
    let (items, err) = app.plugins.catalog(q.get("fresh").is_some_and(|v| v == "1")).await;
    Json(json!({"items": items, "error": err}))
}

fn field<'a>(v: &'a Value, k: &str) -> Option<&'a Value> {
    v.as_object()?.iter().find(|(key, _)| key.eq_ignore_ascii_case(k)).map(|(_, v)| v)
}

pub async fn install(State(app): State<App>, body: Bytes) -> Response {
    let Ok(req) = serde_json::from_slice::<Value>(&body) else { return bad("нет репозитория") };
    let repo = field(&req, "repo").and_then(Value::as_str).unwrap_or("");
    let id = field(&req, "id").and_then(Value::as_str).unwrap_or("");
    if (repo.is_empty() || repo==crate::DEFAULT_REPO) && crate::plugins::compiled_modules().iter().any(|m|m==id) {
        return match app.plugins.install_module(id).await {Ok(m)=>reply(StatusCode::OK,json!({"ok":true,"plugin":m})),Err(e)=>bad(e)};
    }
    match app.plugins.install_repo(repo, id, "", false).await {
        Ok(m) => reply(StatusCode::OK, json!({"ok": true, "plugin": m})),
        Err(e) => bad(e),
    }
}

pub async fn upload(State(app): State<App>, body: Bytes) -> Response {
    match app.plugins.install(&body, "file", false, false).await {
        Ok(m) => reply(StatusCode::OK, json!({"ok": true, "plugin": m})),
        Err(e) => bad(e),
    }
}

pub async fn enable(State(app): State<App>, Path(id): Path<String>, body: Bytes) -> Response {
    let req: Value = serde_json::from_slice(&body).unwrap_or(Value::Null);
    let on = field(&req, "on").and_then(Value::as_bool).unwrap_or(false);
    match app.plugins.set_enabled(&id, on).await {
        Ok(()) => ok(),
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
        Ok(()) => ok(),
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

/// Файл страницы плагина: только из его ui/, без выхода наверх.
pub async fn page_file(State(app): State<App>, Path((id, file)): Path<(String, String)>) -> Response {
    let dir = app.plugins.inner.lock().unwrap().plugins.get(&id).filter(|p| p.rt.is_some()).map(|p| p.dir.clone());
    let Some(dir) = dir else { return super::empty(StatusCode::NOT_FOUND) };
    if file.split('/').any(|p| p == ".." || p.is_empty()) || file.contains('\\') {
        return super::empty(StatusCode::NOT_FOUND);
    }
    let Ok(b) = tokio::fs::read(dir.join("ui").join(&file)).await else { return super::empty(StatusCode::NOT_FOUND) };
    let ct = match file.rsplit('.').next() {
        Some("js") => "text/javascript; charset=utf-8",
        Some("css") => "text/css; charset=utf-8",
        Some("html") => "text/html; charset=utf-8",
        Some("svg") => "image/svg+xml",
        Some("png") => "image/png",
        Some("json") => "application/json",
        _ => "application/octet-stream",
    };
    Response::builder().header(header::CONTENT_TYPE, ct).header(header::CACHE_CONTROL, "no-cache").body(Body::from(b)).unwrap()
}

/// Запрос страницы к самому плагину: тело и ответ идут потоком (архивы, звук).
pub async fn page_proxy(State(app): State<App>, Path((id, path)): Path<(String, String)>, req: Request) -> Response {
    let http = app.plugins.inner.lock().unwrap().plugins.get(&id).and_then(|p| p.rt.as_ref()?.native()?.http());
    let Some((port, token)) = http else {
        return reply(StatusCode::SERVICE_UNAVAILABLE, json!({"error": "плагин не запущен"}));
    };
    let mut url = format!("http://127.0.0.1:{port}/{path}");
    if let Some(q) = req.uri().query() {
        url = format!("{url}?{q}");
    }
    let method = reqwest::Method::from_bytes(req.method().as_str().as_bytes()).unwrap_or(reqwest::Method::GET);
    let client = local_client();
    let mut rb = client.request(method, &url).header("X-Jarvis-Plugin", token);
    for h in ["content-type", "range", "if-modified-since", "if-none-match"] {
        if let Some(v) = req.headers().get(h) {
            rb = rb.header(h, v.as_bytes());
        }
    }
    let body = reqwest::Body::wrap_stream(req.into_body().into_data_stream());
    let resp = match rb.body(body).send().await {
        Ok(r) => r,
        Err(_) => return reply(StatusCode::BAD_GATEWAY, json!({"error": "плагин не ответил"})),
    };
    let mut out = Response::builder().status(resp.status().as_u16());
    for h in ["content-type", "content-length", "content-disposition", "content-range", "accept-ranges", "cache-control", "last-modified", "etag"] {
        if let Some(v) = resp.headers().get(h).and_then(|v| HeaderValue::from_bytes(v.as_bytes()).ok()) {
            out = out.header(h, v);
        }
    }
    out.body(Body::from_stream(resp.bytes_stream())).unwrap_or_else(|_| StatusCode::BAD_GATEWAY.into_response())
}

fn local_client() -> &'static reqwest::Client {
    static C: std::sync::OnceLock<reqwest::Client> = std::sync::OnceLock::new();
    C.get_or_init(|| reqwest::Client::builder().no_proxy().no_gzip().build().expect("http"))
}
