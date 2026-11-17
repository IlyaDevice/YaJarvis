//! Сброс пароля веб-настроек кнопками колонки — если пароль забыли.
//! Последовательность: «громче», «тише» поочерёдно 8 раз подряд (+ − + − + − + −), между нажатиями
//! не дольше 1,5 с. Случайно так не нажать, а громкость в итоге остаётся прежней.
//! Кнопки читаем из /dev/input без захвата — Алиса их тоже видит, как обычно.

use std::{
    io::Read,
    sync::mpsc::{SyncSender, sync_channel},
    time::{Duration, Instant},
};

const KEY_VOLUME_DOWN: u16 = 114;
const KEY_VOLUME_UP: u16 = 115;
const RESET_PRESSES: usize = 8;

/// Распознавание последовательности сброса по нажатиям.
#[derive(Default)]
pub struct Seq {
    keys: Vec<u16>,
    last: Option<Instant>,
}

impl Seq {
    /// Нажатие в момент now; true — набрана последовательность сброса.
    pub fn press(&mut self, k: u16, now: Instant) -> bool {
        if k != KEY_VOLUME_UP && k != KEY_VOLUME_DOWN {
            self.keys.clear();
            return false;
        }
        if self.last.is_none_or(|t| now.duration_since(t) > Duration::from_millis(1500)) || self.keys.last() == Some(&k) {
            self.keys.clear();
        }
        self.last = Some(now);
        if self.keys.is_empty() && k != KEY_VOLUME_UP {
            return false; // начинается с «громче»
        }
        self.keys.push(k);
        if self.keys.len() >= RESET_PRESSES {
            self.keys.clear();
            return true;
        }
        false
    }
}

/// Следить за кнопками всех устройств ввода в своём потоке; on_reset — набрана последовательность сброса.
pub fn watch(on_reset: impl Fn() + Send + 'static) {
    let (tx, rx) = sync_channel::<u16>(16);
    let devs: Vec<_> = std::fs::read_dir("/dev/input")
        .map(|d| d.flatten().map(|e| e.path()).filter(|p| p.file_name().is_some_and(|n| n.to_string_lossy().starts_with("event"))).collect())
        .unwrap_or_default();
    if devs.is_empty() {
        return;
    }
    for d in devs {
        let tx = tx.clone();
        std::thread::spawn(move || read_keys(&d, tx));
    }
    std::thread::spawn(move || {
        let mut seq = Seq::default();
        for k in rx {
            if seq.press(k, Instant::now()) {
                on_reset();
            }
        }
    });
}

/// Нажатия (EV_KEY, value=1) из одного /dev/input/eventN.
fn read_keys(path: &std::path::Path, out: SyncSender<u16>) {
    let Ok(mut f) = std::fs::File::open(path) else { return };
    // struct input_event: timeval (2×long) + type u16 + code u16 + value s32
    let size = 2 * std::mem::size_of::<std::ffi::c_long>() + 8;
    let mut buf = vec![0u8; size * 16];
    loop {
        let n = match f.read(&mut buf) {
            Ok(0) => return,
            Ok(n) => n,
            Err(e) => {
                log::info!("кнопки: {}: {e}", path.display());
                return;
            }
        };
        for ev in buf[..n - n % size].chunks_exact(size) {
            let ev = &ev[size - 8..];
            let (typ, code) = (u16::from_le_bytes([ev[0], ev[1]]), u16::from_le_bytes([ev[2], ev[3]]));
            let val = i32::from_le_bytes([ev[4], ev[5], ev[6], ev[7]]);
            if typ == 1 && val == 1 {
                let _ = out.try_send(code);
            }
        }
    }
}

/// Сигнал «пароль сброшен»: три восходящие ноты дважды, не похоже на короткие сигналы быстрых команд.
pub fn reset_chime() -> Vec<i16> {
    let rate = 16000.0;
    let mut out = vec![];
    for _ in 0..2 {
        for f in [660.0, 880.0, 1320.0] {
            let n = (0.11 * rate) as usize;
            for i in 0..n {
                let env = (i.min(n - i) as f64 / (0.008 * rate)).min(1.0);
                out.push((9000.0 * env * (2.0 * std::f64::consts::PI * f * i as f64 / rate).sin()) as i16);
            }
            out.extend(std::iter::repeat_n(0, 16000 / 40));
        }
        out.extend(std::iter::repeat_n(0, 16000 / 6));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sequence() {
        let t0 = Instant::now();
        let at = |ms: u64| t0 + Duration::from_millis(ms);
        let mut s = Seq::default();
        let mut hit = false;
        for i in 0..8 {
            hit = s.press(if i % 2 == 0 { KEY_VOLUME_UP } else { KEY_VOLUME_DOWN }, at(i * 500));
        }
        assert!(hit);
        // пауза больше 1,5 с — всё сначала
        let mut s = Seq::default();
        let mut n = 0;
        for i in 0..8u64 {
            n += usize::from(s.press(if i % 2 == 0 { KEY_VOLUME_UP } else { KEY_VOLUME_DOWN }, at(i * 500 + if i >= 4 { 2000 } else { 0 })));
        }
        assert_eq!(n, 0);
        // два «громче» подряд — сначала
        let mut s = Seq::default();
        assert!(!(0..8).any(|i| s.press(KEY_VOLUME_UP, at(i * 300))));
    }
}
