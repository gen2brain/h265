//go:build amd64 && !noasm

#include "textflag.h"

// func predUni8AVX2(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)
//
// 8.5.3.3.4.2 without weights. The add saturates because the Go path sums at
// 32 bits, and a sum that saturates shifts to more than the maximum sample and
// would clip there anyway. The pack then saturates unsigned, which is the clip
// itself. w is a multiple of eight; the caller does any remainder.
TEXT ·predUni8AVX2(SB), NOSPLIT, $0-56
	MOVQ dst+0(FP), DI
	MOVQ dstStride+8(FP), SI
	MOVQ src+16(FP), DX
	MOVQ w+32(FP), R8
	MOVQ h+40(FP), R9
	MOVQ shift+48(FP), R10

	// off = 1 << (shift-1), while CL is still free
	MOVQ $1, AX
	MOVQ R10, CX
	DECQ CX
	SHLQ CX, AX
	MOVQ AX, X1

	VPBROADCASTW X1, Y1
	MOVQ         R10, X2

	MOVQ srcStride+24(FP), CX
	SHLQ $1, CX

rows:
	MOVQ DI, R11
	MOVQ DX, R12
	MOVQ R8, R13

wide:
	CMPQ R13, $16
	JLT  narrow

	VMOVDQU   (R12), Y0
	VPADDSW   Y1, Y0, Y0
	VPSRAW    X2, Y0, Y0
	VPACKUSWB Y0, Y0, Y0
	VPERMQ    $0xd8, Y0, Y0
	VMOVDQU   X0, (R11)

	ADDQ $32, R12
	ADDQ $16, R11
	SUBQ $16, R13
	JMP  wide

narrow:
	TESTQ R13, R13
	JZ    next

	VMOVDQU   (R12), X0
	VPADDSW   X1, X0, X0
	VPSRAW    X2, X0, X0
	VPACKUSWB X0, X0, X0
	MOVQ      X0, (R11)

	ADDQ $16, R12
	ADDQ $8, R11
	SUBQ $8, R13
	JMP  narrow

next:
	ADDQ SI, DI
	ADDQ CX, DX
	DECQ R9
	JNZ  rows

	VZEROUPPER
	RET
