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

// func odd16AVX2(out *int32, in *int32, m *int8, stride int)
//
// out[i] = sum over j of m[(2j+1)*stride][i] * in[2j+1], sixteen wide. The
// accumulators stay in registers, and a zero coefficient skips a row.
TEXT ·odd16AVX2(SB), NOSPLIT, $0-32
	MOVQ out+0(FP), DI
	MOVQ in+8(FP), SI
	MOVQ m+16(FP), DX
	MOVQ stride+24(FP), R8

	LEAQ 4(SI), R10

	// The first basis row is stride rows in, and each step advances by two.
	MOVQ R8, R11
	SHLQ $5, R11
	ADDQ R11, DX
	SHLQ $1, R11

	VPXOR Y0, Y0, Y0
	VPXOR Y1, Y1, Y1

	MOVQ $16, R12

loop:
	MOVL  (R10), AX
	TESTL AX, AX
	JZ    skip

	VPBROADCASTD (R10), Y4
	VPMOVSXBD    (DX), Y2
	VPMOVSXBD    8(DX), Y3
	VPMULLD      Y4, Y2, Y2
	VPMULLD      Y4, Y3, Y3
	VPADDD       Y2, Y0, Y0
	VPADDD       Y3, Y1, Y1

skip:
	ADDQ $8, R10
	ADDQ R11, DX
	DECQ R12
	JNZ  loop

	VMOVDQU Y0, (DI)
	VMOVDQU Y1, 32(DI)
	VZEROUPPER
	RET
