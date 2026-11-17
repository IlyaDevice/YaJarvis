//! Слово «Джарвис» на признаках openWakeWord. Звук → мел-спектр (32 полосы, 10 мс) → сеть speech
//! embedding от Google (Apache 2.0, веса из openWakeWord) → «отпечаток» из 96 чисел каждые 80 мс → наша
//! маленькая сеть по 16 отпечаткам подряд → вероятность «Джарвис».
//!
//! Сеть отпечатков считается потоково: свёртки по времени без отступов, шаг по времени ровно 8 кадров,
//! поэтому каждые 80 мс досчитываются только новые строки каждого слоя.

use crate::{Bin, dot, gemv};

const STEP: usize = 1280; // 80 мс
const CTX: usize = 480;
const NFFT: usize = 512;
const HOP: usize = 160;
const MELS: usize = 32;
const BINS: usize = NFFT / 2 + 1;
const DROP: usize = 4; // окна сети начинаются с кадра 4 (как у openWakeWord при шаге 8)

/// После каких свёрток (1-based) пулинг (время×мелы).
fn pool_after(i: usize) -> Option<(usize, usize)> {
    match i {
        3 | 11 | 19 => Some((2, 2)),
        7 | 15 => Some((1, 2)),
        _ => None,
    }
}

struct Conv {
    co: usize,
    ci: usize,
    kh: usize,
    kw: usize,
    w: Vec<f32>, // [kh][kw][ci][co]
    b: Vec<f32>,
    win: usize,
    wout: usize,
    rows: Vec<Vec<f32>>, // последние kh строк входа, [0] — самая старая
    out: Vec<f32>,
    act: bool,
}

impl Conv {
    fn forward(&mut self) {
        let (co, ci, kw) = (self.co, self.ci, self.kw);
        let pad = kw / 2;
        let span = kw * ci;
        for x in 0..self.wout {
            self.out[x * co..(x + 1) * co].copy_from_slice(&self.b);
        }
        for x in 0..self.wout {
            let lo = x as isize - pad as isize;
            let skip = if lo < 0 { (-lo) as usize } else { 0 };
            let hi = ((lo + kw as isize) as usize).min(self.win);
            let start = (lo + skip as isize) as usize;
            let n = (hi - start) * ci;
            let dst = &mut self.out[x * co..(x + 1) * co];
            for dh in 0..self.kh {
                let src = &self.rows[dh][start * ci..start * ci + n];
                let w = &self.w[(dh * span + skip * ci) * co..];
                gemv(dst, src, w, co);
            }
        }
        if self.act {
            for v in &mut self.out {
                if *v < 0.0 {
                    *v = (*v * 0.2).max(-0.4);
                }
            }
        }
    }
}

struct Pool {
    ph: usize,
    pw: usize,
    win: usize,
    c: usize,
    pending: Option<Vec<f32>>,
    out: Vec<f32>,
}

impl Pool {
    fn forward(&mut self, row: &[f32]) -> bool {
        if self.ph == 2 && self.pending.is_none() {
            self.pending = Some(row.to_vec());
            return false;
        }
        let wout = self.win / self.pw;
        for x in 0..wout {
            for ch in 0..self.c {
                let mut m = f32::NEG_INFINITY;
                for dw in 0..self.pw {
                    let i = (x * self.pw + dw) * self.c + ch;
                    m = m.max(row[i]);
                    if let Some(p) = &self.pending {
                        m = m.max(p[i]);
                    }
                }
                self.out[x * self.c + ch] = m;
            }
        }
        self.pending = None;
        true
    }
}

pub struct Oww {
    window: Vec<f64>,
    melmat: Vec<f32>, // [257][32]
    floor: f64,
    convs: Vec<Conv>,
    pools: Vec<Option<Pool>>, // pools[i] — после свёртки i (0-based)
    seq: usize,
    emb: usize,
    h: usize,
    w1: Vec<f32>,
    b1: Vec<f32>,
    w2: Vec<f32>,
    b2: Vec<f32>,
    w3: Vec<f32>,
    b3: f32,
    thresholds: [f32; 10],
    sensitivity: Box<dyn Fn() -> i64 + Send>,
    buf: Vec<f32>,
    dropped: usize,
    pub ring: Vec<Vec<f32>>,
    cooldown: u32,
    pub last_prob: f32,
    pending: u32,
    peak: f32,
    pub last_wake_prob: f32,
    re: Vec<f64>,
    im: Vec<f64>,
    power: Vec<f64>,
}

