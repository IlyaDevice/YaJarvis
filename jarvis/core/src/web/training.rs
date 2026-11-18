use super::{App, bad, ok, reply};
use crate::training_records::{MAX_LIMIT_MB, Records};
use axum::{
    Json,
    body::Body,
    extract::{Query, State},
    http::{StatusCode, header},
    response::Response,
};
use serde::Deserialize;
use serde_json::Value;
use std::sync::Arc;

pub fn routes() -> axum::Router<App> {
    use axum::routing::{get, post};
    axum::Router::new()
        .route("/api/train/records", get(list).put(settings).delete(clear))
        .route("/api/train/records/clip", get(clip))
        .route("/api/train/records/move", post(move_clip))
        .route("/api/train/records/download", get(download))
        .route(
            "/api/train/background",
            get(background)
                .post(start_background)
                .delete(stop_background),
        )
        .route(
            "/api/train/background/cloud",
            axum::routing::put(connect).delete(disconnect),
        )
}

async fn file_operation<T: Send + 'static>(
    app: &App,
    f: impl FnOnce(Arc<Records>) -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    let records = app
        .plugins
        .training_records()
        .ok_or("Включите Rust-плагин «Обучение слова»")?;
    tokio::task::spawn_blocking(move || f(records))
        .await
        .map_err(|e| e.to_string())?
}

async fn list(State(app): State<App>) -> Response {
    let (mode, limit) = app.store.with(|s| (s.train_mode, s.train_limit_mb));
    let data = app.data_dir.clone();
    match file_operation(&app, move |r| {
        let previous = [crate::wake_profiles::TUNED_PREV, "wake.bin.prev"]
            .iter()
            .any(|name| data.join(name).is_file());
        Ok(r.snapshot(mode, limit, previous))
    })
    .await
    {
        Ok(value) => reply(StatusCode::OK, value),
        Err(e) => bad(e),
    }
}

#[derive(Deserialize)]
struct Settings {
    mode: Option<bool>,
    limit_mb: Option<i64>,
}

async fn settings(State(app): State<App>, Json(input): Json<Settings>) -> Response {
    if app.plugins.training_records().is_none() {
        return bad("Включите Rust-плагин «Обучение слова»");
    }
    match app.store.update(|s| -> std::io::Result<()> {
        if let Some(mode) = input.mode {
            s.train_mode = mode;
        }
        if let Some(limit) = input.limit_mb {
            s.train_limit_mb = limit.clamp(1, MAX_LIMIT_MB);
        }
        Ok(())
    }) {
        Ok(()) => ok(),
        Err(e) => bad(e.to_string()),
    }
}

#[derive(Deserialize)]
struct Clip {
    kind: String,
    name: String,
}

async fn clip(State(app): State<App>, Query(input): Query<Clip>) -> Response {
    match file_operation(&app, move |r| r.read(&input.kind, &input.name)).await {
        Ok(body) => audio_or_zip("audio/wav", None, body),
        Err(e) => bad(e),
    }
}

#[derive(Deserialize)]
struct Move {
    #[serde(flatten)]
    clip: Clip,
    to: String,
}

async fn move_clip(State(app): State<App>, Json(input): Json<Move>) -> Response {
    match file_operation(&app, move |r| {
        r.move_clip(&input.clip.kind, &input.clip.name, &input.to)
    })
    .await
    {
        Ok(()) => ok(),
        Err(e) => bad(e),
    }
}

async fn clear(State(app): State<App>) -> Response {
    match file_operation(&app, |r| r.clear()).await {
        Ok(()) => ok(),
        Err(e) => bad(e),
    }
}

async fn download(State(app): State<App>) -> Response {
    let data = app.data_dir.clone();
    let sensitivity = app.store.with(|s| s.wake_sensitivity);
    match file_operation(&app, move |r| r.archive(&data, sensitivity)).await {
        Ok(body) => {
            let name = format!(
                "attachment; filename=jarvis-train-{}.zip",
                jiff::Timestamp::now().strftime("%Y%m%d-%H%M%S")
            );
            audio_or_zip("application/zip", Some(&name), body)
        }
        Err(e) => bad(e),
    }
}

fn audio_or_zip(kind: &str, disposition: Option<&str>, body: Vec<u8>) -> Response {
    let mut response = Response::builder()
        .header(header::CONTENT_TYPE, kind)
        .header(header::CACHE_CONTROL, "no-store");
    if let Some(disposition) = disposition {
        response = response.header(header::CONTENT_DISPOSITION, disposition);
    }
    response.body(Body::from(body)).unwrap()
}

async fn background(State(app): State<App>) -> Response {
    background_response(
        app.plugins
            .training_records()
            .map(|r| r.background.status()),
    )
}

fn background_response(status: Option<Value>) -> Response {
    match status {
        Some(status) => reply(StatusCode::OK, status),
        None => bad("Включите Rust-плагин «Обучение слова»"),
    }
}

#[derive(Deserialize)]
struct Background {
    minutes: u32,
}

async fn start_background(State(app): State<App>, Json(input): Json<Background>) -> Response {
    let Some(records) = app.plugins.training_records() else {
        return bad("Включите Rust-плагин «Обучение слова»");
    };
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
    match records.background.start(input.minutes) {
        Ok(()) => background_response(Some(records.background.status())),
        Err(e) => bad(e),
    }
}

async fn stop_background(State(app): State<App>) -> Response {
    let Some(records) = app.plugins.training_records() else {
        return bad("Включите Rust-плагин «Обучение слова»");
    };
    records.background.stop();
    background_response(Some(records.background.status()))
}

async fn connect(
    State(app): State<App>,
    Json(login): Json<crate::training_background::Login>,
) -> Response {
    let Some(records) = app.plugins.training_records() else {
        return bad("Включите Rust-плагин «Обучение слова»");
    };
    match records.background.connect(login).await {
        Ok(()) => background_response(Some(records.background.status())),
        Err(e) => bad(e),
    }
}

async fn disconnect(State(app): State<App>) -> Response {
    let Some(records) = app.plugins.training_records() else {
        return bad("Включите Rust-плагин «Обучение слова»");
    };
    match records.background.disconnect().await {
        Ok(()) => background_response(Some(records.background.status())),
        Err(e) => bad(e),
    }
}
