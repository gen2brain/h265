//go:build arm64 && !noasm

#include "textflag.h"

#define SMULL4S(Vd, Vn, Vm)    WORD $(0x0e60c000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMULL2_4S(Vd, Vn, Vm)  WORD $(0x4e60c000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMLAL4S_(Vd, Vn, Vm)   WORD $(0x0e608000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMLAL2_4S_(Vd, Vn, Vm) WORD $(0x4e608000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL4S__(Vd, Vn, Vm)   WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SQXTN4H__(Vd, Vn)      WORD $(0x0e614800 | ((Vn) << 5) | (Vd))
#define SQXTN2_8H__(Vd, Vn)    WORD $(0x4e614800 | ((Vn) << 5) | (Vd))

// func mcTapV16x8NEON(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)
//
// The vertical half of the two-pass 8-tap interpolation, at 32 bits.
TEXT ·mcTapV16x8NEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R8
	MOVD w+32(FP), R9
	MOVD h+40(FP), R10
	MOVD shift+48(FP), R6
	MOVD f+56(FP), R11

	LSL $1, R1
	LSL $1, R8

	NEG  R6, R7
	VDUP R7, V4.S4

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

	VLD1 (R15), [V0.H8]
	SMULL4S(2, 0, 16)
	SMULL2_4S(3, 0, 16)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 17)
	SMLAL2_4S_(3, 0, 17)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 18)
	SMLAL2_4S_(3, 0, 18)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 19)
	SMLAL2_4S_(3, 0, 19)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 20)
	SMLAL2_4S_(3, 0, 20)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 21)
	SMLAL2_4S_(3, 0, 21)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 22)
	SMLAL2_4S_(3, 0, 22)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 23)
	SMLAL2_4S_(3, 0, 23)
	ADD  R8, R15

	SSHL4S__(2, 2, 4)
	SSHL4S__(3, 3, 4)
	SQXTN4H__(5, 2)
	SQXTN2_8H__(5, 3)
	VST1.P [V5.H8], 16(R12)

	ADD  $16, R13
	SUB  $8, R14
	CBNZ R14, cols8

	ADD  R1, R0
	ADD  R8, R2
	SUB  $1, R10
	CBNZ R10, rows8

	RET

// func mcTapV16x4NEON(dst *int16, dstStride int, src *int16, srcStride, w, h, shift int, f *int16)
//
// The vertical half of the two-pass 4-tap interpolation, at 32 bits.
TEXT ·mcTapV16x4NEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD dstStride+8(FP), R1
	MOVD src+16(FP), R2
	MOVD srcStride+24(FP), R8
	MOVD w+32(FP), R9
	MOVD h+40(FP), R10
	MOVD shift+48(FP), R6
	MOVD f+56(FP), R11

	LSL $1, R1
	LSL $1, R8

	NEG  R6, R7
	VDUP R7, V4.S4

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

	VLD1 (R15), [V0.H8]
	SMULL4S(2, 0, 16)
	SMULL2_4S(3, 0, 16)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 17)
	SMLAL2_4S_(3, 0, 17)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 18)
	SMLAL2_4S_(3, 0, 18)
	ADD  R8, R15

	VLD1 (R15), [V0.H8]
	SMLAL4S_(2, 0, 19)
	SMLAL2_4S_(3, 0, 19)
	ADD  R8, R15

	SSHL4S__(2, 2, 4)
	SSHL4S__(3, 3, 4)
	SQXTN4H__(5, 2)
	SQXTN2_8H__(5, 3)
	VST1.P [V5.H8], 16(R12)

	ADD  $16, R13
	SUB  $8, R14
	CBNZ R14, cols4

	ADD  R1, R0
	ADD  R8, R2
	SUB  $1, R10
	CBNZ R10, rows4

	RET
