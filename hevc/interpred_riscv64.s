//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func predUni8RVV(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)
//
// 8.5.3.3.4.2 without weights. The sum is taken at 32 bits, as the Go path
// does, then clamped and narrowed. The narrowing shift is logical, so the
// clamp has to come first: the unsigned saturating form would turn a negative
// sample into the maximum rather than zero.
TEXT ·predUni8RVV(SB), NOSPLIT, $0-56
	MOV dst+0(FP), X10
	MOV dstStride+8(FP), X11
	MOV src+16(FP), X12
	MOV srcStride+24(FP), X13
	MOV w+32(FP), X14
	MOV h+40(FP), X15
	MOV shift+48(FP), X16

	SLLI $1, X13

	// off = 1 << (shift-1)
	ADD $-1, X16, X17
	MOV $1, X18
	SLL X17, X18, X18

	MOV $255, X24

rows:
	MOV X10, X19
	MOV X12, X20
	MOV X14, X21

cols:
	VSETVLI X21, E16, MF2, TA, MA, X22

	VLE16V   (X20), V1
	VSETVLI  X21, E32, M1, TA, MA, X22
	VSEXTVF2 V1, V2
	VADDVX   X18, V2, V2
	VSRAVX   X16, V2, V2

	VMAXVX X0, V2, V2
	VMINVX X24, V2, V2

	VSETVLI X21, E16, MF2, TA, MA, X22
	VNSRLWI $0, V2, V3
	VSETVLI X21, E8, MF4, TA, MA, X22
	VNSRLWI $0, V3, V4
	VSE8V   V4, (X19)

	SLLI $1, X22, X23
	ADD  X23, X20
	ADD  X22, X19
	SUB  X22, X21
	BNEZ X21, cols

	ADD  X11, X10
	ADD  X13, X12
	ADD  $-1, X15
	BNEZ X15, rows

	RET
