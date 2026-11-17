//! Подсветка через /sys/class/leds: светодиоды с цветом в имени («micro:red:ring1») группируются по цвету.
//! Перед своим режимом запоминаем, что горело у Алисы, и потом возвращаем как было.
//!
//! Кольцо живое: «слушаю» — белый свет плавно загорается и дышит; «думаю» — бирюзовый огонёк бежит
//! по кругу; «говорю» — фиолетовый свет вспыхивает в такт голосу.

use crate::settings::LedSettings;
use std::{
    collections::{BTreeMap, HashMap},
    f64::consts::PI,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug, Default)]
pub enum Mode {
    #[default]
    Off,
    Listening,
    Thinking,
    Speaking,
}

impl Mode {
    pub fn name(self) -> &'static str {
        match self {
            Mode::Off => "",
            Mode::Listening => "listening",
            Mode::Thinking => "thinking",
            Mode::Speaking => "speaking",
        }
    }
}

struct Chan {
    path: PathBuf,
    color: String, // red | green | blue | white; у часов — имя сегмента
    max: i64,
    group: usize,
    last: i64,
    trig: String, // штатный триггер ядра (Лайт 2: led_ring_fb), пока кольцом управляет Джарвис — «none»
}

type Palette = HashMap<Mode, HashMap<&'static str, f64>>;

struct St {
    palette: Palette,
    off: bool,
    preview: u64,
    chans: Vec<Chan>,
    clock: Vec<Chan>, // сегменты LED-часов (Мини 3): у них своя анимация
    saved: HashMap<PathBuf, i64>,
    mode: Mode,
    until: Option<Instant>,
    groups: usize,
    since: Instant,
    level: f64,
    target: f64,
    hear: f64,
    hear_t: f64,
    frames: u64,
    lvl_at: Instant,
    phase: Mode,
}

pub struct Leds {
    st: Mutex<St>,
    /// Подсветка активного ассистента — читается, когда кольцо загорается.
    pub source: Box<dyn Fn() -> LedSettings + Send + Sync>,
}

fn default_palette() -> Palette {
    let m = |r, g, b, w| HashMap::from([("red", r), ("green", g), ("blue", b), ("white", w)]);
    HashMap::from([
        (Mode::Listening, m(0.43, 0.43, 0.43, 0.43)),
        (Mode::Thinking, m(0.0, 0.43, 0.6, 0.2)),
        (Mode::Speaking, m(0.2, 0.1, 0.55, 0.3)),
    ])
}

fn parse_hex(s: &str) -> (f64, f64, f64) {
    let s = s.trim().trim_start_matches('#');
    match u32::from_str_radix(s, 16) {
        Ok(v) if s.len() == 6 => ((v >> 16 & 255) as f64 / 255.0, (v >> 8 & 255) as f64 / 255.0, (v & 255) as f64 / 255.0),
        _ => (1.0, 1.0, 1.0),
    }
}

pub fn color(hex: &str, bright: i64) -> HashMap<&'static str, f64> {
    let k = bright.clamp(0, 100) as f64 / 100.0;
    let (r, g, b) = parse_hex(hex);
    HashMap::from([("red", r * k), ("green", g * k), ("blue", b * k), ("white", r.min(g).min(b) * k)])
}

fn palette_of(s: &LedSettings) -> Palette {
    HashMap::from([
        (Mode::Listening, color(&s.listening, s.brightness)),
        (Mode::Thinking, color(&s.thinking, s.brightness)),
        (Mode::Speaking, color(&s.speaking, s.brightness)),
    ])
}

fn read_int(p: &Path) -> i64 {
    std::fs::read_to_string(p).ok().and_then(|s| s.trim().parse().ok()).unwrap_or(-1)
}

fn write(p: &Path, v: i64) {
    let _ = std::fs::write(p, v.to_string());
}

fn current_trigger(raw: &str) -> &str {
    raw.split_whitespace().find(|f| f.starts_with('[') && f.ends_with(']')).map(|f| f.trim_matches(['[', ']'])).unwrap_or("")
}

impl St {
    fn available(&self) -> bool {
        !self.chans.is_empty() || !self.clock.is_empty()
    }

    fn take_triggers(&mut self) {
        for c in &mut self.chans {
            let tp = c.path.with_file_name("trigger");
            if let Ok(b) = std::fs::read_to_string(&tp) {
                let t = current_trigger(&b);
                if !t.is_empty() && t != "none" {
                    c.trig = t.into();
                    let _ = std::fs::write(&tp, "none");
                }
            }
        }
    }

    fn restore_triggers(&mut self) {
        for c in &mut self.chans {
            if !c.trig.is_empty() {
                let _ = std::fs::write(c.path.with_file_name("trigger"), &c.trig);
                c.trig.clear();
            }
        }
    }

