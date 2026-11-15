package main

// Потоковый детектор слова (KWS). На каждые 10 мс звука:
//   1) лог-мел спектр (окно 25 мс, FFT 512, 40 полос 20–7600 Гц), нормировка mean/std из модели;
//   2) причинная свёрточная сеть по времени с растущим шагом (dilation): считаем только новый кадр,
//      прошлое лежит в кольцевых буферах слоёв — почти бесплатно для слабого процессора колонки;
//   3) вероятность «слово только что закончилось», сглаженная за 3 кадра, сравнивается с порогом.
// Формат wake.bin: "JKWS1" + uint32 длина JSON-заголовка + JSON + float32 веса (little endian).
// Обучение и экспорт — kws/train.py; совпадение с Python проверяется тестовым вектором.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
)

type kwsHeader struct {
	SampleRate int       `json:"sample_rate"`
	Win        int       `json:"win"`
	Hop        int       `json:"hop"`
	NFFT       int       `json:"n_fft"`
	NMels      int       `json:"n_mels"`
	FMin       float64   `json:"fmin"`
	FMax       float64   `json:"fmax"`
	Channels   int       `json:"channels"`
	Dilations  []int     `json:"dilations"`
	Kernel     int       `json:"kernel"`
	Thresholds []float64 `json:"thresholds"` // порог для чувствительности 1..10
}

type convLayer struct {
	cin, cout, k, dil int
	w                 []float32 // [cout][cin][k]
	b                 []float32
	hist              [][]float32 // кольцо входов длиной (k-1)*dil+1
	pos               int
}

func (l *convLayer) step(x []float32, out []float32) {
	span := (l.k-1)*l.dil + 1
	copy(l.hist[l.pos], x)
	for o := 0; o < l.cout; o++ {
		acc := l.b[o]
		wo := l.w[o*l.cin*l.k:]
		for t := 0; t < l.k; t++ {
			// t=k-1 — текущий кадр, t=0 — самый старый
			idx := l.pos - (l.k-1-t)*l.dil
			idx = ((idx % span) + span) % span
			in := l.hist[idx]
			for c := 0; c < l.cin; c++ {
				acc += wo[c*l.k+t] * in[c]
			}
		}
		out[o] = acc
	}
	l.pos = (l.pos + 1) % span
}

func (l *convLayer) reset() {
	for _, h := range l.hist {
		for i := range h {
			h[i] = 0
		}
	}
	l.pos = 0
}

type KWS struct {
	mu          sync.Mutex
	h           kwsHeader
	mean, std   []float32
	mel         [][]float32 // [mel][bin]
	window      []float64
	proj        *convLayer
	blocks      []*convLayer
	head        *convLayer
	buf         []float64 // хвост звука, ещё не ставший кадром
	feat, a, b2 []float32
	re, im      []float64 // рабочие буферы кадра — 100 кадров в секунду не мусорят в память
	power, out  []float32
	smooth      [3]float32
	si          int
	sensitivity func() int
	cooldown    int
	frames      int
	LastProb    float32
}

