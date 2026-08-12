//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func mcTap8RVV(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h, taps int, f *int16)
//
// The eight-tap interpolation of 8.5.3.3.3.1 for eight-bit samples, in one
// direction. tapStride is one for the horizontal pass and the source stride
// for the vertical one; src already points at the first tap. shift1 is zero at
// eight bits and every partial sum stays inside int16.
TEXT ·mcTap8RVV(SB), NOSPLIT, $0-72
	MOV dst+0(FP), X10
	MOV dstStride+8(FP), X11
	MOV src+16(FP), X12
	MOV srcStride+24(FP), X13
	MOV tapStride+32(FP), X14
	MOV w+40(FP), X15
	MOV h+48(FP), X16
	MOV taps+56(FP), X28
	MOV f+64(FP), X17

	SLLI $1, X11

rows:
	MOV X10, X18
	MOV X12, X19
	MOV X15, X20

cols:
	VSETVLI X20, E16, M1, TA, MA, X21

	MOV  X19, X22
	MOV  X17, X23
	MOV  X28, X24

	VMVVI $0, V4

taps:
	VLE8V    (X22), V1
	VZEXTVF2 V1, V2
	MOVH     (X23), X25
	VMACCVX  V2, X25, V4

	ADD  X14, X22
	ADD  $2, X23
	ADD  $-1, X24
	BNEZ X24, taps

	VSE16V V4, (X18)

	SLLI $1, X21, X26
	ADD  X26, X18
	ADD  X21, X19
	SUB  X21, X20
	BNEZ X20, cols

	ADD  X11, X10
	ADD  X13, X12
	ADD  $-1, X16
	BNEZ X16, rows

	RET
