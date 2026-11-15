//go:build !arm64

package main

// dot — скалярное произведение; четыре независимые суммы, чтобы процессор не ждал каждое сложение.
func dot(a, b []float32) float32 {
	return dotGo(a, b)
}

func gemv(dst, src, w []float32, stride int) { gemvGo(dst, src, w, stride) }

func gemm4(dst []float32, ds int, src []float32, ss int, w []float32, k, ws, co int) {
	for j := 0; j < 4; j++ {
		gemvGo(dst[j*ds:j*ds+co], src[j*ss:j*ss+k], w, ws)
	}
}
