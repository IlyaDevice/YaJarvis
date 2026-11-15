package main

// Слово «Джарвис» на признаках openWakeWord. Звук → мел-спектр (32 полосы, 10 мс) → готовая сеть speech
// embedding от Google (Apache 2.0, веса из openWakeWord) → «отпечаток» из 96 чисел каждые 80 мс → наша
// маленькая сеть по 16 отпечаткам подряд → вероятность «Джарвис».
//
// Сеть отпечатков считается потоково: её свёртки по времени без отступов, а шаг по времени ровно 8 кадров,
// поэтому каждые 80 мс досчитываются только новые строки каждого слоя — в 9 раз дешевле, чем окно целиком.
// Результат совпадает с полным расчётом (kws/oww_feat.py) до округления.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

const (
	owwStep  = 1280 // 80 мс
	owwCtx   = 480
	owwNFFT  = 512
	owwHop   = 160
	owwMels  = 32
	owwBins  = owwNFFT/2 + 1
	owwDrop  = 4 // окна сети начинаются с кадра 4 (как у openWakeWord при шаге 8)
	owwPools = 5 // после каких свёрток пулинг — см. owwPoolAfter
)

// после 3-й, 7-й, 11-й, 15-й и 19-й свёртки: пулинг (время×мелы)
var owwPoolAfter = map[int][2]int{3: {2, 2}, 7: {1, 2}, 11: {2, 2}, 15: {1, 2}, 19: {2, 2}}

type owwConv struct {
	co, ci, kh, kw int
	w              []float32 // [kh][kw][ci][co]: по выходным каналам подряд — так считает gemv
	b              []float32
	win, wout      int // ширина (мелы) на входе и выходе
	rows           [][]float32
	act            bool
}

type owwPool struct {
	ph, pw  int
	win, c  int
	pending []float32
}

type OWW struct {
	mu          sync.Mutex
	window      []float64
	melmat      []float32 // [257][32]
	floor       float64
	convs       []*owwConv
	pools       map[int]*owwPool
	seq, emb, h int
	w1, b1      []float32
	w2, b2      []float32
	w3, b3      []float32
	thresholds  [10]float32
	sensitivity func() int

	buf          []float32 // звук в масштабе int16: контекст + новое
	dropped      int
	ring         [][]float32
	cooldown     int
	LastProb     float32
	pending      int
	peak         float32
	LastWakeProb float32 // максимум короткого окна подтверждения, а не первое пересечение порога
}

type binReader struct {
	r   *bytes.Reader
	err error
}

func (b *binReader) u32() int {
	var v uint32
	if b.err == nil {
		b.err = binary.Read(b.r, binary.LittleEndian, &v)
	}
	return int(v)
}

func (b *binReader) f32s(n int) []float32 {
	v := make([]float32, n)
	if b.err == nil {
		b.err = binary.Read(b.r, binary.LittleEndian, v)
	}
	return v
}

// LoadOWW — feat: oww.bin (мел и сеть отпечатков), head: wake_oww.bin (наша сеть «Джарвис»).
func LoadOWW(feat, head []byte, sensitivity func() int) (*OWW, error) {
	if len(feat) < 8 || string(feat[:4]) != "OWW1" || len(head) < 8 || string(head[:4]) != "OWWH" {
		return nil, errors.New("не те файлы модели openWakeWord")
	}
	o := &OWW{sensitivity: sensitivity, pools: map[int]*owwPool{}}
	r := &binReader{r: bytes.NewReader(feat[4:])}
	o.floor = float64(r.f32s(1)[0])
	w := r.f32s(owwNFFT)
	o.window = make([]float64, owwNFFT)
	for i, v := range w {
		o.window[i] = float64(v)
	}
	o.melmat = r.f32s(owwBins * owwMels)
	n := r.u32()
	width := owwMels
	for i := 1; i <= n && r.err == nil; i++ {
		c := &owwConv{co: r.u32(), ci: r.u32(), kh: r.u32(), kw: r.u32()}
		raw := r.f32s(c.co * c.ci * c.kh * c.kw) // [co][ci][kh][kw]
		c.b = r.f32s(c.co)
		c.w = make([]float32, len(raw))
		for co := 0; co < c.co; co++ {
			for ci := 0; ci < c.ci; ci++ {
				for dh := 0; dh < c.kh; dh++ {
					for dw := 0; dw < c.kw; dw++ {
						c.w[((dh*c.kw+dw)*c.ci+ci)*c.co+co] = raw[((co*c.ci+ci)*c.kh+dh)*c.kw+dw]
					}
				}
			}
		}
		c.win = width
		c.wout = width
		if c.kw == 1 && c.kh == 3 && i == n { // последняя свёртка — без активации
			c.act = false
		} else {
			c.act = true
		}
		o.convs = append(o.convs, c)
		if p, ok := owwPoolAfter[i]; ok {
			o.pools[i] = &owwPool{ph: p[0], pw: p[1], win: width, c: c.co}
			width /= p[1]
		}
	}
	if r.err != nil || len(o.convs) != 20 || width != 1 {
		return nil, fmt.Errorf("модель признаков повреждена (%v)", r.err)
	}
	o.convs[len(o.convs)-1].act = false

	hr := &binReader{r: bytes.NewReader(head[4:])}
	o.seq, o.emb, o.h = hr.u32(), hr.u32(), hr.u32()
	copy(o.thresholds[:], hr.f32s(10))
	o.w1, o.b1 = hr.f32s(o.h*o.seq*o.emb), hr.f32s(o.h)
	o.w2, o.b2 = hr.f32s(o.h*o.h), hr.f32s(o.h)
	o.w3, o.b3 = hr.f32s(o.h), hr.f32s(1)
	if hr.err != nil || o.emb != 96 {
		return nil, fmt.Errorf("модель «Джарвис» повреждена (%v)", hr.err)
	}
	o.resetLocked()
	return o, nil
}

