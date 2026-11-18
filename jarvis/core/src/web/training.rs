use super::{App, bad, ok, reply};
use crate::training_io::{self, Transfer};
use axum::{
    Json,
    body::Body,
    extract::{Query, State},
    http::{StatusCode, header},
    response::Response,
};
use serde::Deserialize;
use serde_json::{Value, json};
use std::time::Duration;

pub fn routes() -> axum::Router<App> {
    use axum::routing::{get, post, put};
    axum::Router::new()
        .route("/api/train/records", get(list).put(settings).delete(clear))
        .route("/api/train/records/clip", get(clip))
        .route("/api/train/records/move", post(move_clip))
        .route("/api/train/records/compact", post(compact))
        .route("/api/train/records/download", get(download))
        .route(
            "/api/train/background",
            get(background)
                .post(start_background)
                .delete(stop_background),
        )
        .route(
            "/api/train/background/cloud",
            put(connect).delete(disconnect),
        )
}

async fn request(app: &App, method: &str, params: Value) -> Result<Value, String> {
    training_io::call(&app.plugins, method, params, Duration::from_secs(35)).await
}

fn result(result: Result<Value, String>) -> Response {
    match result {
        Ok(Value::Null) => ok(),
        Ok(value) => reply(StatusCode::OK, value),
        Err(error) => bad(error),
    }
}

async fn list(State(app): State<App>) -> Response {
    let (mode, limit) = app.store.with(|s| (s.train_mode, s.train_limit_mb));
    result(request(&app, "training.records", json!({"mode":mode,"limit":limit})).await)
}

async fn compact(State(app): State<App>) -> Response {
    result(request(&app, "training.compact", Value::Null).await)
}

#[derive(Deserialize)]
struct Settings {
    mode: Option<bool>,
    limit_mb: Option<i64>,
}

async fn settings(State(app): State<App>, Json(input): Json<Settings>) -> Response {
    match app.store.update(|s| -> std::io::Result<()> {
        if let Some(mode) = input.mode {
            s.train_mode = mode;
        }
        if let Some(limit) = input.limit_mb {
            s.train_limit_mb = limit.clamp(1, 5);
        }
        Ok(())
    }) {
        Ok(()) => ok(),
        Err(error) => bad(error.to_string()),
    }
}

#[derive(Deserialize)]
struct Clip {
    kind: String,
    name: String,
}

async fn clip(State(app): State<App>, Query(input): Query<Clip>) -> Response {
    download_file(
        &app,
        "training.clip",
        json!({"kind":input.kind,"name":input.name}),
        "audio/wav",
        None,
    )
    .await
}

#[derive(Deserialize)]
struct Move {
    #[serde(flatten)]
    clip: Clip,
    to: String,
}

async fn move_clip(State(app): State<App>, Json(input): Json<Move>) -> Response {
    result(
        request(
            &app,
            "training.move",
            json!({"kind":input.clip.kind,"name":input.clip.name,"to":input.to}),
        )
        .await,
    )
}

async fn clear(State(app): State<App>) -> Response {
    result(request(&app, "training.clear", Value::Null).await)
}

async fn download(State(app): State<App>) -> Response {
    let sensitivity = app.store.with(|s| s.wake_sensitivity);
    let name = format!(
        "attachment; filename=jarvis-train-{}.zip",
        jiff::Timestamp::now().strftime("%Y%m%d-%H%M%S")
    );
    download_file(
        &app,
        "training.archive",
        json!({"sensitivity":sensitivity}),
        "application/zip",
        Some(&name),
    )
    .await
}

async fn download_file(
    app: &App,
    method: &str,
    mut params: Value,
    mime: &str,
    disposition: Option<&str>,
) -> Response {
    let transfer = match Transfer::new() {
        Ok(transfer) => transfer,
        Err(error) => return bad(error),
    };
    params["file"] = json!(transfer.path());
    if let Err(error) = request(app, method, params).await {
        return bad(error);
    }
    let body = match tokio::fs::read(transfer.path()).await {
        Ok(body) => body,
        Err(error) => return bad(error.to_string()),
    };
    let mut response = Response::builder()
        .header(header::CONTENT_TYPE, mime)
        .header(header::CACHE_CONTROL, "no-store");
    if let Some(disposition) = disposition {
        response = response.header(header::CONTENT_DISPOSITION, disposition);
    }
    response.body(Body::from(body)).unwrap()
}

async fn background(State(app): State<App>) -> Response {
    result(request(&app, "training.background.status", Value::Null).await)
}

#[derive(Deserialize)]
struct Background {
    minutes: u32,
}

async fn start_background(State(app): State<App>, Json(input): Json<Background>) -> Response {
    if !app.engine.mic.online()
        || !app
            .engine
            .listener
            .ready
            .load(std::sync::atomic::Ordering::Acquire)
        || !app.store.with(|s| s.wake_enabled)
    {
        return bad("Для записи фона включите микрофон и распознавание фразы активации");
    }
    let started = request(
        &app,
        "training.background.start",
        json!({"minutes":input.minutes}),
    )
    .await;
    if started.is_ok() {
        app.engine.set_background_training(Some(input.minutes));
    }
    result(started)
}

async fn stop_background(State(app): State<App>) -> Response {
    app.engine.set_background_training(None);
    result(request(&app, "training.background.stop", Value::Null).await)
}

async fn connect(State(app): State<App>, Json(input): Json<Value>) -> Response {
    result(request(&app, "training.background.connect", input).await)
}

async fn disconnect(State(app): State<App>) -> Response {
    let disconnected = request(&app, "training.background.disconnect", Value::Null).await;
    if disconnected.is_ok() {
        app.engine.set_background_training(None);
    }
    result(disconnected)
}
