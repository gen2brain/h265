//go:build amd64 && !noasm

#include "textflag.h"

// func sse8AVX2(src *uint8, srcStride int, block *uint8, blockStride, n int) int64
//
// The squared error of an n by n block, the pairwise multiply squaring and
// adding in one step.
TEXT ·sse8AVX2(SB), NOSPLIT, $0-48
	MOVQ src+0(FP), SI
	MOVQ srcStride+8(FP), R8
	MOVQ block+16(FP), DI
	MOVQ blockStride+24(FP), R9
	MOVQ n+32(FP), CX

	VPXOR Y0, Y0, Y0

	XORQ BX, BX

rowloop:
	XORQ DX, DX

colloop:
	MOVQ CX, AX
	SUBQ DX, AX
	CMPQ AX, $16
	JLT  half

	VPMOVZXBW (SI)(DX*1), Y1
	VPMOVZXBW (DI)(DX*1), Y2
	VPSUBW    Y2, Y1, Y1
	VPMADDWD  Y1, Y1, Y1
	VPADDD    Y1, Y0, Y0

	ADDQ $16, DX
	JMP  next

half:
	VPMOVZXBW (SI)(DX*1), X1
	VPMOVZXBW (DI)(DX*1), X2
	VPSUBW    X2, X1, X1
	VPMADDWD  X1, X1, X1
	VPADDD    Y1, Y0, Y0

	ADDQ $8, DX

next:
	CMPQ DX, CX
	JLT  colloop

	ADDQ R8, SI
	ADDQ R9, DI
	INCQ BX
	CMPQ BX, CX
	JLT  rowloop

	VEXTRACTI128 $1, Y0, X1
	VPADDD       X1, X0, X0
	VPSHUFD      $0x4e, X0, X1
	VPADDD       X1, X0, X0
	VPSHUFD      $0xb1, X0, X1
	VPADDD       X1, X0, X0

	VMOVD   X0, AX
	MOVLQZX AX, AX
	MOVQ    AX, ret+40(FP)

	VZEROUPPER
	RET
