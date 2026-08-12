//go:build arm64 && !noasm

#include "textflag.h"

#define SQADD8H_(Vd, Vn, Vm) WORD $(0x4e600c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL8H_(Vd, Vn, Vm)  WORD $(0x4e604400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SQXTUN8B3(Vd, Vn)    WORD $(0x2e212800 | ((Vn) << 5) | (Vd))

// func predBi8NEON(dst *uint8, dstStride int, a, b *int16, srcStride, w, h, shift int)
//
// 8.5.3.3.4.2 for two lists. Both adds saturate, which matches the 32-bit sum
// the Go path takes once the shift and clip are applied.
TEXT ·predBi8NEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD a+16(FP), R2
	MOVD b+24(FP), R3
	MOVD srcStride+32(FP), R4
	MOVD w+40(FP), R5
	MOVD h+48(FP), R6
	MOVD shift+56(FP), R7

	LSL $1, R4

	SUB  $1, R7, R8
	MOVD $1, R9
	LSL  R8, R9, R9
	VDUP R9, V1.H8

	NEG  R7, R10
	VDUP R10, V2.H8

rows:
	MOVD R0, R11
	MOVD R2, R12
	MOVD R3, R13
	MOVD R5, R14

cols:
	VLD1.P 16(R12), [V0.H8]
	VLD1.P 16(R13), [V3.H8]
	SQADD8H_(0, 0, 3)
	SQADD8H_(0, 0, 1)
	SSHL8H_(0, 0, 2)
	SQXTUN8B3(0, 0)
	VST1.P [V0.B8], 8(R11)

	SUB  $8, R14
	CBNZ R14, cols

	ADD  R1, R0
	ADD  R4, R2
	ADD  R4, R3
	SUB  $1, R6
	CBNZ R6, rows

	RET
