package main

// Голос моделей приходит в 16 или 24 кГц, а dmix колонки работает на 48 кГц. Если отдать aplay 24 кГц,
// пересчёт делает ALSA plug, а быстрого конвертера в конфиге колонки нет — работает линейный: верха
// «звенят» зеркальными призвуками, голос звучит как из старого динамика. Поэтому поднимаем частоту сами:
// вставка нулей и полифазный FIR-фильтр нижних частот (окно Блэкмана).

import "math"

// OutRate — частота, которую плеер всегда отдаёт в ALSA.
const OutRate = 48000

const upTaps = 24 // коэффициентов на фазу: подавление зеркал около −70 дБ, ~1,2 млн умножений в секунду

type upsampler struct {
	l     int
	phase [][]float64 // phase[p][j] — коэффициент для x[m-j] в выходном отсчёте m*l+p
	buf   []float64   // upTaps-1 прошлых отсчётов + текущий кусок
}

// newUpsampler — подъём частоты в l раз (l ≥ 2).
func newUpsampler(l int) *upsampler {
	n := l * upTaps
	h := make([]float64, n)
	fc := 0.5 / float64(l) * 0.9 // срез чуть ниже прежней частоты Найквиста, в долях выходной частоты
	mid := float64(n-1) / 2
	for k := range h {
		t := float64(k) - mid
		s := 2 * fc
		if t != 0 {
			s = math.Sin(2*math.Pi*fc*t) / (math.Pi * t)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(k)/float64(n-1)) + 0.08*math.Cos(4*math.Pi*float64(k)/float64(n-1))
		h[k] = s * w
	}
	u := &upsampler{l: l, phase: make([][]float64, l), buf: make([]float64, upTaps-1)}
	for p := range l {
		u.phase[p] = make([]float64, upTaps)
		sum := 0.0
		for j := range upTaps {
			u.phase[p][j] = h[p+j*l]
			sum += h[p+j*l]
		}
		for j := range upTaps { // каждая фаза с единичным усилением: постоянная составляющая без ряби
			u.phase[p][j] /= sum
		}
	}
	return u
}

// run — поднять кусок; состояние фильтра переходит между кусками, стыков не слышно.
func (u *upsampler) run(in []int16) []int16 {
	hist := upTaps - 1
	u.buf = u.buf[:hist]
	for _, v := range in {
		u.buf = append(u.buf, float64(v))
	}
	out := make([]int16, 0, len(in)*u.l)
	for i := range in {
		c := hist + i
		for p := range u.l {
			acc := 0.0
			for j, k := range u.phase[p] {
				acc += k * u.buf[c-j]
			}
			out = append(out, int16(max(-32767, min(32767, math.Round(acc)))))
		}
	}
	copy(u.buf, u.buf[len(in):])
	u.buf = u.buf[:hist]
	return out
}
