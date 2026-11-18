use super::{App, bad, ok, reply};
use crate::wake_profiles;
use axum::{
    Json,
    body::Bytes,
    extract::{Path, Query, State},
    http::StatusCode,
    response::Response,
};
use serde::Deserialize;
use serde_json::json;

#[derive(Deserialize)]
pub struct NamedModel {
    phrase: String,
}

#[derive(Deserialize)]
pub struct Selection {
    id: String,
}

pub async fn list(State(app): State<App>) -> Response {
    let path = app.data_dir.clone();
    let list = tokio::task::spawn_blocking(move || wake_profiles::list(&path)).await;
    match list {
        Ok(profiles) => reply(
            StatusCode::OK,
            json!({
                "profiles":profiles, "selected":app.store.with(|s| s.wake_profile.clone()),
                "phrase":app.engine.listener.phrase(),
                "training":cfg!(feature="wake-training")
            }),
        ),
        Err(_) => bad("Не удалось прочитать профили слова"),
    }
}

pub async fn upload(
    State(app): State<App>,
    Query(input): Query<NamedModel>,
    body: Bytes,
) -> Response {
    let guard = app.wake_update.clone().lock_owned().await;
    let path = app.data_dir.clone();
    let store = app.store.clone();
    let result = tokio::task::spawn_blocking(move || {
        let _guard = guard;
        wake_profiles::save(&path, &input.phrase, &body, &store)
    })
    .await;
    respond_profile(result)
}

fn respond_profile(
    result: Result<Result<wake_profiles::Profile, String>, tokio::task::JoinError>,
) -> Response {
    match result {
        Ok(Ok(profile)) => reply(StatusCode::OK, json!({"ok":true,"profile":profile})),
        Ok(Err(error)) => bad(error),
        Err(_) => bad("Не удалось сохранить фразу"),
    }
}

pub async fn select(State(app): State<App>, Json(input): Json<Selection>) -> Response {
    let guard = app.wake_update.clone().lock_owned().await;
    let path = app.data_dir.clone();
    let store = app.store.clone();
    let listener = app.engine.listener.clone();
    let result = tokio::task::spawn_blocking(move || {
        let _guard = guard;
        wake_profiles::select(&path, &input.id, &store)?;
        listener.request_reload();
        Ok::<(), String>(())
    })
    .await;
    match result {
        Ok(Ok(())) => ok(),
        Ok(Err(error)) => bad(error),
        Err(_) => bad("Не удалось включить модель"),
    }
}

pub async fn remove(State(app): State<App>, Path(id): Path<String>) -> Response {
    let guard = app.wake_update.clone().lock_owned().await;
    let path = app.data_dir.clone();
    let store = app.store.clone();
    let result = tokio::task::spawn_blocking(move || {
        let _guard = guard;
        wake_profiles::remove(&path, &id, &store)
    })
    .await;
    match result {
        Ok(Ok(())) => ok(),
        Ok(Err(error)) => bad(error),
        Err(_) => bad("Не удалось удалить модель"),
    }
}

#[cfg(feature = "wake-training")]
pub async fn capture(State(app): State<App>) -> Response {
    const SAMPLES: usize = jarvis_wake_train::SAMPLES;
    let Ok(guard) = app.wake_update.clone().try_lock_owned() else {
        return bad("Идёт другая запись или обучение");
    };
    if !app.engine.mic.online() {
        return bad("Микрофон колонки недоступен");
    }
    let mic = app.engine.mic.clone();
    let engine = app.engine.clone();
    let result = tokio::task::spawn_blocking(move || {
        let _guard = guard;
        let _recording = engine.recording_word();
        let (id, rx) = mic.subscribe(32);
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(6);
        let mut samples = Vec::with_capacity(SAMPLES * 2);
        let result = loop {
            if engine.talking() || engine.player.playing() || engine.music.state().playing {
                break Err("Остановите разговор или музыку и запишите ещё раз");
            }
            let Some(left) = deadline.checked_duration_since(std::time::Instant::now()) else {
                break Err("Запись не завершилась");
            };
            let Ok(frame) = rx.recv_timeout(left) else {
                break Err("Микрофон не передал звук");
            };
            let n = frame.len().min(SAMPLES - samples.len() / 2);
            samples.extend(frame[..n].iter().flat_map(|s| s.to_le_bytes()));
            if samples.len() == SAMPLES * 2 {
                break Ok(samples);
            }
        };
        mic.unsubscribe(id);
        result
    })
    .await;
    match result {
        Ok(Ok(samples)) => Response::builder()
            .header("Content-Type", "application/octet-stream")
            .header("Cache-Control", "no-store")
            .body(axum::body::Body::from(samples))
            .unwrap(),
        Ok(Err(error)) => bad(error),
        Err(_) => bad("Запись не завершилась"),
    }
}

#[cfg(feature = "wake-training")]
pub async fn train(
    State(app): State<App>,
    Query(input): Query<NamedModel>,
    body: Bytes,
) -> Response {
    let Ok(guard) = app.wake_update.clone().try_lock_owned() else {
        return bad("Идёт другая запись или обучение");
    };
    let phrase = match wake_profiles::phrase(&input.phrase) {
        Ok(value) => value,
        Err(error) => return bad(error),
    };
    let data = app.data_dir.clone();
    let store = app.store.clone();
    let result = tokio::task::spawn_blocking(move || {
        let _guard = guard;
        let features = wake_profiles::features(&data)?;
        let head = jarvis_wake_train::train(&features, &body)?;
        wake_profiles::save(&data, &phrase, &head, &store)
    })
    .await;
    respond_profile(result)
}

#[cfg(feature = "wake-training")]
pub async fn test(State(app): State<App>, Path(id): Path<String>, body: Bytes) -> Response {
    if body.len() != jarvis_wake_train::SAMPLES * 2 {
        return bad("Нужна одна запись фразы");
    }
    let Ok(guard) = app.wake_update.clone().try_lock_owned() else {
        return bad("Идёт другая запись или обучение");
    };
    let data = app.data_dir.clone();
    let store = app.store.clone();
    let result = tokio::task::spawn_blocking(move || -> Result<(bool, f32), String> {
        let _guard = guard;
        let (_, bytes) = wake_profiles::get(&data, &id)?;
        let mut detector = wake_profiles::model(&data, &bytes, &store)?;
        let mut signal = vec![0; 32000];
        signal.extend(
            body.chunks_exact(2)
                .map(|b| i16::from_le_bytes([b[0], b[1]])),
        );
        signal.resize(signal.len() + 6400, 0);
        let mut hit = false;
        let mut peak = 0.0f32;
        for frame in signal.chunks(160) {
            hit |= detector.feed(frame);
            peak = peak.max(detector.last_prob());
        }
        Ok((hit, peak))
    })
    .await;
    match result {
        Ok(Ok((hit, peak))) => reply(StatusCode::OK, json!({"detected":hit,"peak":peak})),
        Ok(Err(error)) => bad(error),
        Err(_) => bad("Не удалось проверить фразу"),
    }
}
