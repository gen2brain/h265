//go:build amd64 && !noasm

#include "textflag.h"

// Selects byte 0 of each dword within a 128-bit lane, so eight clamped int32
// become eight bytes in two groups of four.
DATA packLow<>+0x00(SB)/8, $0x808080800c080400
DATA packLow<>+0x08(SB)/8, $0x8080808080808080
DATA packLow<>+0x10(SB)/8, $0x808080800c080400
DATA packLow<>+0x18(SB)/8, $0x8080808080808080
GLOBL packLow<>(SB), RODATA|NOPTR, $32

// func addResidual8AVX2(dst *uint8, stride int, coef *int32, n, shift int)
TEXT ·addResidual8AVX2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ stride+8(FP), SI
	MOVQ coef+16(FP), DX
	MOVQ n+24(FP), BX
	MOVQ shift+32(FP), R8

	// rnd is 1<<(shift-1), and zero when nothing is shifted.
	XORQ R9, R9
	TESTQ R8, R8
	JLE  havernd
	MOVQ R8, CX
	DECQ CX
	MOVQ $1, R9
	SHLQ CX, R9

havernd:
	MOVQ         R9, X0
	VPBROADCASTD X0, Y0
	MOVQ         R8, X1
	VPXOR        Y2, Y2, Y2
	MOVL         $255, AX
	MOVQ         AX, X3
	VPBROADCASTD X3, Y3
	VMOVDQU      packLow<>(SB), Y6

	MOVQ BX, R10

rows:
	MOVQ DI, R12
	MOVQ BX, R11

cols:
	VMOVDQU   (DX), Y4
	VPADDD    Y0, Y4, Y4
	VPSRAD    X1, Y4, Y4
	VPMOVZXBD (R12), Y5
	VPADDD    Y5, Y4, Y4
	VPMAXSD   Y2, Y4, Y4
	VPMINSD   Y3, Y4, Y4

	VPSHUFB      Y6, Y4, Y4
	VEXTRACTI128 $1, Y4, X7
	MOVL         X4, (R12)
	MOVL         X7, 4(R12)

	ADDQ $32, DX
	ADDQ $8, R12
	SUBQ $8, R11
	JNZ  cols

	ADDQ SI, DI
	DECQ R10
	JNZ  rows

	VZEROUPPER
	RET