func LoadKWS(data []byte, sensitivity func() int) (*KWS, error) {
	if len(data) < 9 || string(data[:5]) != "JKWS1" {
		return nil, errors.New("wake.bin: не тот формат")
	}
	n := int(binary.LittleEndian.Uint32(data[5:9]))
	if 9+n > len(data) {
		return nil, errors.New("wake.bin: обрезан")
	}
	var h kwsHeader
	if err := json.Unmarshal(data[9:9+n], &h); err != nil {
		return nil, err
	}
	raw := data[9+n:]
	if len(raw)%4 != 0 {
		return nil, errors.New("wake.bin: веса повреждены")
	}
	floats := make([]float32, len(raw)/4)
	for i := range floats {
		floats[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	take := func(n int) ([]float32, error) {
		if len(floats) < n {
			return nil, errors.New("wake.bin: весов меньше, чем нужно")
		}
		v := floats[:n]
		floats = floats[n:]
		return v, nil
	}
	k := &KWS{h: h, sensitivity: sensitivity}
	var err error
	if k.mean, err = take(h.NMels); err != nil {
		return nil, err
	}
	if k.std, err = take(h.NMels); err != nil {
		return nil, err
	}
	mk := func(cin, cout, kern, dil int) (*convLayer, error) {
		l := &convLayer{cin: cin, cout: cout, k: kern, dil: dil}
		if l.w, err = take(cout * cin * kern); err != nil {
			return nil, err
		}
		if l.b, err = take(cout); err != nil {
			return nil, err
		}
		span := (kern-1)*dil + 1
		l.hist = make([][]float32, span)
		for i := range l.hist {
			l.hist[i] = make([]float32, cin)
		}
		return l, nil
	}
	if k.proj, err = mk(h.NMels, h.Channels, 1, 1); err != nil {
		return nil, err
	}
	for _, d := range h.Dilations {
		l, err := mk(h.Channels, h.Channels, h.Kernel, d)
		if err != nil {
			return nil, err
		}
		k.blocks = append(k.blocks, l)
	}
	if k.head, err = mk(h.Channels, 1, 1, 1); err != nil {
		return nil, err
	}
	if len(floats) != 0 {
		return nil, fmt.Errorf("wake.bin: лишние веса (%d)", len(floats))
	}
	if len(h.Thresholds) != 10 {
		return nil, errors.New("wake.bin: нужно 10 порогов")
	}
	k.mel = melBank(h.SampleRate, h.NFFT, h.NMels, h.FMin, h.FMax)
	k.window = make([]float64, h.Win)
	for i := range k.window { // окно Ханна, как torch.hann_window(periodic=True)
		k.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(h.Win))
	}
	k.feat = make([]float32, h.NMels)
	k.a = make([]float32, h.Channels)
	k.b2 = make([]float32, h.Channels)
	return k, nil
}

func hzToMel(f float64) float64 { return 2595 * math.Log10(1+f/700) }
func melToHz(m float64) float64 { return 700 * (math.Pow(10, m/2595) - 1) }

// melBank — треугольные фильтры HTK (так же считает kws/features.py).
func melBank(sr, nfft, nmels int, fmin, fmax float64) [][]float32 {
	bins := nfft/2 + 1
	lo, hi := hzToMel(fmin), hzToMel(fmax)
	pts := make([]float64, nmels+2)
	for i := range pts {
		pts[i] = melToHz(lo + (hi-lo)*float64(i)/float64(nmels+1))
	}
	bank := make([][]float32, nmels)
	for m := 0; m < nmels; m++ {
		bank[m] = make([]float32, bins)
		l, c, r := pts[m], pts[m+1], pts[m+2]
		for b := 0; b < bins; b++ {
			f := float64(b) * float64(sr) / float64(nfft)
			var v float64
			if f > l && f < c {
				v = (f - l) / (c - l)
			} else if f >= c && f < r {
				v = (r - f) / (r - c)
			}
			bank[m][b] = float32(v)
		}
	}
	return bank
}

func (k *KWS) Ready() bool { return true }

func (k *KWS) Reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.proj.reset()
	for _, l := range k.blocks {
		l.reset()
	}
	k.head.reset()
	k.smooth = [3]float32{}
	k.cooldown = 100 // 1 с после разговора не слушаем — хвост своей же речи
}

func (k *KWS) Feed(pcm []int16) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, s := range pcm {
		k.buf = append(k.buf, float64(s)/32768)
	}
	hit := false
	off := 0
	for len(k.buf)-off >= k.h.Win {
		if k.frame(k.buf[off : off+k.h.Win]) {
			hit = true
		}
		off += k.h.Hop
	}
	// хвост — в начало того же буфера, без новых выделений
	k.buf = k.buf[:copy(k.buf, k.buf[off:])]
	return hit
}

func (k *KWS) frame(x []float64) bool {
	n := k.h.NFFT
	bins := n/2 + 1
	if len(k.re) != n {
		k.re, k.im, k.power, k.out = make([]float64, n), make([]float64, n), make([]float32, bins), make([]float32, 1)
	}
	re, im, power := k.re, k.im, k.power
	clear(re)
	clear(im)
	for i, v := range x {
		re[i] = v * k.window[i]
	}
	fft(re, im)
	for b := 0; b < bins; b++ {
		power[b] = float32(re[b]*re[b] + im[b]*im[b])
	}
	for m, f := range k.mel {
		var acc float32
		for b, w := range f {
			if w != 0 {
				acc += w * power[b]
			}
		}
		k.feat[m] = (float32(math.Log(float64(acc)+1e-6)) - k.mean[m]) / k.std[m]
	}
	k.proj.step(k.feat, k.a)
	relu(k.a)
	for _, l := range k.blocks {
		l.step(k.a, k.b2)
		for i := range k.a {
			v := k.b2[i]
			if v < 0 {
				v = 0
			}
			k.a[i] += v // остаточная связь
		}
	}
	out := k.out
	out[0] = 0
	k.head.step(k.a, out)
	p := float32(1 / (1 + math.Exp(-float64(out[0]))))
	k.smooth[k.si%3] = p
	k.si++
	avg := (k.smooth[0] + k.smooth[1] + k.smooth[2]) / 3
	k.LastProb = avg
	k.frames++
	if k.cooldown > 0 {
		k.cooldown--
		return false
	}
	s := 5
	if k.sensitivity != nil {
		s = max(1, min(10, k.sensitivity()))
	}
	if avg >= float32(k.h.Thresholds[s-1]) {
		k.cooldown = 150 // 1.5 с
		return true
	}
	return false
}

func relu(v []float32) {
	for i := range v {
		if v[i] < 0 {
			v[i] = 0
		}
	}
}

// fft — классический радикс-2 на месте.
func fft(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := -2 * math.Pi / float64(size)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for start := 0; start < n; start += size {
			cr, ci := 1.0, 0.0
			for k := 0; k < size/2; k++ {
				a, b := start+k, start+k+size/2
				tr := re[b]*cr - im[b]*ci
				ti := re[b]*ci + im[b]*cr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a] += tr
				im[a] += ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}
