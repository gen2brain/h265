//go:build amd64 && !noasm

#include "textflag.h"

// func dequant32AVX2(coef *int32, m *uint8, n int, ls, rnd, sh, lo, hi int32)
//
// 8.6.3 once the left shift has been folded into the right one, so the product
// fits 32 bits. m may be nil, which means a flat matrix of sixteen. n is the
// coefficient count, always a multiple of eight.
TEXT ·dequant32AVX2(SB), NOSPLIT, $0-44
	MOVQ coef+0(FP), DI
	MOVQ m+8(FP), SI
	MOVQ n+16(FP), CX

	MOVL         ls+24(FP), AX
	MOVQ         AX, X1
	VPBROADCASTD X1, Y1
	MOVL         rnd+28(FP), AX
	MOVQ         AX, X2
	VPBROADCASTD X2, Y2
	MOVL         lo+36(FP), AX
	MOVQ         AX, X4
	VPBROADCASTD X4, Y4
	MOVL         hi+40(FP), AX
	MOVQ         AX, X5
	VPBROADCASTD X5, Y5

	MOVL sh+32(FP), AX
	MOVQ AX, X3

	MOVL         $16, AX
	MOVQ         AX, X6
	VPBROADCASTD X6, Y6

	TESTQ SI, SI
	JZ    flat

scaled:
	VMOVDQU   (DI), Y0
	VPMOVZXBD (SI), Y7
	VPMULLD   Y7, Y0, Y0
	VPMULLD   Y1, Y0, Y0
	VPADDD    Y2, Y0, Y0
	VPSRAD    X3, Y0, Y0
	VPMAXSD   Y4, Y0, Y0
	VPMINSD   Y5, Y0, Y0
	VMOVDQU   Y0, (DI)

	ADDQ $32, DI
	ADDQ $8, SI
	SUBQ $8, CX
	JNZ  scaled

	VZEROUPPER
	RET

flat:
	VMOVDQU (DI), Y0
	VPMULLD Y6, Y0, Y0
	VPMULLD Y1, Y0, Y0
	VPADDD  Y2, Y0, Y0
	VPSRAD  X3, Y0, Y0
	VPMAXSD Y4, Y0, Y0
	VPMINSD Y5, Y0, Y0
	VMOVDQU Y0, (DI)

	ADDQ $32, DI
	SUBQ $8, CX
	JNZ  flat

	VZEROUPPER
	RET