    fn shape(&self, group: usize, t: f64) -> f64 {
        match self.mode {
            Mode::Listening => (t / 0.25).min(1.0) * (0.5 + 0.5 * (0.5 + 0.5 * (2.0 * PI * t / 2.8).cos())),
            Mode::Thinking => {
                let n = self.groups.max(1) as f64;
                let c = (2.0 * PI * (t * 1.2 - group as f64 / n)).cos();
                0.15 + 0.85 * c.max(0.0).powi(2)
            }
            Mode::Speaking => {
                if self.lvl_at.elapsed() > Duration::from_millis(400) {
                    0.2 + 0.8 * (2.0 * PI * t * 1.7).sin().max(0.0).powi(3)
                } else {
                    0.3 + 0.7 * self.level
                }
            }
            Mode::Off => 0.0,
        }
    }

    fn clock_shape(&self, seg: &str, t: f64) -> f64 {
        let b = seg.as_bytes();
        if b.len() != 2 || b[0] != b'g' || !(b'0'..=b'3').contains(&b[1]) {
            return 0.0;
        }
        let pos = (b[1] - b'0') as f64;
        let inner = pos == 1.0 || pos == 2.0;
        match self.mode {
            Mode::Thinking => {
                let c = (2.0 * PI * (t * 1.2 - pos / 4.0)).cos();
                0.1 + 0.9 * c.max(0.0).powi(2)
            }
            Mode::Listening | Mode::Speaking => {
                let v = if self.mode == Mode::Speaking { self.level } else { self.hear };
                if inner { (t / 0.2).min(1.0) * (0.2 + 0.8 * (v / 0.35).min(1.0)) } else { ((v - 0.3) / 0.35).clamp(0.0, 1.0) }
            }
            Mode::Off => 0.0,
        }
    }

    fn frame(&mut self) {
        if self.mode == Mode::Off {
            return;
        }
        self.level = self.level * 0.6 + self.target * 0.4;
        self.target *= 0.5;
        self.hear = self.hear * 0.6 + self.hear_t * 0.4;
        self.hear_t *= 0.5;
        let t = self.since.elapsed().as_secs_f64();
        let pal = self.palette.get(&self.mode).cloned().unwrap_or_default();
        if !self.clock.is_empty() {
            self.frames += 1;
            let k = (pal.values().fold(0f64, |a, &v| a.max(v)) * 1.6).min(1.0);
            let rewrite = self.frames % 12 == 0; // maind мог дорисовать время
            let shapes: Vec<f64> = self.clock.iter().map(|c| self.clock_shape(&c.color, t)).collect();
            for (c, s) in self.clock.iter_mut().zip(shapes) {
                if rewrite {
                    c.last = -1;
                }
                let v = (k * s * c.max as f64) as i64;
                if v != c.last {
                    write(&c.path, v);
                    c.last = v;
                }
            }
        }
        let shapes: Vec<f64> = self.chans.iter().map(|c| self.shape(c.group, t)).collect();
        for (c, s) in self.chans.iter_mut().zip(shapes) {
            let v = (pal.get(c.color.as_str()).copied().unwrap_or(0.0) * s * c.max as f64) as i64;
            if v != c.last {
                write(&c.path, v);
                c.last = v;
            }
        }
    }
}

impl Leds {
    pub fn new(source: Box<dyn Fn() -> LedSettings + Send + Sync>) -> Arc<Leds> {
        let mut chans = vec![];
        let mut clock = vec![];
        let mut groups: BTreeMap<String, usize> = BTreeMap::new();
        let mut entries: Vec<PathBuf> = std::fs::read_dir("/sys/class/leds").map(|d| d.flatten().map(|e| e.path()).collect()).unwrap_or_default();
        entries.sort();
        for e in entries {
            let name = e.file_name().map(|n| n.to_string_lossy().to_lowercase()).unwrap_or_default();
            let Some(color) = ["red", "green", "blue", "white"].iter().rev().find(|c| name.contains(*c)) else { continue };
            if name.contains("mute") {
                continue;
            }
            let mut max = read_int(&e.join("max_brightness"));
            if max <= 0 {
                max = 255;
            }
            if let Some(i) = name.rfind("clock") {
                clock.push(Chan { path: e.join("brightness"), color: name[i + 5..].into(), max, group: 0, last: -1, trig: String::new() });
                continue;
            }
            let key = name.replacen(color, "", 1);
            let n = groups.len();
            let group = *groups.entry(key).or_insert(n);
            chans.push(Chan { path: e.join("brightness"), color: color.to_string(), max, group, last: -1, trig: String::new() });
        }
        let now = Instant::now();
        let l = Arc::new(Leds {
            st: Mutex::new(St {
                palette: default_palette(),
                off: false,
                preview: 0,
                chans,
                clock,
                saved: HashMap::new(),
                mode: Mode::Off,
                until: None,
                groups: groups.len(),
                since: now,
                level: 0.0,
                target: 0.0,
                hear: 0.0,
                hear_t: 0.0,
                frames: 0,
                lvl_at: now,
                phase: Mode::Off,
            }),
            source,
        });
        if l.st.lock().unwrap().available() {
            let w = Arc::downgrade(&l);
            std::thread::Builder::new()
                .name("leds".into())
                .spawn(move || {
                    while let Some(l) = w.upgrade() {
                        let expired = {
                            let mut st = l.st.lock().unwrap();
                            st.frame();
                            st.until.is_some_and(|u| Instant::now() > u)
                        };
                        if expired {
                            l.set(Mode::Off); // подсветка не должна остаться гореть, если что-то пошло не так
                        }
                        drop(l);
                        std::thread::sleep(Duration::from_millis(40));
                    }
                })
                .ok();
        }
        l
    }

