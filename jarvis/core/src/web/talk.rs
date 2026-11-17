//! Разговор из панели: «Поговорить», «Стоп», переписка последних реплик.

use super::{App, ok, reply};
use axum::{extract::State, http::StatusCode, response::Response};
use serde_json::json;

pub async fn talk(State(app): State<App>) -> Response {
    app.engine.trigger("кнопка в настройках", 1.0);
    ok()
}

pub async fn stop(State(app): State<App>) -> Response {
    let e = app.engine.clone();
    let _ = tokio::task::spawn_blocking(move || e.stop("кнопка в настройках")).await;
    ok()
}

/// Громкость колонки 0..10 из панели.
pub async fn volume(body: axum::body::Bytes) -> Response {
    let Some(lvl) = serde_json::from_slice::<serde_json::Value>(&body).ok().and_then(|v| v.get("level").or(v.get("Level")).and_then(|x| x.as_i64())) else {
        return super::bad("пустой запрос");
    };
    let lvl = lvl.clamp(0, 10);
    match tokio::task::spawn_blocking(move || crate::speakervol::set_alice_volume(lvl)).await.unwrap_or_else(|e| Err(e.to_string())) {
        Ok(()) => reply(StatusCode::OK, json!({"volume": lvl})),
        Err(e) => super::bad(e),
    }
}

pub async fn journal(State(app): State<App>) -> Response {
    let vol = tokio::task::spawn_blocking(super::vol).await.unwrap_or(-1);
    let e = &app.engine;
    let talking = e.talking();
    let phase = if talking { e.leds.phase().name() } else { "" };
    reply(
        StatusCode::OK,
        json!({"items": e.journal.items(50), "mic": e.mic.online(), "mic_why": "", "talking": talking, "phase": phase,
               "sys": {}, "volume": vol, "version": crate::version()}),
    )
}
