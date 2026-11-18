//! jarvis — голосовой ассистент «Джарвис» целиком на Яндекс Станции: без своего сервера.
//! Ядро на Rust; модули (голосовые движки, YouTube, дом…) вшиваются при сборке, лёгкие плагины — на Lua.
#![recursion_limit = "256"]

mod alice;
mod alice_music;
mod launchpatch;
mod audio;
mod chatgpt;
mod buttons;
mod speakervol;
mod cmdspot;
mod memory;
mod music;
mod engine;
mod fastcmd;
mod hardware;
mod jlog;
mod journal;
mod led;
mod llm;
#[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
mod cloud_voice;
#[cfg(any(feature="openai",feature="gemini",feature="grok",feature="elevenlabs"))]
mod classic_voice;
mod net;
mod plugins;
mod rules;
mod session;
mod settings;
mod tools;
mod telemetry;
mod vpn;
mod wake;
mod wake_profiles;
#[cfg(feature = "wake-training")]
mod training_records;
#[cfg(feature = "wake-training")]
mod training_background;
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
    if std::env::args().nth(1).as_deref() == Some("patch-launch") {
        let args: Vec<_> = std::env::args().skip(2).collect();
        let result = if args.len() == 4 {
            launchpatch::patch(&args[0], &args[1], &args[2], &args[3])
        } else {
            Err("usage: jarvis patch-launch <launch.json> <out.json> <vqe_tap.so> <mic.sock>".into())
        };
        if let Err(error) = result { eprintln!("{error}"); std::process::exit(1); }
        return;
    }
    if std::env::args().nth(1).as_deref() == Some("modules") {
        println!("{}", serde_json::to_string(&plugins::compiled_modules()).expect("список модулей"));
        return;
    }
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
    // проверка разговора без живого микрофона: jarvis mic-send сокет файл.wav — звук в сокет в реальном времени
    #[cfg(unix)]
    if std::env::args().nth(1).as_deref() == Some("mic-send") {
        let a: Vec<String> = std::env::args().skip(2).collect();
        if let Err(e) = audio::send_wav(std::path::Path::new(&a[0]), std::path::Path::new(&a[1])) {
            println!("{e}");
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
    let music = Arc::new(music::Music::default());
    let host_cell: Arc<std::sync::OnceLock<std::sync::Weak<plugins::Host>>> = Arc::default();
    let engine_cell: Arc<std::sync::OnceLock<std::sync::Weak<engine::Engine>>> = Arc::default();
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
            network: {let proxy=proxy.clone();Box::new(move ||proxy.network_reply("plugins"))},
            request: Box::new(move |id, method, params| {
                let svc = svc.clone();
                Box::pin(async move {
                    if id == "chatgpt" && method == "voice.brain" {
                        if !(svc.official)(&id) {
                            return Err("official plugin required".into());
                        }
                        return chatgpt::brain_request(params).await;
                    }
                    tokio::task::spawn_blocking(move || svc.call(&id, &method, &params)).await.unwrap_or_else(|e| Err(e.to_string()))
                })
            }),
            alice: Box::new(|text, silent| Box::pin(async move { tokio::task::spawn_blocking(move || alice::text(&text, silent).is_ok()).await.unwrap_or(false) })),
            say: {
                let cell = engine_cell.clone();
                Box::new(move |text| {
                    // плагин просит сказать вслух (напоминание, ответ на событие): голосом ассистента
                    if let Some(e) = cell.get().and_then(std::sync::Weak::upgrade) {
                        std::thread::spawn(move || e.say_text(&text));
                    }
                })
            },
            info: Box::new(move || info(&st)),
            event: {
                let music = music.clone();
                let engine = engine_cell.clone();
                Box::new(move |manifest, name, data| {
                    if manifest.id=="chatgpt" && manifest.kind=="module" && name=="voice.event"{chatgpt::module_voice_event(data.clone());}
                    if manifest.provides("youtube") {
                        let was_playing = music.state().playing;
                        let next = (name == "music.related_end").then(|| data.as_str().map(str::to_owned)).flatten();
                        music.event(name, data);
                        if let Some(e) = engine.get().and_then(std::sync::Weak::upgrade) {
                            if let Some(id) = next {
                                tokio::spawn(e.offer_next(id));
                            } else if !was_playing && music.state().playing && music.alice.lock().unwrap().playing() {
                                let music = music.clone();
                                tokio::spawn(async move { let _ = music.alice_control("pause", &Value::Null).await; });
                            }
                        }
                    }
                })
            },
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
            while std::env::var_os("JARVIS_NO_AUTOINSTALL").is_none() {
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
    #[cfg(unix)]
    alice::watch_mic(mic.clone());
    let (listener, det) = wake::Listener::new(&f.data, &store, host.wake_training());
    let player = {
        let st = store.clone();
        audio::Player::new(Box::new(move || st.with(|s| s.voice_volume())), Box::new(|| speakervol::voice_trim(speakervol::speaker_level())))
    };
    let leds = {
        let st = store.clone();
        led::Leds::new(Box::new(move || st.with(|s| s.led_for())))
    };
    tokio::spawn(chatgpt::warm_loop(host.clone(), store.clone()));
    let journal = Arc::new(journal::Journal::default());
    let memory = {
        let (a, b) = (store.clone(), store.clone());
        memory::Memory::load(f.data.join("chatgpt-chat.json"), Box::new(move || !a.with(|s| s.memory.off)), Box::new(move || b.with(|s| s.now())))
    };
    {
        let m = memory.clone();
        *journal.on_remember.lock().unwrap() = Some(Box::new(move |t: journal::Turn| {
            if !t.model.is_empty() {
                m.add(&t.text, &t.answer); // разговор с моделью — в общую память, какой бы ассистент ни отвечал
            } else if !t.answer.is_empty() && memory::worthy(&t.answer) {
                m.add(&t.text, &format!("[{}]", t.answer)); // выполнено без модели — модель тоже должна знать
            }
        }));
    }
    let mut tools = tools::Tools::new(host.clone(), store.clone());
    tools.memory = Some(memory.clone());
    for (target, queue) in [(&mut tools.music, false), (&mut tools.music_queue, true)] {
        let (music, host) = (music.clone(), host.clone());
        *target = Some(Arc::new(move |q| {
            let (music, host) = (music.clone(), host.clone());
            Box::pin(async move { music.play(&host, &q, queue).await })
        }));
    }
    tools.music_ctl = Some({
        let (music, host) = (music.clone(), host.clone());
        Arc::new(move |text| {
            let (music, host) = (music.clone(), host.clone());
            Box::pin(async move { music.control(&host, &text).await.unwrap_or_else(|| "Сейчас музыка не играет или команда не распознана.".into()) })
        })
    });
    tools.music_fast=Some({let (music,host)=(music.clone(),host.clone());Arc::new(move|text|{let (music,host)=(music.clone(),host.clone());Box::pin(async move{music.control(&host,&text).await})})});
    let tools = Arc::new(tools);
    let engine = engine::Engine::new(
        tokio::runtime::Handle::current(),
        store.clone(),
        host.clone(),
        mic,
        player,
        leds,
        journal,
        tools,
        listener,
        f.data.clone(),
        music.clone(),
    );
    #[cfg(unix)]
    speakervol::watch_night(store.clone(), engine.leds.clone());
    let _ = engine_cell.set(Arc::downgrade(&engine));
    {
        let e = Arc::downgrade(&engine);
        music::volume_loop(music, host.clone(), move || e.upgrade().is_some_and(|e| e.talking() || e.player.playing()));
    }
    memory.set_summarizer(Some(engine.summarizer()));
    engine.run(det);
    let app = web::Web::new(store.clone(), host.clone(), proxy, f.data.clone(), engine);
    #[cfg(unix)]
    {
        let app = app.clone();
        buttons::watch(move || web::buttons_reset(&app));
    }
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
