//! Слово активации «Джарвис» прямо на колонке. Модели — файлы в папке настроек:
//! openWakeWord (oww.bin + wake_oww.bin), если не выключена файлом disable-oww, иначе wake.bin (KWS).
//! Нет модели — Джарвиса можно позвать кнопкой «Поговорить» в панели.

use crate::settings::Store;
use jarvis_dsp::{Wake, kws::Kws, oww::Oww};
use std::path::Path;

struct NoWake;

impl Wake for NoWake {
    fn feed(&mut self, _: &[i16]) -> bool {
        false
    }
    fn reset(&mut self) {}
    fn last_prob(&self) -> f32 {
        0.0
    }
}

pub struct Loaded {
    pub det: Box<dyn Wake>,
    pub ready: bool,
}

pub fn load(data: &Path, store: &Store) -> Loaded {
    let sens = |store: &Store| {
        let s = store.clone();
        Box::new(move || s.with(|s| s.wake_sensitivity)) as Box<dyn Fn() -> i64 + Send>
    };
    if !data.join("disable-oww").exists() {
        if let (Ok(feat), Ok(head)) = (std::fs::read(data.join("oww.bin")), std::fs::read(data.join("wake_oww.bin"))) {
            match Oww::load(&feat, &head, sens(store)) {
                Ok(m) => {
                    log::info!("слово активации: модель openWakeWord загружена ({} КБ)", (feat.len() + head.len()) / 1024);
                    return Loaded { det: Box::new(m), ready: true };
                }
                Err(e) => log::info!("слово активации: openWakeWord: {e} — беру прежнюю модель"),
            }
        }
    }
    let path = data.join("wake.bin");
    let Ok(bin) = std::fs::read(&path) else {
        log::info!("слово активации: модели {} нет — только кнопка в настройках", path.display());
        return Loaded { det: Box::new(NoWake), ready: false };
    };
    match Kws::load(&bin, sens(store)) {
        Ok(m) => {
            log::info!("слово активации: модель загружена ({} КБ)", bin.len() / 1024);
            Loaded { det: Box::new(m), ready: true }
        }
        Err(e) => {
            log::info!("слово активации: {e}");
            Loaded { det: Box::new(NoWake), ready: false }
        }
    }
}

/// Проверка на колонке без микрофона: прогнать WAV (16 кГц моно) через модель и замерить скорость.
pub fn check_file(data: &Path, wav: &Path, store: &Store) -> Result<(), String> {
    let raw = std::fs::read(wav).map_err(|e| e.to_string())?;
    let pcm: Vec<i16> = if raw.len() > 44 && &raw[..4] == b"RIFF" {
        raw[44..].chunks_exact(2).map(|c| i16::from_le_bytes([c[0], c[1]])).collect()
    } else {
        raw.chunks_exact(2).map(|c| i16::from_le_bytes([c[0], c[1]])).collect()
    };
    let mut w = load(data, store);
    let t0 = std::time::Instant::now();
    let mut hits = vec![];
    let mut peak = 0f32;
    for (i, chunk) in pcm.chunks(160).enumerate() {
        if w.det.feed(chunk) {
            hits.push(format!("{:.2} с ({:.2})", i as f64 * 0.01, w.det.wake_prob()));
        }
        peak = peak.max(w.det.last_prob());
    }
    let el = t0.elapsed();
    let secs = pcm.len() as f64 / 16000.0;
    println!(
        "{:.1} с звука за {:?} ({:.1}% реального времени), пик {:.3}, срабатываний {}: {}",
        secs,
        el,
        100.0 * el.as_secs_f64() / secs,
        peak,
        hits.len(),
        hits.join(", ")
    );
    Ok(())
}

/// Состояние слова для панели; сам детектор крутится в цикле движка разговора.
pub struct Listener {
    pub ready: std::sync::atomic::AtomicBool,
    pub last_prob: std::sync::atomic::AtomicU32,
}

impl Listener {
    pub fn new(data: &Path, store: &Store) -> (std::sync::Arc<Listener>, Box<dyn Wake>) {
        let w = load(data, store);
        let me = std::sync::Arc::new(Listener { ready: w.ready.into(), last_prob: 0.into() });
        (me, w.det)
    }
}
