//go:build arm64 && !noasm

#include "textflag.h"

#define SQADD8H(Vd, Vn, Vm) WORD $(0x4e600c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL8H(Vd, Vn, Vm)  WORD $(0x4e604400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SQXTUN8B2(Vd, Vn)   WORD $(0x2e212800 | ((Vn) << 5) | (Vd))

// func predUni8NEON(dst *uint8, dstStride int, src *int16, srcStride, w, h, shift int)
//
// 8.5.3.3.4.2 without weights. The add saturates because the Go path sums at
// 32 bits, and a sum that saturates shifts to more than the maximum sample and
// would clip there anyway. w is a multiple of eight.
TEXT ·predUni8NEON(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD w+32(FP), R4
	MOVD h+40(FP), R5
	MOVD shift+48(FP), R6

	LSL $1, R3

	// off = 1 << (shift-1)
	SUB  $1, R6, R7
	MOVD $1, R8
	LSL  R7, R8, R8
	VDUP R8, V1.H8

	NEG  R6, R9
	VDUP R9, V2.H8

rows:
	MOVD R0, R10
	MOVD R2, R11
	MOVD R4, R12

cols:
	VLD1.P 16(R11), [V0.H8]
	SQADD8H(0, 0, 1)
	SSHL8H(0, 0, 2)
	SQXTUN8B2(0, 0)
	VST1.P [V0.B8], 8(R10)

	SUB  $8, R12
	CBNZ R12, cols

	ADD  R1, R0
	ADD  R3, R2
	SUB  $1, R5
	CBNZ R5, rows

	RET
