//! jarvis — голосовой ассистент «Джарвис» целиком на Яндекс Станции: без своего сервера.
//! Ядро на Rust; модули (голосовые движки, YouTube, дом…) вшиваются при сборке, лёгкие плагины — на Lua.
#![recursion_limit = "256"]

mod audio;
mod hardware;
mod jlog;
mod net;
mod plugins;
mod settings;
mod vpn;
mod wake;
mod web;

use serde_json::{Value, json};
use std::{path::PathBuf, sync::Arc};

pub const DEFAULT_REPO: &str = match option_env!("JARVIS_REPO") {
    Some(r) => r,
    None => "newfpv/YaJavis",
};

pub fn version() -> &'static str {
    option_env!("JARVIS_VERSION").unwrap_or("dev")
}

/// Флаги как у Go-версии: -data, -mic, -listen (jarvisd.sh запускает с ними).
/// -modules — старый jarvisd с вшитыми модулями Go: на переходный период запускает chatgpt, home, vpn, youtube.
struct Flags {
    data: PathBuf,
    mic: PathBuf,
    listen: String,
    modules: Option<PathBuf>,
}

fn flags() -> Flags {
    let mut f = Flags { data: "/data/opt/jarvisd".into(), mic: "/tmp/jarvisd/mic.sock".into(), listen: ":8080".into(), modules: None };
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
            "modules" => f.modules = Some(val().into()),
            _ => {}
        }
    }
    if f.modules.is_none() {
        f.modules = Some(f.data.join("jarvisd-modules"));
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
    // проверка слова на колонке без микрофона: jarvis wake-check -data /data/opt/jarvisd файл.wav…
    if std::env::args().nth(1).as_deref() == Some("wake-check") {
        let store = settings::Store::open(f.data.join("settings.json")).expect("настройки");
        for a in std::env::args().skip(2).filter(|a| a.ends_with(".wav") || a.ends_with(".raw")) {
            print!("{a}: ");
            if let Err(e) = wake::check_file(&f.data, std::path::Path::new(&a), &store) {
                println!("{e}");
            }
        }
        return;
    }
    log::info!("jarvis {} (ядро на Rust)", version());
    let upgrade_from = plugins::catalog::detect_upgrade(&f.data); // до Store::open: он создаёт settings.json
    let store = match settings::Store::open(f.data.join("settings.json")) {
        Ok(s) => s,
        Err(e) => {
            log::info!("настройки: {e}");
            std::process::exit(1);
        }
    };
    jlog::set_timezone(&store.with(|s| s.timezone.clone()));
    let rt = tokio::runtime::Builder::new_multi_thread().worker_threads(2).enable_all().build().expect("tokio");
    rt.block_on(run(f, store, upgrade_from));
}

/// Город, комната, язык, модель колонки — плагинам при запуске.
fn info(store: &settings::Store) -> Value {
    store.with(|s| {
        let p = s.active_profile();
        json!({"city": s.city, "timezone": s.timezone, "room": s.speaker_room, "language": s.language,
               "model": hardware::station_model().id, "assistant": p.name})
    })
}

async fn run(f: Flags, store: settings::Store, upgrade_from: String) {
    let proxy = vpn::Proxy::new(store.clone());
    let host_cell: Arc<std::sync::OnceLock<std::sync::Weak<plugins::Host>>> = Arc::default();
    let svc = {
        let (store, proxy, data, cell) = (store.clone(), proxy.clone(), f.data.clone(), host_cell.clone());
        let st2 = store.clone();
        Arc::new(plugins::coresvc::CoreServices {
            store,
            proxy,
            data_dir: data,
            info: Box::new(move || info(&st2)),
            official: Box::new(move |id| cell.get().and_then(|w| w.upgrade()).is_some_and(|h| h.official(id))),
        })
    };
    let hooks = {
        let st = store.clone();
        let svc = svc.clone();
        plugins::Hooks {
            request: Box::new(move |id, method, params| {
                let svc = svc.clone();
                Box::pin(async move { tokio::task::spawn_blocking(move || svc.call(&id, &method, &params)).await.unwrap_or_else(|e| Err(e.to_string())) })
            }),
            info: Box::new(move || info(&st)),
            ..Default::default()
        }
    };
    let host = plugins::Host::new(f.data.clone(), hooks, f.modules.clone());
    let _ = host_cell.set(Arc::downgrade(&host));
    {
        let (host, proxy) = (host.clone(), proxy.clone());
        tokio::spawn(async move {
            host.load().await;
            tokio::spawn(proxy.run(host.clone()));
            // нужные этой версии официальные плагины; нет интернета — пробуем снова
            let mut wait = 60;
            loop {
                let settings = serde_json::to_value(host_settings(&host)).ok().and_then(|v| v.as_object().cloned()).unwrap_or_default();
                match host.ensure_for(&format!("v{}", version()), false, &settings, &upgrade_from).await {
                    Ok(_) => {
                        plugins::catalog::mark_core_version(&host.data_dir);
                        break;
                    }
                    Err(e) => log::info!("плагины: {e} — попробую через {wait} с"),
                }
                tokio::time::sleep(std::time::Duration::from_secs(wait)).await;
                wait = (wait * 2).min(3600);
            }
        });
    }
    {
        let host = host.clone();
        tokio::spawn(async move {
            let _ = tokio::signal::ctrl_c().await;
            host.stop_all().await;
            std::process::exit(0);
        });
    }
    #[cfg(unix)]
    {
        let host = host.clone();
        tokio::spawn(async move {
            if let Ok(mut s) = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
                s.recv().await;
                log::info!("завершаюсь: останавливаю плагины");
                host.stop_all().await;
                std::process::exit(0);
            }
        });
    }
    // микрофон и слово «Джарвис»
    let mic = {
        let st = store.clone();
        audio::Mic::new(f.mic.clone(), Box::new(move || st.with(|s| s.mic_gain_db)))
    };
    if cfg!(unix) {
        mic.clone().run();
    }
    let (wake_tx, mut wake_rx) = tokio::sync::mpsc::channel::<f32>(1);
    let listener = wake::Listener::start(&f.data, &store, mic.clone(), wake_tx);
    tokio::spawn(async move {
        while let Some(_p) = wake_rx.recv().await {
            // движок разговора переносится следующим шагом: пока оклик только в журнале
        }
    });
    let app = web::Web::new(store.clone(), host.clone(), proxy, f.data.clone(), mic, listener);
    if let Err(e) = web::serve(app, &f.listen).await {
        log::info!("веб-настройки: {e}");
        host.stop_all().await;
        std::process::exit(1);
    }
}

/// Настройки колонки картой — для правил автоустановки плагинов.
fn host_settings(host: &plugins::Host) -> Value {
    std::fs::read(host.data_dir.join("settings.json")).ok().and_then(|b| serde_json::from_slice(&b).ok()).unwrap_or(Value::Null)
}
