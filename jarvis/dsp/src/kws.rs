//! Потоковый детектор слова (KWS). На каждые 10 мс звука:
//!   1) лог-мел спектр (окно 25 мс, FFT 512, 40 полос 20–7600 Гц), нормировка mean/std из модели;
//!   2) причинная свёрточная сеть по времени с растущим шагом (dilation): считаем только новый кадр,
//!      прошлое лежит в кольцевых буферах слоёв;
//!   3) вероятность «слово только что закончилось», сглаженная за 3 кадра, сравнивается с порогом.
//! Формат wake.bin: "JKWS1" + u32 длина JSON-заголовка + JSON + f32 веса (little endian).

use serde::Deserialize;

#[derive(Deserialize, Clone)]
pub struct Header {
    pub sample_rate: usize,
    pub win: usize,
    pub hop: usize,
    pub n_fft: usize,
    pub n_mels: usize,
    pub fmin: f64,
    pub fmax: f64,
    pub channels: usize,
    pub dilations: Vec<usize>,
    pub kernel: usize,
    pub thresholds: Vec<f64>,
}

struct Conv {
    cin: usize,
    cout: usize,
    k: usize,
    dil: usize,
    w: Vec<f32>, // [cout][cin][k]
    b: Vec<f32>,
    hist: Vec<Vec<f32>>, // кольцо входов длиной (k-1)·dil+1
    pos: usize,
}

impl Conv {
    fn step(&mut self, x: &[f32], out: &mut [f32]) {
        let span = (self.k - 1) * self.dil + 1;
        self.hist[self.pos].copy_from_slice(x);
        for o in 0..self.cout {
            let mut acc = self.b[o];
            let wo = &self.w[o * self.cin * self.k..];
            for t in 0..self.k {
                // t=k-1 — текущий кадр, t=0 — самый старый
                let idx = (self.pos as isize - ((self.k - 1 - t) * self.dil) as isize).rem_euclid(span as isize) as usize;
                let inp = &self.hist[idx];
                for c in 0..self.cin {
                    acc += wo[c * self.k + t] * inp[c];
                }
            }
            out[o] = acc;
        }
        self.pos = (self.pos + 1) % span;
    }

    fn reset(&mut self) {
        for h in &mut self.hist {
            h.fill(0.0);
        }
        self.pos = 0;
    }
}

pub struct Kws {
    pub h: Header,
    mean: Vec<f32>,
    std: Vec<f32>,
    mel: Vec<Vec<f32>>,
    window: Vec<f64>,
    proj: Conv,
    blocks: Vec<Conv>,
    head: Conv,
    buf: Vec<f64>,
    feat: Vec<f32>,
    a: Vec<f32>,
    b2: Vec<f32>,
    re: Vec<f64>,
    im: Vec<f64>,
    power: Vec<f32>,
    smooth: [f32; 3],
    si: usize,
    sensitivity: Box<dyn Fn() -> i64 + Send>,
    pub cooldown: u32,
    pub last_prob: f32,
}

fn hz_to_mel(f: f64) -> f64 {
    2595.0 * (1.0 + f / 700.0).log10()
}

fn mel_to_hz(m: f64) -> f64 {
    700.0 * (10f64.powf(m / 2595.0) - 1.0)
}

/// Треугольные фильтры HTK (так же считает kws/features.py).
pub fn mel_bank(sr: usize, nfft: usize, nmels: usize, fmin: f64, fmax: f64) -> Vec<Vec<f32>> {
    let bins = nfft / 2 + 1;
    let (lo, hi) = (hz_to_mel(fmin), hz_to_mel(fmax));
    let pts: Vec<f64> = (0..nmels + 2).map(|i| mel_to_hz(lo + (hi - lo) * i as f64 / (nmels + 1) as f64)).collect();
    (0..nmels)
        .map(|m| {
            let (l, c, r) = (pts[m], pts[m + 1], pts[m + 2]);
            (0..bins)
                .map(|b| {
                    let f = b as f64 * sr as f64 / nfft as f64;
                    let v = if f > l && f < c {
                        (f - l) / (c - l)
                    } else if f >= c && f < r {
                        (r - f) / (r - c)
                    } else {
                        0.0
                    };
                    v as f32
                })
                .collect()
        })
        .collect()
}

