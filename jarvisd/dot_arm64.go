package main

//go:noescape
func dot16(a, b *float32, n int) float32

// dot — на колонке (ARM64) основная часть считается векторными командами NEON, хвост — обычным циклом.
func dot(a, b []float32) float32 {
	b = b[:len(a)]
	n := len(a) &^ 15
	var s float32
	if n > 0 {
		s = dot16(&a[0], &b[0], n)
	}
	for i := n; i < len(a); i++ {
		s += a[i] * b[i]
	}
	return s
}

//go:noescape
func gemv16(dst, src, w *float32, k, stride int)

//go:noescape
func gemv8(dst, src, w *float32, k, stride int)

// gemv — dst[c] += сумма src[i] * w[i*stride + c]; len(dst) кратно 8.
func gemv(dst, src, w []float32, stride int) {
	if len(src) == 0 {
		return
	}
	_ = w[(len(src)-1)*stride+len(dst)-1]
	c := 0
	for ; c+16 <= len(dst); c += 16 {
		gemv16(&dst[c], &src[0], &w[c], len(src), stride)
	}
	for ; c+8 <= len(dst); c += 8 {
		gemv8(&dst[c], &src[0], &w[c], len(src), stride)
	}
	if c < len(dst) {
		gemvGo(dst[c:], src, w[c:], stride)
	}
}

//go:noescape
func gemm4x16(dst *float32, dstStride int, src *float32, srcStride int, w *float32, k, wstride int)

//go:noescape
func gemm4x8(dst *float32, dstStride int, src *float32, srcStride int, w *float32, k, wstride int)

// gemm4 — четыре соседних положения сразу: dst[j*ds + c] += сумма src[j*ss + i] * w[i*ws + c], c < co (кратно 8).
func gemm4(dst []float32, ds int, src []float32, ss int, w []float32, k, ws, co int) {
	if k == 0 {
		return
	}
	_ = dst[3*ds+co-1]
	_ = src[3*ss+k-1]
	_ = w[(k-1)*ws+co-1]
	c := 0
	for ; c+16 <= co; c += 16 {
		gemm4x16(&dst[c], ds, &src[0], ss, &w[c], k, ws)
	}
	for ; c+8 <= co; c += 8 {
		gemm4x8(&dst[c], ds, &src[0], ss, &w[c], k, ws)
	}
	if c < co {
		for j := 0; j < 4; j++ {
			gemvGo(dst[j*ds+c:j*ds+co], src[j*ss:j*ss+k], w[c:], ws)
		}
	}
}
