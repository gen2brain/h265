//go:build arm64 && !noasm

#include "textflag.h"

#define MUL8H(Vd, Vn, Vm) WORD $(0x4e609c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define MLA8H(Vd, Vn, Vm) WORD $(0x4e609400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func mcTap8NEON(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)
//
// 8-tap interpolation of 8.5.3.3.3 for eight-bit samples, in one direction.
TEXT ·mcTap8NEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD tapStride+32(FP), R8
	MOVD w+40(FP), R9
	MOVD h+48(FP), R10
	MOVD f+56(FP), R11

	LSL $1, R1

	VLD1R.P 2(R11), [V16.H8]
	VLD1R.P 2(R11), [V17.H8]
	VLD1R.P 2(R11), [V18.H8]
	VLD1R.P 2(R11), [V19.H8]
	VLD1R.P 2(R11), [V20.H8]
	VLD1R.P 2(R11), [V21.H8]
	VLD1R.P 2(R11), [V22.H8]
	VLD1R.P 2(R11), [V23.H8]

rows8:
	MOVD R0, R12
	MOVD R2, R13
	MOVD R9, R14

cols8:
	MOVD R13, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MUL8H(2, 0, 16)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 17)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 18)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 19)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 20)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 21)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 22)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 23)
	ADD   R8, R15

	VST1.P [V2.H8], 16(R12)

	ADD  $8, R13
	SUB  $8, R14
	CBNZ R14, cols8

	ADD  R1, R0
	ADD  R3, R2
	SUB  $1, R10
	CBNZ R10, rows8

	RET

// func mcTap4NEON(dst *int16, dstStride int, src *uint8, srcStride, tapStride, w, h int, f *int16)
//
// 4-tap interpolation of 8.5.3.3.3 for eight-bit samples, in one direction.
TEXT ·mcTap4NEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R3
	MOVD tapStride+32(FP), R8
	MOVD w+40(FP), R9
	MOVD h+48(FP), R10
	MOVD f+56(FP), R11

	LSL $1, R1

	VLD1R.P 2(R11), [V16.H8]
	VLD1R.P 2(R11), [V17.H8]
	VLD1R.P 2(R11), [V18.H8]
	VLD1R.P 2(R11), [V19.H8]

rows4:
	MOVD R0, R12
	MOVD R2, R13
	MOVD R9, R14

cols4:
	MOVD R13, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MUL8H(2, 0, 16)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 17)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 18)
	ADD   R8, R15

	VLD1  (R15), [V0.B8]
	VUXTL V0.B8, V0.H8
	MLA8H(2, 0, 19)
	ADD   R8, R15

	VST1.P [V2.H8], 16(R12)

	ADD  $8, R13
	SUB  $8, R14
	CBNZ R14, cols4

	ADD  R1, R0
	ADD  R3, R2
	SUB  $1, R10
	CBNZ R10, rows4

	RET
