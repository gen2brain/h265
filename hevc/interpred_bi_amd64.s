//go:build amd64 && !noasm

#include "textflag.h"

// func predBi8AVX2(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)
//
// 8.5.3.3.4.2 for two lists. Both adds saturate: the Go path sums at 32 bits,
// and a sum that saturates shifts to more than the maximum sample and would
// clip there anyway. w is a multiple of eight.
TEXT ·predBi8AVX2(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ a+16(FP), DX
	MOVQ b+24(FP), R11
	MOVQ w+40(FP), R8
	MOVQ h+48(FP), R9
	MOVQ shift+56(FP), R10

	// off = 1 << (shift-1), while CL is still free
	MOVQ $1, AX
	MOVQ R10, CX
	DECQ CX
	SHLQ CX, AX
	MOVQ AX, X1

	VPBROADCASTW X1, Y1
	MOVQ         R10, X2

	MOVQ srcStride+32(FP), CX
	SHLQ $1, CX

rows:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R11, R14
	MOVQ R8, R15

wide:
	CMPQ R15, $16
	JLT  narrow

	VMOVDQU   (R13), Y0
	VPADDSW   (R14), Y0, Y0
	VPADDSW   Y1, Y0, Y0
	VPSRAW    X2, Y0, Y0
	VPACKUSWB Y0, Y0, Y0
	VPERMQ    $0xd8, Y0, Y0
	VMOVDQU   X0, (R12)

	ADDQ $32, R13
	ADDQ $32, R14
	ADDQ $16, R12
	SUBQ $16, R15
	JMP  wide

narrow:
	TESTQ R15, R15
	JZ    next

	VMOVDQU   (R13), X0
	VPADDSW   (R14), X0, X0
	VPADDSW   X1, X0, X0
	VPSRAW    X2, X0, X0
	VPACKUSWB X0, X0, X0
	MOVQ      X0, (R12)

	ADDQ $16, R13
	ADDQ $16, R14
	ADDQ $8, R12
	SUBQ $8, R15
	JMP  narrow

next:
	ADDQ SI, DI
	ADDQ CX, DX
	ADDQ CX, R11
	DECQ R9
	JNZ  rows

	VZEROUPPER
	RET

// func predBi8AVX512(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)
TEXT ·predBi8AVX512(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ a+16(FP), DX
	MOVQ b+24(FP), R11
	MOVQ w+40(FP), R8
	MOVQ h+48(FP), R9
	MOVQ shift+56(FP), R10

	MOVQ $1, AX
	MOVQ R10, CX
	DECQ CX
	SHLQ CX, AX
	MOVQ AX, X1

	VPBROADCASTW X1, Z1
	MOVQ         R10, X2
	VPXORD       Z3, Z3, Z3

	MOVQ srcStride+32(FP), CX
	SHLQ $1, CX

rows512:
	MOVQ DI, R12
	MOVQ DX, R13
	MOVQ R11, R14
	MOVQ R8, R15

wide512:
	CMPQ R15, $32
	JLT  half512

	VMOVDQU32 (R13), Z0
	VPADDSW   (R14), Z0, Z0
	VPADDSW   Z1, Z0, Z0
	VPSRAW    X2, Z0, Z0
	VPMAXSW   Z3, Z0, Z0
	VPMOVUSWB Z0, (R12)

	ADDQ $64, R13
	ADDQ $64, R14
	ADDQ $32, R12
	SUBQ $32, R15
	JMP  wide512

half512:
	CMPQ R15, $16
	JLT  narrow512

	VMOVDQU   (R13), Y0
	VPADDSW   (R14), Y0, Y0
	VPADDSW   Y1, Y0, Y0
	VPSRAW    X2, Y0, Y0
	VPACKUSWB Y0, Y0, Y0
	VPERMQ    $0xd8, Y0, Y0
	VMOVDQU   X0, (R12)

	ADDQ $32, R13
	ADDQ $32, R14
	ADDQ $16, R12
	SUBQ $16, R15
	JMP  half512

narrow512:
	TESTQ R15, R15
	JZ    next512

	VMOVDQU   (R13), X0
	VPADDSW   (R14), X0, X0
	VPADDSW   X1, X0, X0
	VPSRAW    X2, X0, X0
	VPACKUSWB X0, X0, X0
	MOVQ      X0, (R12)

	ADDQ $16, R13
	ADDQ $16, R14
	ADDQ $8, R12
	SUBQ $8, R15
	JMP  narrow512

next512:
	ADDQ SI, DI
	ADDQ CX, DX
	ADDQ CX, R11
	DECQ R9
	JNZ  rows512

	VZEROUPPER
	RET
