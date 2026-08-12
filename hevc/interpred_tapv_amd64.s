//go:build amd64 && !noasm

#include "textflag.h"

// func mcTapV16x8AVX2(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)
//
// The vertical half of the two-pass 8-tap interpolation, reading the
// horizontal pass at sixteen bits. The products need 32 bits, so this widens
// rather than staying in int16 as the first pass does. w is a multiple of
// eight.
TEXT ·mcTapV16x8AVX2(SB), NOSPLIT, $0-64
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

	MOVWQSX 0(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y8
	MOVWQSX 2(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y9
	MOVWQSX 4(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y10
	MOVWQSX 6(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y11
	MOVWQSX 8(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y12
	MOVWQSX 10(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y13
	MOVWQSX 12(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y14
	MOVWQSX 14(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y15

rows8:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R9, R14

cols8:
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

	VPSRAD    X2, Y0, Y0
	VPACKSSDW Y0, Y0, Y0
	VPERMQ    $0xd8, Y0, Y0
	VMOVDQU   X0, (R12)

	ADDQ $16, R13
	ADDQ $16, R12
	SUBQ $8, R14
	JNZ  cols8

	ADDQ SI, DI
	ADDQ R8, DX
	DECQ R10
	JNZ  rows8

	VZEROUPPER
	RET

// func mcTapV16x4AVX2(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)
//
// The vertical half of the two-pass 4-tap interpolation, reading the
// horizontal pass at sixteen bits. The products need 32 bits, so this widens
// rather than staying in int16 as the first pass does. w is a multiple of
// eight.
TEXT ·mcTapV16x4AVX2(SB), NOSPLIT, $0-64
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

	MOVWQSX 0(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y8
	MOVWQSX 2(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y9
	MOVWQSX 4(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y10
	MOVWQSX 6(R11), AX
	MOVQ    AX, X3
	VPBROADCASTD X3, Y11

rows4:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R9, R14

cols4:
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

	VPSRAD    X2, Y0, Y0
	VPACKSSDW Y0, Y0, Y0
	VPERMQ    $0xd8, Y0, Y0
	VMOVDQU   X0, (R12)

	ADDQ $16, R13
	ADDQ $16, R12
	SUBQ $8, R14
	JNZ  cols4

	ADDQ SI, DI
	ADDQ R8, DX
	DECQ R10
	JNZ  rows4

	VZEROUPPER
	RET