impl Oww {
    /// feat: oww.bin (мел и сеть отпечатков), head: wake_oww.bin (наша сеть «Джарвис»).
    pub fn load(feat: &[u8], head: &[u8], sensitivity: Box<dyn Fn() -> i64 + Send>) -> Result<Oww, String> {
        if feat.len() < 8 || &feat[..4] != b"OWW1" || head.len() < 8 || &head[..4] != b"OWWH" {
            return Err("не те файлы модели openWakeWord".into());
        }
        let mut r = Bin::new(&feat[4..]);
        let floor = r.f32s(1)[0] as f64;
        let window = r.f32s(NFFT).into_iter().map(|v| v as f64).collect();
        let melmat = r.f32s(BINS * MELS);
        let n = r.u32();
        let mut width = MELS;
        let mut convs = vec![];
        let mut pools = vec![];
        for i in 1..=n {
            if r.err {
                break;
            }
            let (co, ci, kh, kw) = (r.u32(), r.u32(), r.u32(), r.u32());
            let raw = r.f32s(co * ci * kh * kw); // [co][ci][kh][kw]
            let b = r.f32s(co);
            let mut w = vec![0f32; raw.len()];
            for o in 0..co {
                for c in 0..ci {
                    for dh in 0..kh {
                        for dw in 0..kw {
                            w[((dh * kw + dw) * ci + c) * co + o] = raw[((o * ci + c) * kh + dh) * kw + dw];
                        }
                    }
                }
            }
            convs.push(Conv { co, ci, kh, kw, w, b, win: width, wout: width, rows: vec![], out: vec![0.0; width * co], act: true });
            match pool_after(i) {
                Some((ph, pw)) => {
                    pools.push(Some(Pool { ph, pw, win: width, c: co, pending: None, out: vec![0.0; (width / pw) * co] }));
                    width /= pw;
                }
                None => pools.push(None),
            }
        }
        if r.err || convs.len() != 20 || width != 1 {
            return Err("модель признаков повреждена".into());
        }
        convs.last_mut().unwrap().act = false;

        let mut hr = Bin::new(&head[4..]);
        let (seq, emb, h) = (hr.u32(), hr.u32(), hr.u32());
        let th = hr.f32s(10);
        let w1 = hr.f32s(h * seq * emb);
        let b1 = hr.f32s(h);
        let w2 = hr.f32s(h * h);
        let b2 = hr.f32s(h);
        let w3 = hr.f32s(h);
        let b3 = hr.f32s(1)[0];
        if hr.err || emb != 96 {
            return Err("модель «Джарвис» повреждена".into());
        }
        let mut thresholds = [0f32; 10];
        thresholds.copy_from_slice(&th);
        Ok(Oww {
            window,
            melmat,
            floor,
            convs,
            pools,
            seq,
            emb,
            h,
            w1,
            b1,
            w2,
            b2,
            w3,
            b3,
            thresholds,
            sensitivity,
            buf: vec![0.0; CTX],
            dropped: 0,
            ring: Vec::with_capacity(seq),
            cooldown: 0,
            last_prob: 0.0,
            pending: 0,
            peak: 0.0,
            last_wake_prob: 0.0,
            re: vec![0.0; NFFT],
            im: vec![0.0; NFFT],
            power: vec![0.0; BINS],
        })
    }

    /// 80 мс звука: 8 кадров мел-спектра, новые строки сети, вероятность.
    fn step(&mut self) -> bool {
        let frames = self.mel();
        let mut hit = false;
        for f in frames {
            if self.dropped < DROP {
                self.dropped += 1;
                continue;
            }
            if let Some(e) = self.push(f) {
                if self.head(e) {
                    hit = true;
                }
            }
        }
        hit
    }

    fn mel(&mut self) -> Vec<Vec<f32>> {
        let n = (self.buf.len() - NFFT) / HOP + 1;
        let mut out = vec![vec![0f32; MELS]; n];
        let mut top = f64::NEG_INFINITY;
        for (t, row) in out.iter_mut().enumerate() {
            for i in 0..NFFT {
                self.re[i] = self.buf[t * HOP + i] as f64 * self.window[i];
                self.im[i] = 0.0;
            }
            crate::fft(&mut self.re, &mut self.im);
            for b in 0..BINS {
                self.power[b] = self.re[b] * self.re[b] + self.im[b] * self.im[b];
            }
            for (m, cell) in row.iter_mut().enumerate() {
                let mut acc = 0f64;
                for b in 0..BINS {
                    acc += self.power[b] * self.melmat[b * MELS + m] as f64;
                }
                let db = 10.0 * acc.max(self.floor).log10();
                *cell = db as f32;
                top = top.max(db);
            }
        }
        for row in &mut out {
            for v in row.iter_mut() {
                *v = ((*v as f64).max(top - 80.0) / 10.0 + 2.0) as f32;
            }
        }
        out
    }

