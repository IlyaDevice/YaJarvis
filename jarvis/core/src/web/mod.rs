//! Веб-настройки на самой колонке (http://<ip колонки>:8080). Та же панель и тот же API, что у Go-версии.
//! Защита:
//!   - пароль (bcrypt), сессия — подписанная cookie (HttpOnly, SameSite=Strict), 30 дней;
//!   - перебор пароля: после 5 ошибок с адреса — пауза, растущая до 15 минут;
//!   - только домашняя сеть (по умолчанию): запросы не из локальных адресов отклоняются;
//!   - изменения только с заголовком X-Jarvis (форма с чужого сайта так не сможет);
//!   - ключи и ссылка VLESS обратно в браузер не отдаются — только «задан, …1234»;
//!   - строгий CSP, запрет встраивания во фреймы.

mod auth;
mod plugins;
pub mod state;
mod ui;

use crate::{plugins::Host, settings::Store, vpn::Proxy};
use axum::{
    Json, Router,
    body::Body,
    extract::{ConnectInfo, DefaultBodyLimit, Request, State},
    http::{HeaderValue, Method, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{any, delete, get, post, put},
};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    net::{IpAddr, SocketAddr},
    path::PathBuf,
    sync::{Arc, Mutex},
    time::Instant,
};

pub struct Web {
    pub store: Store,
    pub plugins: Arc<Host>,
    pub proxy: Arc<Proxy>,
    pub mic: Arc<crate::audio::Mic>,
    pub wake: Arc<crate::wake::Listener>,
    pub data_dir: PathBuf,
    pub started: Instant,
    fails: Mutex<HashMap<IpAddr, auth::Fail>>,
    reset_at: Mutex<Option<Instant>>,
}

pub type App = Arc<Web>;

impl Web {
    pub fn new(store: Store, plugins: Arc<Host>, proxy: Arc<Proxy>, data_dir: PathBuf, mic: Arc<crate::audio::Mic>, wake: Arc<crate::wake::Listener>) -> App {
        Arc::new(Web { store, plugins, proxy, mic, wake, data_dir, started: Instant::now(), fails: Mutex::default(), reset_at: Mutex::default() })
    }
}

/// Ответ JSON с кодом.
pub fn reply(code: StatusCode, v: Value) -> Response {
    (code, Json(v)).into_response()
}

pub fn bad(msg: impl Into<String>) -> Response {
    reply(StatusCode::BAD_REQUEST, json!({"error": msg.into()}))
}

pub fn ok() -> Response {
    reply(StatusCode::OK, json!({"ok": true}))
}

pub fn router(app: App) -> Router {
    let api = Router::new()
        .route("/api/state", get(state::state))
        .route("/api/password", put(auth::change_password))
        .route("/api/plugins", get(plugins::list))
        .route("/api/plugins/catalog", get(plugins::catalog))
        .route("/api/plugins/install", post(plugins::install))
        .route("/api/plugins/upload", post(plugins::upload).layer(DefaultBodyLimit::max(crate::plugins::install::ZIP_MAX + 1)))
        .route("/api/plugins/warned", post(plugins::warned))
        .route("/api/plugins/{id}/enable", post(plugins::enable))
        .route("/api/plugins/{id}/settings", post(plugins::settings))
        .route("/api/plugins/{id}", delete(plugins::remove))
        .route("/api/onboarded", post(plugins::onboarded))
        .route("/p/{id}/{*file}", get(plugins::page_file))
        .route("/api/p/{id}/{*path}", any(plugins::page_proxy).layer(DefaultBodyLimit::max(16 << 20)))
        .route_layer(middleware::from_fn_with_state(app.clone(), auth::require));
    Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/api/login", post(auth::login))
        .route("/api/logout", post(auth::logout))
        .route("/api/setup", post(auth::setup))
        .merge(api)
        .route("/api/{*rest}", any(not_ported))
        .fallback(get(ui::serve))
        .layer(DefaultBodyLimit::max(256 << 10))
        .layer(middleware::from_fn_with_state(app.clone(), guard))
        .with_state(app)
}

/// Часть API, которая ещё живёт только в Go-версии.
async fn not_ported(req: Request) -> Response {
    log::info!("веб: {} {} — ещё не перенесено", req.method(), req.uri().path());
    reply(StatusCode::NOT_IMPLEMENTED, json!({"error": "Эта функция ещё не перенесена в новое ядро"}))
}

fn local_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v) => v.is_loopback() || v.is_private() || v.is_link_local(),
        IpAddr::V6(v) => {
            if let Some(v4) = v.to_ipv4_mapped() {
                return local_ip(IpAddr::V4(v4));
            }
            v.is_loopback() || (v.segments()[0] & 0xfe00) == 0xfc00 || (v.segments()[0] & 0xffc0) == 0xfe80
        }
    }
}

/// Общие правила для всех запросов.
async fn guard(State(app): State<App>, ConnectInfo(peer): ConnectInfo<SocketAddr>, req: Request, next: Next) -> Response {
    if app.store.with(|s| s.lan_only) && !local_ip(peer.ip()) {
        return (StatusCode::FORBIDDEN, "Доступ только из домашней сети").into_response();
    }
    if req.method() != Method::GET && req.headers().get("x-jarvis").is_none_or(|v| v != "1") {
        return (StatusCode::FORBIDDEN, "нужен заголовок X-Jarvis").into_response();
    }
    let mut resp = next.run(req).await;
    let h = resp.headers_mut();
    let set = |h: &mut axum::http::HeaderMap, k: &'static str, v: &'static str| {
        h.insert(k, HeaderValue::from_static(v));
    };
    set(h, "content-security-policy", "default-src 'self'; img-src 'self' data: https://i.ytimg.com https://avatars.yandex.net; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'");
    set(h, "x-frame-options", "DENY");
    set(h, "x-content-type-options", "nosniff");
    set(h, "referrer-policy", "no-referrer");
    if !h.contains_key(header::CACHE_CONTROL) {
        set(h, "cache-control", "no-store");
    }
    resp
}

pub async fn serve(app: App, addr: &str) -> std::io::Result<()> {
    let addr = if addr.starts_with(':') { format!("0.0.0.0{addr}") } else { addr.to_string() };
    let listener = tokio::net::TcpListener::bind(&addr).await?;
    log::info!("веб-настройки: http://<ip колонки>{}", &addr[addr.rfind(':').unwrap_or(0)..]);
    axum::serve(listener, router(app).into_make_service_with_connect_info::<SocketAddr>()).await
}

pub fn empty(code: StatusCode) -> Response {
    Response::builder().status(code).body(Body::empty()).unwrap()
}
