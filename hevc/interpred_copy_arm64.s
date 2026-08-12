//go:build arm64 && !noasm

#include "textflag.h"

#define USHL8H(Vd, Vn, Vm) WORD $(0x6e604400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func mcCopy8NEON(dst *int16, dstStride int, src *uint8, srcStride, w, h, shift int)
//
// The integer-position case of 8.5.3.3.3, a widening copy into the 14-bit
// intermediate. w is a multiple of eight.
TEXT ·mcCopy8NEON(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
	MOVD shift+48(FP), R6

	LSL  $1, R1
	VDUP R6, V1.H8

rows:
	MOVD R0, R7
	MOVD R2, R8
	MOVD R4, R9

cols:
	VLD1.P 8(R8), [V0.B8]
	VUXTL  V0.B8, V0.H8
	USHL8H(0, 0, 1)
	VST1.P [V0.H8], 16(R7)

	SUB  $8, R9
	CBNZ R9, cols

	ADD  R1, R0
	ADD  R3, R2
	SUB  $1, R5
	CBNZ R5, rows

	RET