func (o *OWW) Ready() bool { return true }

func (o *OWW) resetLocked() {
	o.buf = make([]float32, owwCtx, owwCtx+owwStep*2)
	o.dropped = 0
	o.ring = o.ring[:0]
	for _, c := range o.convs {
		c.rows = c.rows[:0]
	}
	for _, p := range o.pools {
		p.pending = nil
	}
}

func (o *OWW) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Сеть потоковая: сброс её контекста создавал ~2,5 с «глухоты» после каждого разговора.
	// Микрофон продолжает идти, поэтому сохраняем признаки и подавляем лишь повторный оклик.
	o.cooldown = 12 // ~1 с после разговора не слушаем — хвост своей же речи
	o.pending = 0
}

func (o *OWW) Feed(pcm []int16) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	hit := false
	for _, s := range pcm {
		o.buf = append(o.buf, float32(s))
		if len(o.buf) == owwCtx+owwStep {
			if o.step() {
				hit = true
			}
			copy(o.buf, o.buf[owwStep:])
			o.buf = o.buf[:owwCtx]
		}
	}
	return hit
}

// step — 80 мс звука: 8 кадров мел-спектра, новые строки сети, вероятность.
func (o *OWW) step() bool {
	frames := o.mel(o.buf)
	var hit bool
	for _, f := range frames {
		if o.dropped < owwDrop {
			o.dropped++
			continue
		}
		if e := o.push(0, f); e != nil {
			if o.head(e) {
				hit = true
			}
		}
	}
	return hit
}

func (o *OWW) mel(x []float32) [][]float32 {
	n := (len(x)-owwNFFT)/owwHop + 1
	out := make([][]float32, n)
	re := make([]float64, owwNFFT)
	im := make([]float64, owwNFFT)
	power := make([]float64, owwBins)
	top := math.Inf(-1)
	for t := 0; t < n; t++ {
		for i := 0; i < owwNFFT; i++ {
			re[i] = float64(x[t*owwHop+i]) * o.window[i]
			im[i] = 0
		}
		fft(re, im)
		for b := 0; b < owwBins; b++ {
			power[b] = re[b]*re[b] + im[b]*im[b]
		}
		row := make([]float32, owwMels)
		for m := 0; m < owwMels; m++ {
			var acc float64
			for b := 0; b < owwBins; b++ {
				acc += power[b] * float64(o.melmat[b*owwMels+m])
			}
			db := 10 * math.Log10(math.Max(acc, o.floor))
			row[m] = float32(db)
			top = math.Max(top, db)
		}
		out[t] = row
	}
	for _, row := range out {
		for m, v := range row {
			row[m] = float32(math.Max(float64(v), top-80)/10 + 2)
		}
	}
	return out
}

// push — новая строка на вход свёртки i; когда доходит до конца сети — отпечаток (96 чисел).
func (o *OWW) push(i int, row []float32) []float32 {
	for ; i < len(o.convs); i++ {
		c := o.convs[i]
		c.rows = append(c.rows, row)
		if len(c.rows) < c.kh {
			return nil
		}
		if len(c.rows) > c.kh {
			c.rows = c.rows[1:]
		}
		row = c.forward()
		if p, ok := o.pools[i+1]; ok {
			if row = p.forward(row); row == nil {
				return nil
			}
		}
	}
	return row
}

