//! Звук Джарвиса: спектр и детекторы слова «Джарвис». Порт Go-версии один в один —
//! совпадение проверяется тестами на тех же моделях и векторах (Python, «золотые» данные Go).

pub mod audio;
pub mod kws;
pub mod oww;

/// Детектор слова активации: звук 16 кГц моно кусками любой длины.
pub trait Wake: Send {
    /// true — только что прозвучало «Джарвис».
    fn feed(&mut self, pcm: &[i16]) -> bool;
    /// После разговора: не слушать хвост своей же речи.
    fn reset(&mut self);
    /// Последняя вероятность (для панели и журнала).
    fn last_prob(&self) -> f32;
    /// Вероятность в момент срабатывания.
    fn wake_prob(&self) -> f32 {
        self.last_prob()
    }
}

/// Классический радикс-2 FFT на месте (как в Go: те же операции в том же порядке).
pub fn fft(re: &mut [f64], im: &mut [f64]) {
    let n = re.len();
    let mut j = 0usize;
    for i in 1..n {
        let mut bit = n >> 1;
        while j & bit != 0 {
            j ^= bit;
            bit >>= 1;
        }
        j ^= bit;
        if i < j {
            re.swap(i, j);
            im.swap(i, j);
        }
    }
    let mut size = 2;
    while size <= n {
        let ang = -2.0 * std::f64::consts::PI / size as f64;
        let (wr, wi) = (ang.cos(), ang.sin());
        let mut start = 0;
        while start < n {
            let (mut cr, mut ci) = (1.0f64, 0.0f64);
            for k in 0..size / 2 {
                let (a, b) = (start + k, start + k + size / 2);
                let tr = re[b] * cr - im[b] * ci;
                let ti = re[b] * ci + im[b] * cr;
                re[b] = re[a] - tr;
                im[b] = im[a] - ti;
                re[a] += tr;
                im[a] += ti;
                let ncr = cr * wr - ci * wi;
                ci = cr * wi + ci * wr;
                cr = ncr;
            }
            start += size;
        }
        size <<= 1;
    }
}

/// dst[c] += Σ src[i] · w[i·stride + c]  (свёртки: выходные каналы подряд — векторизуется).
#[inline]
pub fn gemv(dst: &mut [f32], src: &[f32], w: &[f32], stride: usize) {
    let n = dst.len();
    for (i, &v) in src.iter().enumerate() {
        let row = &w[i * stride..i * stride + n];
        for (d, &r) in dst.iter_mut().zip(row) {
            *d += v * r;
        }
    }
}

/// Скалярное произведение: четыре независимые суммы (как dotGo).
#[inline]
pub fn dot(a: &[f32], b: &[f32]) -> f32 {
    let b = &b[..a.len()];
    let (mut s0, mut s1, mut s2, mut s3) = (0f32, 0f32, 0f32, 0f32);
    let mut ca = a.chunks_exact(4);
    let mut cb = b.chunks_exact(4);
    for (x, y) in (&mut ca).zip(&mut cb) {
        s0 += x[0] * y[0];
        s1 += x[1] * y[1];
        s2 += x[2] * y[2];
        s3 += x[3] * y[3];
    }
    for (x, y) in ca.remainder().iter().zip(cb.remainder()) {
        s0 += x * y;
    }
    (s0 + s1) + (s2 + s3)
}

/// Чтение моделей: little-endian u32 и f32 подряд.
pub(crate) struct Bin<'a> {
    b: &'a [u8],
    pub err: bool,
}

impl<'a> Bin<'a> {
    pub fn new(b: &'a [u8]) -> Self {
        Bin { b, err: false }
    }
    pub fn u32(&mut self) -> usize {
        if self.b.len() < 4 {
            self.err = true;
            return 0;
        }
        let v = u32::from_le_bytes(self.b[..4].try_into().unwrap());
        self.b = &self.b[4..];
        v as usize
    }
    pub fn f32s(&mut self, n: usize) -> Vec<f32> {
        if self.err || self.b.len() < n * 4 {
            self.err = true;
            return vec![0.0; n];
        }
        let v = self.b[..n * 4].chunks_exact(4).map(|c| f32::from_le_bytes(c.try_into().unwrap())).collect();
        self.b = &self.b[n * 4..];
        v
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fft_of_impulse_is_flat() {
        let mut re = vec![0.0; 8];
        let mut im = vec![0.0; 8];
        re[0] = 1.0;
        fft(&mut re, &mut im);
        assert!(re.iter().all(|v| (v - 1.0).abs() < 1e-12) && im.iter().all(|v| v.abs() < 1e-12));
    }

    #[test]
    fn dot_and_gemv() {
        let a: Vec<f32> = (0..10).map(|i| i as f32).collect();
        assert_eq!(dot(&a, &a), 285.0);
        let mut d = vec![1.0, 1.0];
        gemv(&mut d, &[1.0, 2.0], &[1.0, 2.0, 3.0, 4.0], 2);
        assert_eq!(d, vec![8.0, 11.0]);
    }
}
