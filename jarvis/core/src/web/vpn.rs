use super::{App, bad, ok};
use axum::{Json, extract::State, response::Response};
use serde_json::Value;

pub async fn put(State(app): State<App>, Json(input): Json<Value>) -> Response {
    let Some(link) = input.get("link").and_then(Value::as_str) else {
        return bad("Нужен параметр link");
    };
    match app.proxy.set_link(&app.plugins, link).await {
        Ok(()) => ok(),
        Err(e) => bad(e),
    }
}
