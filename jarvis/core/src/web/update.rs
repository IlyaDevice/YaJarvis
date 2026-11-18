use super::{App, bad, ok, reply};
use axum::{Json, body::Bytes, extract::State, http::StatusCode, response::Response};
use serde::Deserialize;

pub async fn get(State(app): State<App>) -> Response {
    reply(StatusCode::OK, app.updater.snapshot())
}

#[derive(Deserialize)]
pub struct Settings {
    repo: Option<String>,
    check: Option<bool>,
    auto: Option<bool>,
    stats: Option<bool>,
}

pub async fn put(State(app): State<App>, Json(input): Json<Settings>) -> Response {
    let repo = input.repo.as_deref().map(str::trim);
    if repo.is_some_and(|r| !r.is_empty() && !crate::plugins::catalog::valid_repo(r)) {
        return bad("Репозиторий должен быть в формате владелец/имя");
    }
    match app.store.update(|settings| -> std::io::Result<()> {
        if let Some(repo) = repo {
            settings.update_repo = repo.into();
        }
        if let Some(check) = input.check {
            settings.update_check = check;
        }
        if let Some(auto) = input.auto {
            settings.update_auto = auto;
        }
        if let Some(stats) = input.stats {
            settings.no_stats = !stats;
        }
        Ok(())
    }) {
        Ok(()) => ok(),
        Err(error) => bad(error.to_string()),
    }
}

pub async fn check(State(app): State<App>) -> Response {
    match app.updater.check().await {
        Ok(()) => get(State(app)).await,
        Err(error) => bad(error),
    }
}

pub async fn install(State(app): State<App>) -> Response {
    if app.engine.talking() || app.engine.music.playing() {
        return bad("Остановите разговор и музыку перед обновлением");
    }
    match app.updater.install_build(None).await {
        Ok(()) => ok(),
        Err(error) => bad(error),
    }
}

pub async fn upload(State(app): State<App>, data: Bytes) -> Response {
    if app.engine.talking() || app.engine.music.playing() {
        return bad("Остановите разговор и музыку перед обновлением");
    }
    match app.updater.upload(data).await {
        Ok(()) => ok(),
        Err(error) => bad(error),
    }
}

pub fn automatic(app: App) {
    if std::env::var_os("JARVIS_READY_PATH").is_none()
        || std::env::var_os("JARVIS_NO_AUTOUPDATE").is_some()
    {
        return;
    }
    tokio::spawn(async move {
        use std::time::{Duration, Instant};
        tokio::time::sleep(Duration::from_secs(120)).await;
        let mut last_check = None;
        loop {
            let (enabled, auto, hour) = app
                .store
                .with(|s| (s.update_check, s.update_auto, s.now().hour()));
            if enabled {
                if last_check.is_none_or(|t: Instant| t.elapsed() >= Duration::from_secs(6 * 3600))
                {
                    last_check = Some(Instant::now());
                    let _ = app.updater.check().await;
                }
                if auto
                    && (3..6).contains(&hour)
                    && app.updater.available()
                    && !app.engine.talking()
                    && !app.engine.music.playing()
                {
                    let _ = app.updater.install_build(None).await;
                }
            }
            tokio::time::sleep(Duration::from_secs(20 * 60)).await;
        }
    });
}
