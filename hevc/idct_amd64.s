//go:build amd64 && !noasm

#include "textflag.h"

// func idctCols8AVX2(dst, src, m *int32, n, mstride, shift int, rnd, lo, hi int32)
//
// One pass of 8.6.4.2 over eight columns at a time. Outputs are produced four
// at a time so the even and odd accumulators for the tile stay in registers,
// and j is unrolled by two so the parity is static.
TEXT ·idctCols8AVX2(SB), NOSPLIT, $0-60
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ m+16(FP), R14
	MOVQ n+24(FP), R8
	MOVQ mstride+32(FP), R10
	MOVQ shift+40(FP), CX

	SHLQ $2, R10

	MOVL         rnd+48(FP), AX
	MOVQ         AX, X10
	VPBROADCASTD X10, Y10
	MOVL         lo+52(FP), AX
	MOVQ         AX, X11
	VPBROADCASTD X11, Y11
	MOVL         hi+56(FP), AX
	MOVQ         AX, X12
	VPBROADCASTD X12, Y12
	MOVQ         CX, X13

	MOVQ R8, R9
	SHRQ $1, R9

	MOVQ R8, R15
	SHLQ $2, R15

	XORQ BX, BX

xloop:
	XORQ R11, R11

iloop:
	VPXOR Y0, Y0, Y0
	VPXOR Y1, Y1, Y1
	VPXOR Y2, Y2, Y2
	VPXOR Y3, Y3, Y3
	VPXOR Y4, Y4, Y4
	VPXOR Y5, Y5, Y5
	VPXOR Y6, Y6, Y6
	VPXOR Y7, Y7, Y7

	LEAQ (SI)(BX*4), DX
	LEAQ (R14)(R11*4), AX

	MOVQ R8, R12

jloop:
	VMOVDQU (DX), Y8
	VPTEST  Y8, Y8
	JZ      skipeven

	VPBROADCASTD (AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y0, Y0
	VPBROADCASTD 4(AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y1, Y1
	VPBROADCASTD 8(AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y2, Y2
	VPBROADCASTD 12(AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y3, Y3

skipeven:
	ADDQ R15, DX
	ADDQ R10, AX

	VMOVDQU (DX), Y8
	VPTEST  Y8, Y8
	JZ      skipodd

	VPBROADCASTD (AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y4, Y4
	VPBROADCASTD 4(AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y5, Y5
	VPBROADCASTD 8(AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y6, Y6
	VPBROADCASTD 12(AX), Y9
	VPMULLD      Y8, Y9, Y9
	VPADDD       Y9, Y7, Y7

skipodd:
	ADDQ R15, DX
	ADDQ R10, AX

	SUBQ $2, R12
	JNZ  jloop

	// The low output rows run forward from i0, the high ones backward from
	// n-1-i0, which is the M[j][n-1-i] symmetry.
	MOVQ  R11, AX
	IMULQ R15, AX
	LEAQ  (DI)(AX*1), R13
	LEAQ  (R13)(BX*4), R13

	MOVQ  R8, AX
	DECQ  AX
	SUBQ  R11, AX
	IMULQ R15, AX
	LEAQ  (DI)(AX*1), R12
	LEAQ  (R12)(BX*4), R12

#define EMIT(Ye, Yo)         \
	VPADDD  Yo, Ye, Y8   \
	VPSUBD  Yo, Ye, Y9   \
	VPADDD  Y10, Y8, Y8  \
	VPADDD  Y10, Y9, Y9  \
	VPSRAD  X13, Y8, Y8  \
	VPSRAD  X13, Y9, Y9  \
	VPMAXSD Y11, Y8, Y8  \
	VPMINSD Y12, Y8, Y8  \
	VPMAXSD Y11, Y9, Y9  \
	VPMINSD Y12, Y9, Y9  \
	VMOVDQU Y8, (R13)    \
	VMOVDQU Y9, (R12)    \
	ADDQ    R15, R13     \
	SUBQ    R15, R12

	EMIT(Y0, Y4)
	EMIT(Y1, Y5)
	EMIT(Y2, Y6)
	EMIT(Y3, Y7)

	ADDQ $4, R11
	CMPQ R11, R9
	JLT  iloop

	ADDQ $8, BX
	CMPQ BX, R8
	JLT  xloop

	VZEROUPPER
	RET
