use super::{App, bad, reply};
use axum::{Json, extract::State, http::StatusCode, response::Response};
use serde_json::{Value, json};

pub async fn get(State(app): State<App>) -> Json<Value> {
    Json(app.engine.music.snapshot(&app.plugins).await)
}

pub async fn post(State(app): State<App>, Json(v): Json<Value>) -> Response {
    let action = v["action"].as_str().unwrap_or("");
    let query = v["query"].as_str().unwrap_or("").trim();
    let (op, args) = match action {
        "play" | "queue" if query.is_empty() => return bad("Что включить?"),
        "play" => ("play", json!({"query":query})),
        "id" if !query.is_empty() => ("play_id", json!({"id":query})),
        "queue" => ("enqueue", json!({"query":query})),
        "pause" | "resume" | "next" | "stop" => (action, Value::Null),
        "seek" => match v["sec"].as_f64().filter(|s| s.is_finite() && *s >= 0.0) {
            Some(sec) => ("seek", json!({"delta":sec,"abs":true})),
            None => return bad("Неверная позиция"),
        },
        "unqueue" => match v["index"].as_u64() {
            Some(i) => ("unqueue", json!({"index":i})),
            None => return bad("Неверный номер в очереди"),
        },
        "radio" => {
            let Some(on) = v["on"].as_bool() else {
                return bad("Нужно указать on");
            };
            if let Err(e) = app.store.update(|s| -> Result<(), std::io::Error> {
                s.radio_off = !on;
                Ok(())
            }) {
                return bad(e.to_string());
            }
            return reply(
                StatusCode::OK,
                app.engine.music.snapshot(&app.plugins).await,
            );
        }
        _ => return bad("Неизвестная команда"),
    };
    match app.engine.music.call(&app.plugins, op, args).await {
        Ok(_) => reply(
            StatusCode::OK,
            app.engine.music.snapshot(&app.plugins).await,
        ),
        Err(e) => bad(e),
    }
}