func (c *owwConv) forward() []float32 {
	out := make([]float32, c.wout*c.co)
	pad := c.kw / 2
	span := c.kw * c.ci
	for x := 0; x < c.wout; x++ {
		copy(out[x*c.co:(x+1)*c.co], c.b)
	}
	// середина: окно целиком внутри строки — по четыре положения сразу
	x0, x1 := pad, c.wout-pad
	x := x0
	for ; x+4 <= x1; x += 4 {
		for dh := 0; dh < c.kh; dh++ {
			gemm4(out[x*c.co:], c.co, c.rows[dh][(x-pad)*c.ci:], c.ci, c.w[dh*span*c.co:], span, c.co, c.co)
		}
	}
	// остальное (края с отступом и хвост) — по одному
	for xx := 0; xx < c.wout; xx++ {
		if xx >= x0 && xx < x {
			continue
		}
		lo, hi := xx-pad, xx-pad+c.kw
		skip := 0
		if lo < 0 {
			skip = -lo
		}
		if hi > c.win {
			hi = c.win
		}
		n := (hi - lo - skip) * c.ci
		dst := out[xx*c.co : (xx+1)*c.co]
		for dh := 0; dh < c.kh; dh++ {
			gemv(dst, c.rows[dh][(lo+skip)*c.ci:][:n], c.w[(dh*span+skip*c.ci)*c.co:], c.co)
		}
	}
	if c.act {
		for i, v := range out {
			if v < 0 {
				v *= 0.2
				if v < -0.4 {
					v = -0.4
				}
				out[i] = v
			}
		}
	}
	return out
}

// gemvGo — dst[c] += сумма src[i] * w[i*stride + c] без векторных команд.
func gemvGo(dst, src, w []float32, stride int) {
	for i, v := range src {
		row := w[i*stride:][:len(dst)]
		for c := range dst {
			dst[c] += v * row[c]
		}
	}
}

// dotGo — скалярное произведение без векторных команд (на компьютере и для проверки NEON).
func dotGo(a, b []float32) float32 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}

func (p *owwPool) forward(row []float32) []float32 {
	if p.ph == 2 && p.pending == nil {
		p.pending = row
		return nil
	}
	wout := p.win / p.pw
	out := make([]float32, wout*p.c)
	for x := 0; x < wout; x++ {
		for ch := 0; ch < p.c; ch++ {
			m := float32(math.Inf(-1))
			for dw := 0; dw < p.pw; dw++ {
				m = max(m, row[(x*p.pw+dw)*p.c+ch])
				if p.ph == 2 {
					m = max(m, p.pending[(x*p.pw+dw)*p.c+ch])
				}
			}
			out[x*p.c+ch] = m
		}
	}
	p.pending = nil
	return out
}

// head — новый отпечаток: по последним 16 считаем вероятность «Джарвис».
func (o *OWW) head(e []float32) bool {
	o.ring = append(o.ring, e)
	if len(o.ring) > o.seq {
		o.ring = o.ring[1:]
	}
	if len(o.ring) < o.seq {
		return false
	}
	in := o.emb * o.seq
	h1 := make([]float32, o.h)
	for j := 0; j < o.h; j++ {
		acc := o.b1[j]
		wj := o.w1[j*in : (j+1)*in]
		for t, v := range o.ring {
			acc += dot(v, wj[t*o.emb:(t+1)*o.emb])
		}
		h1[j] = max(acc, 0)
	}
	h2 := make([]float32, o.h)
	for j := 0; j < o.h; j++ {
		acc := o.b2[j] + dot(h1, o.w2[j*o.h:(j+1)*o.h])
		h2[j] = max(acc, 0)
	}
	acc := o.b3[0]
	for q, v := range h2 {
		acc += v * o.w3[q]
	}
	p := float32(1 / (1 + math.Exp(-float64(acc))))
	return o.detectProb(p)
}

func (o *OWW) detectProb(p float32) bool {
	o.LastProb = p
	if o.cooldown > 0 {
		o.cooldown--
		return false
	}
	if o.pending > 0 {
		o.peak = max(o.peak, p)
		o.pending--
		if o.pending == 0 {
			o.LastWakeProb = o.peak
			o.cooldown = 19
			return true
		}
		return false
	}
	s := 5
	if o.sensitivity != nil {
		s = max(1, min(10, o.sensitivity()))
	}
	if p >= o.thresholds[s-1] {
		// Первое пересечение часто бывает 0,4, а через 80–240 мс тот же оклик достигает 0,99.
		// Дожидаемся пика, чтобы не отправлять уверенное имя на медленную повторную проверку.
		o.pending, o.peak = 3, p
	}
	return false
}
