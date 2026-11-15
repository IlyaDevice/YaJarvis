#include "textflag.h"

// func dot16(a, b *float32, n int) float32
// n > 0 и кратно 16. Четыре вектора по 4 числа: 16 умножений-сложений за круг (NEON FMLA).
TEXT ·dot16(SB), NOSPLIT, $0-28
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD n+16(FP), R2
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
loop:
	VLD1.P 64(R0), [V4.S4, V5.S4, V6.S4, V7.S4]
	VLD1.P 64(R1), [V8.S4, V9.S4, V10.S4, V11.S4]
	VFMLA V4.S4, V8.S4, V0.S4
	VFMLA V5.S4, V9.S4, V1.S4
	VFMLA V6.S4, V10.S4, V2.S4
	VFMLA V7.S4, V11.S4, V3.S4
	SUBS $16, R2, R2
	BNE loop
	// сумма 16 чисел из четырёх векторов
	FMOVS ZR, F20
	VMOV V0.S[0], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V0.S[1], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V0.S[2], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V0.S[3], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V1.S[0], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V1.S[1], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V1.S[2], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V1.S[3], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V2.S[0], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V2.S[1], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V2.S[2], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V2.S[3], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V3.S[0], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V3.S[1], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V3.S[2], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	VMOV V3.S[3], R3
	FMOVS R3, F21
	FADDS F21, F20, F20
	FMOVS F20, ret+24(FP)
	RET

// func gemv16(dst, src, w *float32, k, stride int)
// dst[0:16] += сумма по i<k: src[i] * w[i*stride : i*stride+16]. Результат копится в регистрах.
TEXT ·gemv16(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD w+16(FP), R2
	MOVD k+24(FP), R3
	MOVD stride+32(FP), R4
	LSL $2, R4, R4
	VLD1 (R0), [V0.S4, V1.S4, V2.S4, V3.S4]
g16:
	VLD1R.P 4(R1), [V8.S4]
	VLD1 (R2), [V4.S4, V5.S4, V6.S4, V7.S4]
	ADD R4, R2, R2
	VFMLA V4.S4, V8.S4, V0.S4
	VFMLA V5.S4, V8.S4, V1.S4
	VFMLA V6.S4, V8.S4, V2.S4
	VFMLA V7.S4, V8.S4, V3.S4
	SUBS $1, R3, R3
	BNE g16
	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R0)
	RET