impl Kws {
    pub fn load(data: &[u8], sensitivity: Box<dyn Fn() -> i64 + Send>) -> Result<Kws, String> {
        if data.len() < 9 || &data[..5] != b"JKWS1" {
            return Err("wake.bin: не тот формат".into());
        }
        let n = u32::from_le_bytes(data[5..9].try_into().unwrap()) as usize;
        if 9 + n > data.len() {
            return Err("wake.bin: обрезан".into());
        }
        let h: Header = serde_json::from_slice(&data[9..9 + n]).map_err(|e| e.to_string())?;
        let raw = &data[9 + n..];
        if raw.len() % 4 != 0 {
            return Err("wake.bin: веса повреждены".into());
        }
        let floats: Vec<f32> = raw.chunks_exact(4).map(|c| f32::from_le_bytes(c.try_into().unwrap())).collect();
        let mut off = 0;
        let mut take = |n: usize| -> Result<Vec<f32>, String> {
            if floats.len() - off < n {
                return Err("wake.bin: весов меньше, чем нужно".into());
            }
            off += n;
            Ok(floats[off - n..off].to_vec())
        };
        let mean = take(h.n_mels)?;
        let std = take(h.n_mels)?;
        let mut mk = |cin: usize, cout: usize, k: usize, dil: usize| -> Result<Conv, String> {
            let w = take(cout * cin * k)?;
            let b = take(cout)?;
            let span = (k - 1) * dil + 1;
            Ok(Conv { cin, cout, k, dil, w, b, hist: vec![vec![0.0; cin]; span], pos: 0 })
        };
        let proj = mk(h.n_mels, h.channels, 1, 1)?;
        let mut blocks = vec![];
        for &d in &h.dilations {
            blocks.push(mk(h.channels, h.channels, h.kernel, d)?);
        }
        let head = mk(h.channels, 1, 1, 1)?;
        if off != floats.len() {
            return Err(format!("wake.bin: лишние веса ({})", floats.len() - off));
        }
        if h.thresholds.len() != 10 {
            return Err("wake.bin: нужно 10 порогов".into());
        }
        // окно Ханна, как torch.hann_window(periodic=True)
        let window = (0..h.win).map(|i| 0.5 - 0.5 * (2.0 * std::f64::consts::PI * i as f64 / h.win as f64).cos()).collect();
        let bins = h.n_fft / 2 + 1;
        Ok(Kws {
            mel: mel_bank(h.sample_rate, h.n_fft, h.n_mels, h.fmin, h.fmax),
            mean,
            std,
            window,
            proj,
            blocks,
            head,
            buf: Vec::with_capacity(4096),
            feat: vec![0.0; h.n_mels],
            a: vec![0.0; h.channels],
            b2: vec![0.0; h.channels],
            re: vec![0.0; h.n_fft],
            im: vec![0.0; h.n_fft],
            power: vec![0.0; bins],
            smooth: [0.0; 3],
            si: 0,
            sensitivity,
            cooldown: 0,
            last_prob: 0.0,
            h,
        })
    }

    /// Один кадр (окно win): обновить сеть; вернуть сглаженную вероятность.
    pub fn frame(&mut self, x: &[f64]) -> f32 {
        let bins = self.h.n_fft / 2 + 1;
        self.re.fill(0.0);
        self.im.fill(0.0);
        for (i, v) in x.iter().enumerate() {
            self.re[i] = v * self.window[i];
        }
        crate::fft(&mut self.re, &mut self.im);
        for b in 0..bins {
            self.power[b] = (self.re[b] * self.re[b] + self.im[b] * self.im[b]) as f32;
        }
        for (m, f) in self.mel.iter().enumerate() {
            let mut acc = 0f32;
            for (b, &w) in f.iter().enumerate() {
                if w != 0.0 {
                    acc += w * self.power[b];
                }
            }
            self.feat[m] = (((acc as f64) + 1e-6).ln() as f32 - self.mean[m]) / self.std[m];
        }
        self.proj.step(&self.feat, &mut self.a);
        self.a.iter_mut().for_each(|v| *v = v.max(0.0));
        for l in &mut self.blocks {
            l.step(&self.a, &mut self.b2);
            for (a, &v) in self.a.iter_mut().zip(&self.b2) {
                *a += v.max(0.0); // остаточная связь
            }
        }
        let mut out = [0f32];
        self.head.step(&self.a, &mut out);
        let p = (1.0 / (1.0 + (-(out[0] as f64)).exp())) as f32;
        self.smooth[self.si % 3] = p;
        self.si += 1;
        let avg = (self.smooth[0] + self.smooth[1] + self.smooth[2]) / 3.0;
        self.last_prob = avg;
        avg
    }

