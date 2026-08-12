//go:build arm64 && !noasm

#include "textflag.h"

// func transpose4NEON(dst, src *int32, n int)
//
// Transposes an n by n block of int32 in four by four tiles, the interleave
// twice at element width then at pair width.
TEXT ·transpose4NEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2

	LSL $2, R2, R3

	MOVD $0, R4

yloop:
	MOVD $0, R5

xloop:
	MUL R3, R4, R6
	ADD R1, R6
	ADD R5<<2, R6, R6

	MUL R3, R5, R7
	ADD R0, R7
	ADD R4<<2, R7, R7

	VLD1 (R6), [V0.S4]
	ADD  R3, R6
	VLD1 (R6), [V1.S4]
	ADD  R3, R6
	VLD1 (R6), [V2.S4]
	ADD  R3, R6
	VLD1 (R6), [V3.S4]

	VTRN1 V1.S4, V0.S4, V4.S4
	VTRN2 V1.S4, V0.S4, V5.S4
	VTRN1 V3.S4, V2.S4, V6.S4
	VTRN2 V3.S4, V2.S4, V7.S4

	VTRN1 V6.D2, V4.D2, V8.D2
	VTRN1 V7.D2, V5.D2, V9.D2
	VTRN2 V6.D2, V4.D2, V10.D2
	VTRN2 V7.D2, V5.D2, V11.D2

	VST1 [V8.S4], (R7)
	ADD  R3, R7
	VST1 [V9.S4], (R7)
	ADD  R3, R7
	VST1 [V10.S4], (R7)
	ADD  R3, R7
	VST1 [V11.S4], (R7)

	ADD $4, R5
	CMP R2, R5
	BLT xloop

	ADD $4, R4
	CMP R2, R4
	BLT yloop

	RET
