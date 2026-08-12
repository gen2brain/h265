//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func mcTapV16RVV(dst *int16, dstStride int, src *int16, srcStride, w, h, shift, taps int, f *int16)
//
// The vertical half of the two-pass eight-tap interpolation, reading the
// horizontal pass at sixteen bits. The products need 32 bits, so this widens
// rather than staying in int16 as the first pass does.
TEXT ·mcTapV16RVV(SB), NOSPLIT, $0-72
	MOV dst+0(FP), X10
	MOV dstStride+8(FP), X11
	MOV src+16(FP), X12
	MOV srcStride+24(FP), X13
	MOV w+32(FP), X14
	MOV h+40(FP), X15
	MOV shift+48(FP), X16
	MOV taps+56(FP), X28
	MOV f+64(FP), X17

	SLLI $1, X11
	SLLI $1, X13

rows:
	MOV X10, X18
	MOV X12, X19
	MOV X14, X20

cols:
	VSETVLI X20, E32, M1, TA, MA, X21

	MOV  X19, X22
	MOV  X17, X23
	MOV  X28, X24

	VMVVI $0, V4

taps:
	VSETVLI  X20, E16, MF2, TA, MA, X21
	VLE16V   (X22), V1
	VSETVLI  X20, E32, M1, TA, MA, X21
	VSEXTVF2 V1, V2
	MOVH     (X23), X25
	VMACCVX  V2, X25, V4

	ADD  X13, X22
	ADD  $2, X23
	ADD  $-1, X24
	BNEZ X24, taps

	VSRAVX X16, V4, V4

	VSETVLI X20, E16, MF2, TA, MA, X21
	VNSRLWI $0, V4, V5
	VSE16V  V5, (X18)
	VSETVLI X20, E32, M1, TA, MA, X21

	SLLI $1, X21, X26
	ADD  X26, X18
	ADD  X26, X19
	SUB  X21, X20
	BNEZ X20, cols

	ADD  X11, X10
	ADD  X13, X12
	ADD  $-1, X15
	BNEZ X15, rows

	RET
