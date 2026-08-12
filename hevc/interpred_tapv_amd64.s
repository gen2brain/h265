//go:build amd64 && !noasm

#include "textflag.h"

// func mcLumaTapV16AVX2(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int32)
//
// The vertical half of the two-pass eight-tap interpolation, reading the
// horizontal pass at sixteen bits. The products need 32 bits, so this widens
// rather than staying in int16 as the first pass does. w is a multiple of
// eight.
TEXT ·mcLumaTapV16AVX2(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ src+16(FP), DX
	MOVQ srcStride+24(FP), R8
	MOVQ w+32(FP), R9
	MOVQ h+40(FP), R10
	MOVQ shift+48(FP), R14
	MOVQ f+56(FP), R11

	SHLQ $1, SI
	SHLQ $1, R8
	MOVQ R14, X2

	VPBROADCASTD 0(R11), Y8
	VPBROADCASTD 4(R11), Y9
	VPBROADCASTD 8(R11), Y10
	VPBROADCASTD 12(R11), Y11
	VPBROADCASTD 16(R11), Y12
	VPBROADCASTD 20(R11), Y13
	VPBROADCASTD 24(R11), Y14
	VPBROADCASTD 28(R11), Y15

rows:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R9, R14

cols:
	MOVQ R13, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y8, Y1, Y1
	VMOVDQU   Y1, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y9, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y10, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y11, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y12, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y13, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y14, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVSXWD (R15), Y1
	VPMULLD   Y15, Y1, Y1
	VPADDD    Y1, Y0, Y0
	ADDQ      R8, R15

	VPSRAD       X2, Y0, Y0
	VPACKSSDW    Y0, Y0, Y0
	VPERMQ       $0xd8, Y0, Y0
	VMOVDQU      X0, (R12)

	ADDQ $16, R13
	ADDQ $16, R12
	SUBQ $8, R14
	JNZ  cols

	ADDQ SI, DI
	ADDQ R8, DX
	DECQ R10
	JNZ  rows

	VZEROUPPER
	RET
