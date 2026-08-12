//go:build amd64 && !noasm

#include "textflag.h"

// func mcCopy8AVX2(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)
//
// The integer-position case of 8.5.3.3.3, which is a widening copy into the
// 14-bit intermediate. w is a multiple of eight.
TEXT ·mcCopy8AVX2(SB), NOSPLIT, $0-56
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ src+16(FP), DX
	MOVQ srcStride+24(FP), CX
	MOVQ w+32(FP), R8
	MOVQ h+40(FP), R9
	MOVQ shift+48(FP), R10

	SHLQ $1, SI
	MOVQ R10, X2

rows:
	MOVQ DI, R11
	MOVQ DX, R12
	MOVQ R8, R13

wide:
	CMPQ R13, $16
	JLT  narrow

	VPMOVZXBW (R12), Y0
	VPSLLW    X2, Y0, Y0
	VMOVDQU   Y0, (R11)

	ADDQ $16, R12
	ADDQ $32, R11
	SUBQ $16, R13
	JMP  wide

narrow:
	TESTQ R13, R13
	JZ    next

	VPMOVZXBW (R12), X0
	VPSLLW    X2, X0, X0
	VMOVDQU   X0, (R11)

	ADDQ $8, R12
	ADDQ $16, R11
	SUBQ $8, R13
	JMP  narrow

next:
	ADDQ SI, DI
	ADDQ CX, DX
	DECQ R9
	JNZ  rows

	VZEROUPPER
	RET

// func mcCopy8AVX512(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)
TEXT ·mcCopy8AVX512(SB), NOSPLIT, $0-56
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ src+16(FP), DX
	MOVQ srcStride+24(FP), CX
	MOVQ w+32(FP), R8
	MOVQ h+40(FP), R9
	MOVQ shift+48(FP), R10

	SHLQ $1, SI
	MOVQ R10, X2

rows512:
	MOVQ DI, R11
	MOVQ DX, R12
	MOVQ R8, R13

wide512:
	CMPQ R13, $32
	JLT  half512

	VPMOVZXBW (R12), Z0
	VPSLLW    X2, Z0, Z0
	VMOVDQU32 Z0, (R11)

	ADDQ $32, R12
	ADDQ $64, R11
	SUBQ $32, R13
	JMP  wide512

half512:
	CMPQ R13, $16
	JLT  narrow512

	VPMOVZXBW (R12), Y0
	VPSLLW    X2, Y0, Y0
	VMOVDQU   Y0, (R11)

	ADDQ $16, R12
	ADDQ $32, R11
	SUBQ $16, R13
	JMP  half512

narrow512:
	TESTQ R13, R13
	JZ    next512

	VPMOVZXBW (R12), X0
	VPSLLW    X2, X0, X0
	VMOVDQU   X0, (R11)

	ADDQ $8, R12
	ADDQ $16, R11
	SUBQ $8, R13
	JMP  narrow512

next512:
	ADDQ SI, DI
	ADDQ CX, DX
	DECQ R9
	JNZ  rows512

	VZEROUPPER
	RET
