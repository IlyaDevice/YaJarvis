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

pub async fn journal(State(app): State<App>) -> Response {
    let e = &app.engine;
    let talking = e.talking();
    let phase = if talking { e.leds.phase().name() } else { "" };
    reply(
        StatusCode::OK,
        json!({"items": e.journal.items(50), "mic": e.mic.online(), "mic_why": "", "talking": talking, "phase": phase,
               "sys": {}, "volume": 0, "version": crate::version()}),
    )
}