// func gemv8(dst, src, w *float32, k, stride int) — то же для 8 выходов
TEXT ·gemv8(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD w+16(FP), R2
	MOVD k+24(FP), R3
	MOVD stride+32(FP), R4
	LSL $2, R4, R4
	VLD1 (R0), [V0.S4, V1.S4]
g8:
	VLD1R.P 4(R1), [V8.S4]
	VLD1 (R2), [V4.S4, V5.S4]
	ADD R4, R2, R2
	VFMLA V4.S4, V8.S4, V0.S4
	VFMLA V5.S4, V8.S4, V1.S4
	SUBS $1, R3, R3
	BNE g8
	VST1 [V0.S4, V1.S4], (R0)
	RET

// func gemm4x16(dst *float32, dstStride int, src *float32, srcStride int, w *float32, k, wstride int)
// Четыре соседних положения j=0..3: dst[j*dstStride + c] += сумма по i<k: src[j*srcStride + i] * w[i*wstride + c],
// c < 16. Веса каждой строки читаются один раз на все четыре положения.
TEXT ·gemm4x16(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD k+40(FP), R5
	MOVD wstride+48(FP), R6
	LSL $2, R1, R1
	LSL $2, R3, R3
	LSL $2, R6, R6
	MOVD R0, R7
	ADD R1, R7, R8
	ADD R1, R8, R9
	ADD R1, R9, R10
	MOVD R2, R11
	ADD R3, R11, R12
	ADD R3, R12, R13
	ADD R3, R13, R14
	VLD1 (R7), [V0.S4, V1.S4, V2.S4, V3.S4]
	VLD1 (R8), [V4.S4, V5.S4, V6.S4, V7.S4]
	VLD1 (R9), [V8.S4, V9.S4, V10.S4, V11.S4]
	VLD1 (R10), [V12.S4, V13.S4, V14.S4, V15.S4]
gemm4x16loop:
	VLD1 (R4), [V16.S4, V17.S4, V18.S4, V19.S4]
	ADD R6, R4, R4
	VLD1R.P 4(R11), [V20.S4]
	VLD1R.P 4(R12), [V21.S4]
	VLD1R.P 4(R13), [V22.S4]
	VLD1R.P 4(R14), [V23.S4]
	VFMLA V16.S4, V20.S4, V0.S4
	VFMLA V17.S4, V20.S4, V1.S4
	VFMLA V18.S4, V20.S4, V2.S4
	VFMLA V19.S4, V20.S4, V3.S4
	VFMLA V16.S4, V21.S4, V4.S4
	VFMLA V17.S4, V21.S4, V5.S4
	VFMLA V18.S4, V21.S4, V6.S4
	VFMLA V19.S4, V21.S4, V7.S4
	VFMLA V16.S4, V22.S4, V8.S4
	VFMLA V17.S4, V22.S4, V9.S4
	VFMLA V18.S4, V22.S4, V10.S4
	VFMLA V19.S4, V22.S4, V11.S4
	VFMLA V16.S4, V23.S4, V12.S4
	VFMLA V17.S4, V23.S4, V13.S4
	VFMLA V18.S4, V23.S4, V14.S4
	VFMLA V19.S4, V23.S4, V15.S4
	SUBS $1, R5, R5
	BNE gemm4x16loop
	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R7)
	VST1 [V4.S4, V5.S4, V6.S4, V7.S4], (R8)
	VST1 [V8.S4, V9.S4, V10.S4, V11.S4], (R9)
	VST1 [V12.S4, V13.S4, V14.S4, V15.S4], (R10)
	RET

// func gemm4x8(dst *float32, dstStride int, src *float32, srcStride int, w *float32, k, wstride int)
// Четыре соседних положения j=0..3: dst[j*dstStride + c] += сумма по i<k: src[j*srcStride + i] * w[i*wstride + c],
// c < 8. Веса каждой строки читаются один раз на все четыре положения.
TEXT ·gemm4x8(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD k+40(FP), R5
	MOVD wstride+48(FP), R6
	LSL $2, R1, R1
	LSL $2, R3, R3
	LSL $2, R6, R6
	MOVD R0, R7
	ADD R1, R7, R8
	ADD R1, R8, R9
	ADD R1, R9, R10
	MOVD R2, R11
	ADD R3, R11, R12
	ADD R3, R12, R13
	ADD R3, R13, R14
	VLD1 (R7), [V0.S4, V1.S4]
	VLD1 (R8), [V2.S4, V3.S4]
	VLD1 (R9), [V4.S4, V5.S4]
	VLD1 (R10), [V6.S4, V7.S4]
gemm4x8loop:
	VLD1 (R4), [V16.S4, V17.S4]
	ADD R6, R4, R4
	VLD1R.P 4(R11), [V20.S4]
	VLD1R.P 4(R12), [V21.S4]
	VLD1R.P 4(R13), [V22.S4]
	VLD1R.P 4(R14), [V23.S4]
	VFMLA V16.S4, V20.S4, V0.S4
	VFMLA V17.S4, V20.S4, V1.S4
	VFMLA V16.S4, V21.S4, V2.S4
	VFMLA V17.S4, V21.S4, V3.S4
	VFMLA V16.S4, V22.S4, V4.S4
	VFMLA V17.S4, V22.S4, V5.S4
	VFMLA V16.S4, V23.S4, V6.S4
	VFMLA V17.S4, V23.S4, V7.S4
	SUBS $1, R5, R5
	BNE gemm4x8loop
	VST1 [V0.S4, V1.S4], (R7)
	VST1 [V2.S4, V3.S4], (R8)
	VST1 [V4.S4, V5.S4], (R9)
	VST1 [V6.S4, V7.S4], (R10)
	RET
