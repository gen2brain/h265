//go:build amd64 && !noasm

#include "textflag.h"

// func mcTap8AVX2(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)
//
// 8-tap interpolation of 8.5.3.3.3 for eight-bit samples, in one direction.
// tapStride is one for the horizontal pass and the source stride for the
// vertical one; src already points at the first tap. shift1 is zero at eight
// bits and every partial sum stays inside int16. w is a multiple of eight.
TEXT ·mcTap8AVX2(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ src+16(FP), DX
	MOVQ srcStride+24(FP), CX
	MOVQ tapStride+32(FP), R8
	MOVQ w+40(FP), R9
	MOVQ h+48(FP), R10
	MOVQ f+56(FP), R11

	SHLQ $1, SI

	VPBROADCASTW 0(R11), Y8
	VPBROADCASTW 2(R11), Y9
	VPBROADCASTW 4(R11), Y10
	VPBROADCASTW 6(R11), Y11
	VPBROADCASTW 8(R11), Y12
	VPBROADCASTW 10(R11), Y13
	VPBROADCASTW 12(R11), Y14
	VPBROADCASTW 14(R11), Y15

rows8:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R9, R14

wide8:
	CMPQ R14, $16
	JLT  narrow8

	MOVQ R13, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y8, Y1, Y1
	VMOVDQU   Y1, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y9, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y10, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y11, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y12, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y13, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y14, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y15, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VMOVDQU Y0, (R12)

	ADDQ $16, R13
	ADDQ $32, R12
	SUBQ $16, R14
	JMP  wide8

narrow8:
	TESTQ R14, R14
	JZ    next8

	MOVQ R13, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X8, X1, X1
	VMOVDQU   X1, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X9, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X10, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X11, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X12, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X13, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X14, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X15, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VMOVDQU X0, (R12)

	ADDQ $8, R13
	ADDQ $16, R12
	SUBQ $8, R14
	JMP  narrow8

next8:
	ADDQ SI, DI
	ADDQ CX, DX
	DECQ R10
	JNZ  rows8

	VZEROUPPER
	RET

// func mcTap4AVX2(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)
//
// 4-tap interpolation of 8.5.3.3.3 for eight-bit samples, in one direction.
// tapStride is one for the horizontal pass and the source stride for the
// vertical one; src already points at the first tap. shift1 is zero at eight
// bits and every partial sum stays inside int16. w is a multiple of eight.
TEXT ·mcTap4AVX2(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ src+16(FP), DX
	MOVQ srcStride+24(FP), CX
	MOVQ tapStride+32(FP), R8
	MOVQ w+40(FP), R9
	MOVQ h+48(FP), R10
	MOVQ f+56(FP), R11

	SHLQ $1, SI

	VPBROADCASTW 0(R11), Y8
	VPBROADCASTW 2(R11), Y9
	VPBROADCASTW 4(R11), Y10
	VPBROADCASTW 6(R11), Y11

rows4:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R9, R14

wide4:
	CMPQ R14, $16
	JLT  narrow4

	MOVQ R13, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y8, Y1, Y1
	VMOVDQU   Y1, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y9, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y10, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VPMOVZXBW (R15), Y1
	VPMULLW   Y11, Y1, Y1
	VPADDW    Y1, Y0, Y0
	ADDQ      R8, R15

	VMOVDQU Y0, (R12)

	ADDQ $16, R13
	ADDQ $32, R12
	SUBQ $16, R14
	JMP  wide4

narrow4:
	TESTQ R14, R14
	JZ    next4

	MOVQ R13, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X8, X1, X1
	VMOVDQU   X1, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X9, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X10, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VPMOVZXBW (R15), X1
	VPMULLW   X11, X1, X1
	VPADDW    X1, X0, X0
	ADDQ      R8, R15

	VMOVDQU X0, (R12)

	ADDQ $8, R13
	ADDQ $16, R12
	SUBQ $8, R14
	JMP  narrow4

next4:
	ADDQ SI, DI
	ADDQ CX, DX
	DECQ R10
	JNZ  rows4

	VZEROUPPER
	RET