    pub fn available(&self) -> bool {
        self.st.lock().unwrap().available()
    }

    /// Громкость голоса в микрофоне (0..1), пока Джарвис слушает — линия на часах Мини 3.
    pub fn hear(&self, v: f64) {
        let mut st = self.st.lock().unwrap();
        if !st.clock.is_empty() {
            st.hear_t = st.hear_t.max(v.min(1.0));
        }
    }

    /// Громкость голоса Джарвиса прямо сейчас (0..1): кольцо вспыхивает в такт.
    pub fn level(&self, v: f64) {
        let mut st = self.st.lock().unwrap();
        st.target = st.target.max(v.min(1.0));
        st.lvl_at = Instant::now();
    }

    /// Цвета и яркость из настроек.
    pub fn configure(&self, s: &LedSettings) {
        {
            let mut st = self.st.lock().unwrap();
            st.palette = palette_of(s);
            st.off = !s.enabled;
        }
        if !s.enabled {
            self.set(Mode::Off);
        }
    }

    /// Показать цвет на dur (примерка цвета в настройках). Работает и при выключенной подсветке.
    pub fn show(self: &Arc<Self>, mode: Mode, hex: &str, bright: i64, dur: Duration) {
        let (n, same) = {
            let mut st = self.st.lock().unwrap();
            st.preview += 1;
            st.palette.insert(mode, color(hex, bright));
            let same = st.mode == mode;
            if same {
                st.frame();
            }
            (st.preview, same)
        };
        if !same {
            self.set_inner(mode, true);
        }
        self.st.lock().unwrap().until = Some(Instant::now() + dur);
        let l = self.clone();
        std::thread::spawn(move || {
            std::thread::sleep(dur);
            let mine = {
                let st = l.st.lock().unwrap();
                st.preview == n && st.mode == mode
            };
            if mine {
                l.set(Mode::Off);
            }
        });
    }

    /// Что делает Джарвис (даже если подсветки нет) — для панели.
    pub fn phase(&self) -> Mode {
        self.st.lock().unwrap().phase
    }

    pub fn set(&self, mode: Mode) {
        self.set_inner(mode, false)
    }

    fn set_inner(&self, mode: Mode, force: bool) {
        let mut st = self.st.lock().unwrap();
        if !force {
            st.phase = mode;
        }
        if st.mode == Mode::Off && mode != Mode::Off && !force {
            let s = (self.source)();
            st.palette = palette_of(&s);
            st.off = !s.enabled;
        }
        if mode == st.mode || !st.available() || (st.off && mode != Mode::Off && !force) {
            return;
        }
        if st.mode == Mode::Off {
            let paths: Vec<PathBuf> = st.chans.iter().chain(st.clock.iter()).map(|c| c.path.clone()).collect();
            for p in paths {
                let v = read_int(&p);
                st.saved.insert(p, v);
            }
            st.hear = 0.0;
            st.hear_t = 0.0;
            st.take_triggers();
        }
        st.mode = mode;
        st.since = Instant::now();
        if mode == Mode::Off {
            let St { chans, clock, saved, .. } = &mut *st;
            for c in chans.iter_mut() {
                write(&c.path, saved.get(&c.path).copied().unwrap_or(0).max(0));
                c.last = -1;
            }
            for c in clock.iter_mut() {
                if let Some(&v) = saved.get(&c.path)
                    && v >= 0
                {
                    write(&c.path, v);
                }
                c.last = -1;
            }
            st.restore_triggers();
        } else {
            st.frame();
        }
        st.until = match mode {
            Mode::Listening => Some(Instant::now() + Duration::from_secs(20)),
            Mode::Thinking | Mode::Speaking => Some(Instant::now() + Duration::from_secs(600)),
            Mode::Off => None,
        };
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn colors() {
        let c = super::color("#ff8000", 50);
        assert!((c["red"] - 0.5).abs() < 1e-9 && (c["green"] - 0.5 * 128.0 / 255.0).abs() < 1e-9 && c["blue"] == 0.0);
        assert_eq!(super::current_trigger("none [led_ring_fb] timer"), "led_ring_fb");
    }
}
