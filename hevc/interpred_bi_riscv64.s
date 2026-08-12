//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func predBi8RVV(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)
//
// 8.5.3.3.4.2 for two lists, summed at 32 bits as the Go path does, then
// clamped and narrowed.
TEXT ·predBi8RVV(SB), NOSPLIT, $0-64
	MOV dst+0(FP), X10
	MOV dstStride+8(FP), X11
	MOV a+16(FP), X12
	MOV b+24(FP), X13
	MOV srcStride+32(FP), X14
	MOV w+40(FP), X15
	MOV h+48(FP), X16
	MOV shift+56(FP), X17

	SLLI $1, X14

	ADD $-1, X17, X18
	MOV $1, X19
	SLL X18, X19, X19

	MOV $255, X20

rows:
	MOV X10, X21
	MOV X12, X22
	MOV X13, X23
	MOV X15, X24

cols:
	VSETVLI X24, E16, MF2, TA, MA, X25

	VLE16V (X22), V1
	VLE16V (X23), V2

	VSETVLI  X24, E32, M1, TA, MA, X25
	VSEXTVF2 V1, V3
	VSEXTVF2 V2, V4
	VADDVV   V4, V3, V3
	VADDVX   X19, V3, V3
	VSRAVX   X17, V3, V3
	VMAXVX   X0, V3, V3
	VMINVX   X20, V3, V3

	VSETVLI X24, E16, MF2, TA, MA, X25
	VNSRLWI $0, V3, V5
	VSETVLI X24, E8, MF4, TA, MA, X25
	VNSRLWI $0, V5, V6
	VSE8V   V6, (X21)

	SLLI $1, X25, X26
	ADD  X26, X22
	ADD  X26, X23
	ADD  X25, X21
	SUB  X25, X24
	BNEZ X24, cols

	ADD  X11, X10
	ADD  X14, X12
	ADD  X14, X13
	ADD  $-1, X16
	BNEZ X16, rows

	RET