    /// Новая строка на вход сети; когда доходит до конца — отпечаток (96 чисел).
    fn push(&mut self, row: Vec<f32>) -> Option<Vec<f32>> {
        let mut row = row;
        for i in 0..self.convs.len() {
            let c = &mut self.convs[i];
            if c.rows.len() < c.kh {
                c.rows.push(row);
            } else {
                c.rows.rotate_left(1);
                *c.rows.last_mut().unwrap() = row;
            }
            if c.rows.len() < c.kh {
                return None;
            }
            c.forward();
            row = c.out.clone();
            if let Some(p) = self.pools[i].as_mut() {
                if !p.forward(&row) {
                    return None;
                }
                row = p.out.clone();
            }
        }
        Some(row)
    }

    /// Новый отпечаток: по последним seq считаем вероятность «Джарвис».
    fn head(&mut self, e: Vec<f32>) -> bool {
        if self.ring.len() < self.seq {
            self.ring.push(e);
        } else {
            self.ring.rotate_left(1);
            *self.ring.last_mut().unwrap() = e;
        }
        if self.ring.len() < self.seq {
            return false;
        }
        let inp = self.emb * self.seq;
        let mut h1 = vec![0f32; self.h];
        for (j, out) in h1.iter_mut().enumerate() {
            let mut acc = self.b1[j];
            let wj = &self.w1[j * inp..(j + 1) * inp];
            for (t, v) in self.ring.iter().enumerate() {
                acc += dot(v, &wj[t * self.emb..(t + 1) * self.emb]);
            }
            *out = acc.max(0.0);
        }
        let mut acc = self.b3;
        for j in 0..self.h {
            let a = (self.b2[j] + dot(&h1, &self.w2[j * self.h..(j + 1) * self.h])).max(0.0);
            acc += a * self.w3[j];
        }
        let p = (1.0 / (1.0 + (-(acc as f64)).exp())) as f32;
        self.detect(p)
    }

    fn detect(&mut self, p: f32) -> bool {
        self.last_prob = p;
        if self.cooldown > 0 {
            self.cooldown -= 1;
            return false;
        }
        if self.pending > 0 {
            self.peak = self.peak.max(p);
            self.pending -= 1;
            if self.pending == 0 {
                self.last_wake_prob = self.peak;
                self.cooldown = 19;
                return true;
            }
            return false;
        }
        let s = (self.sensitivity)().clamp(1, 10) as usize;
        if p >= self.thresholds[s - 1] {
            // первое пересечение часто 0,4, а через 80–240 мс тот же оклик достигает 0,99: ждём пик
            self.pending = 3;
            self.peak = p;
        }
        false
    }
}

impl crate::Wake for Oww {
    fn feed(&mut self, pcm: &[i16]) -> bool {
        let mut hit = false;
        for &s in pcm {
            self.buf.push(s as f32);
            if self.buf.len() == CTX + STEP {
                if self.step() {
                    hit = true;
                }
                self.buf.drain(..STEP);
            }
        }
        hit
    }

    fn reset(&mut self) {
        // сеть потоковая: сброс контекста давал ~2,5 с «глухоты»; подавляем лишь повторный оклик
        self.cooldown = 12;
        self.pending = 0;
    }

    fn last_prob(&self) -> f32 {
        self.last_prob
    }

    fn wake_prob(&self) -> f32 {
        self.last_wake_prob
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Wake;

    /// Те же входы и «золотые» отпечатки, что у Go (jarvisd/testdata/oww-stream.bin).
    #[test]
    fn stream_matches_go_golden() {
        let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../..");
        let (Ok(feat), Ok(head)) = (std::fs::read(root.join("models/oww.bin")), std::fs::read(root.join("models/wake_oww.bin"))) else {
            eprintln!("нет моделей — пропускаю");
            return;
        };
        let gold = std::env::var("JARVIS_OWW_GOLDEN").map(std::path::PathBuf::from).unwrap_or_else(|_| root.join("jarvisd/testdata/oww-stream.bin"));
        let Ok(gold) = std::fs::read(gold) else {
            eprintln!("нет oww-stream.bin — пропускаю");
            return;
        };
        let want: Vec<f32> = gold.chunks_exact(4).map(|c| f32::from_le_bytes(c.try_into().unwrap())).collect();
        let mut o = Oww::load(&feat, &head, Box::new(|| 5)).unwrap();
        let mut values = vec![];
        let mut seed: u32 = 17;
        let mut pcm = vec![0i16; STEP];
        let t0 = std::time::Instant::now();
        for _ in 0..80 {
            for s in pcm.iter_mut() {
                seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
                *s = (seed >> 16) as u16 as i16;
            }
            o.feed(&pcm);
            if let Some(last) = o.ring.last() {
                values.extend_from_slice(last);
                values.push(o.last_prob);
            }
        }
        eprintln!("80 шагов (6,4 с звука) за {:?}", t0.elapsed());
        assert_eq!(values.len(), want.len());
        for (i, (a, b)) in values.iter().zip(&want).enumerate() {
            assert!(!a.is_nan() && (a - b).abs() <= 0.001, "отпечаток/вероятность {i}: {a} против {b}");
        }
    }
}
