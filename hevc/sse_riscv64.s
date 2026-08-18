//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func sse8RVV(src *uint8, srcStride int, block *uint8, blockStride, n int) int64
//
// The squared error of an n by n block. n and the vector length are both powers
// of two, so one length serves every chunk.
TEXT ·sse8RVV(SB), NOSPLIT, $0-48
	MOV src+0(FP), X10
	MOV srcStride+8(FP), X11
	MOV block+16(FP), X12
	MOV blockStride+24(FP), X13
	MOV n+32(FP), X14

	VSETVLI X14, E16, M1, TA, MA, X15

	VSETVLI X15, E32, M2, TA, MA, X0
	VMVVI   $0, V8

	MOV $0, X16

rowloop:
	MOV X10, X17
	MOV X12, X18
	MOV $0, X19

colloop:
	VSETVLI X15, E16, M1, TA, MA, X0

	VLE8V    (X17), V1
	VLE8V    (X18), V2
	VZEXTVF2 V1, V3
	VZEXTVF2 V2, V4
	VSUBVV   V4, V3, V3

	VWMACCVV V3, V3, V8

	ADD  X15, X17
	ADD  X15, X18
	ADD  X15, X19
	BLT  X19, X14, colloop

	ADD  X11, X10
	ADD  X13, X12
	ADD  $1, X16
	BLT  X16, X14, rowloop

	VSETVLI   X15, E32, M2, TA, MA, X0
	VMVVI     $0, V12
	VREDSUMVS V12, V8, V14
	VMVXS     V14, X20

	MOV X20, ret+40(FP)
	RET