    /// Последняя несглаженная вероятность кадра (для сверки с Python).
    pub fn last_raw(&self) -> f32 {
        self.smooth[(self.si + 2) % 3]
    }
}

impl crate::Wake for Kws {
    fn feed(&mut self, pcm: &[i16]) -> bool {
        self.buf.extend(pcm.iter().map(|&s| s as f64 / 32768.0));
        let mut hit = false;
        let mut off = 0;
        let (win, hop) = (self.h.win, self.h.hop);
        while self.buf.len() - off >= win {
            let frame: Vec<f64> = self.buf[off..off + win].to_vec();
            let avg = self.frame(&frame);
            off += hop;
            if self.cooldown > 0 {
                self.cooldown -= 1;
                continue;
            }
            let s = (self.sensitivity)().clamp(1, 10) as usize;
            if avg >= self.h.thresholds[s - 1] as f32 {
                self.cooldown = 150; // 1,5 с
                hit = true;
            }
        }
        self.buf.drain(..off);
        hit
    }

    fn reset(&mut self) {
        self.proj.reset();
        for l in &mut self.blocks {
            l.reset();
        }
        self.head.reset();
        self.smooth = [0.0; 3];
        self.cooldown = 100; // 1 с после разговора не слушаем — хвост своей же речи
    }

    fn last_prob(&self) -> f32 {
        self.last_prob
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Совпадение с PyTorch: JARVIS_KWS_DIR=…/kws/out cargo test -p jarvis-dsp kws
    #[test]
    fn matches_python() {
        let Ok(dir) = std::env::var("JARVIS_KWS_DIR") else {
            eprintln!("нет JARVIS_KWS_DIR — пропускаю");
            return;
        };
        let dir = std::path::Path::new(&dir);
        let bin = std::fs::read(dir.join("wake.bin")).unwrap();
        #[derive(Deserialize)]
        struct Tv {
            pcm: Vec<i16>,
            probs: Vec<f64>,
        }
        let tv: Tv = serde_json::from_slice(&std::fs::read(dir.join("testvec.json")).unwrap()).unwrap();
        let mut k = Kws::load(&bin, Box::new(|| 5)).unwrap();
        let mut got = vec![];
        let mut buf: Vec<f64> = vec![];
        let t0 = std::time::Instant::now();
        for chunk in tv.pcm.chunks(320) {
            buf.extend(chunk.iter().map(|&s| s as f64 / 32768.0));
            while buf.len() >= k.h.win {
                let f: Vec<f64> = buf[..k.h.win].to_vec();
                k.frame(&f);
                got.push(k.last_raw() as f64);
                buf.drain(..k.h.hop);
            }
        }
        let el = t0.elapsed();
        assert_eq!(got.len(), tv.probs.len(), "кадров");
        let maxd = got.iter().zip(&tv.probs).map(|(a, b)| (a - b).abs()).fold(0.0, f64::max);
        let peak = got.iter().cloned().fold(0.0, f64::max);
        let peak_py = tv.probs.iter().cloned().fold(0.0, f64::max);
        eprintln!("кадров {}, макс. расхождение {maxd:.5}, пик Rust {peak:.3} / Python {peak_py:.3}, {:?} на {:.1} с звука", got.len(), el, tv.pcm.len() as f64 / 16000.0);
        assert!(maxd < 0.01, "Rust и Python расходятся: {maxd}");
    }
}
