//! jarvis — голосовой ассистент «Джарвис» целиком на Яндекс Станции: без своего сервера.
//! Ядро на Rust; модули (голосовые движки, YouTube, дом…) вшиваются при сборке, лёгкие плагины — на Lua.
#![recursion_limit = "256"]

mod hardware;
mod jlog;
mod settings;
mod web;

use std::path::PathBuf;

pub const DEFAULT_REPO: &str = match option_env!("JARVIS_REPO") {
    Some(r) => r,
    None => "newfpv/YaJavis",
};

pub fn version() -> &'static str {
    option_env!("JARVIS_VERSION").unwrap_or("dev")
}

/// Флаги как у Go-версии: -data, -mic, -listen (jarvisd.sh запускает с ними).
struct Flags {
    data: PathBuf,
    mic: PathBuf,
    listen: String,
}

fn flags() -> Flags {
    let mut f = Flags { data: "/data/opt/jarvisd".into(), mic: "/tmp/jarvisd/mic.sock".into(), listen: ":8080".into() };
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        let a = a.trim_start_matches('-');
        let (k, v) = match a.split_once('=') {
            Some((k, v)) => (k.to_string(), Some(v.to_string())),
            None => (a.to_string(), None),
        };
        let mut val = || v.clone().or_else(|| args.next()).unwrap_or_default();
        match k.as_str() {
            "data" => f.data = val().into(),
            "mic" => f.mic = val().into(),
            "listen" => f.listen = val(),
            _ => {}
        }
    }
    f
}

fn main() {
    if std::env::args().nth(1).as_deref() == Some("version") {
        println!("{}", version());
        return;
    }
    jlog::init();
    let f = flags();
    log::info!("jarvis {} (ядро на Rust)", version());
    let store = match settings::Store::open(f.data.join("settings.json")) {
        Ok(s) => s,
        Err(e) => {
            log::info!("настройки: {e}");
            std::process::exit(1);
        }
    };
    jlog::set_timezone(&store.with(|s| s.timezone.clone()));
    let _ = &f.mic;
    let rt = tokio::runtime::Builder::new_multi_thread().worker_threads(2).enable_all().build().expect("tokio");
    let app = web::Web::new(store, f.data.clone());
    if let Err(e) = rt.block_on(web::serve(app, &f.listen)) {
        log::info!("веб-настройки: {e}");
        std::process::exit(1);
    }
}
