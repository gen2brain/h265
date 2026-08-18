//go:build arm64 && !noasm

#include "textflag.h"

#define SMLAL4S(Vd, Vn, Vm)   WORD $(0x0e608000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMLAL2_4S(Vd, Vn, Vm) WORD $(0x4e608000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func sse8NEON(src *uint8, srcStride int, block *uint8, blockStride, n int) int64
//
// The squared error of an n by n block.
TEXT ·sse8NEON(SB), NOSPLIT, $0-48
	MOVD src+0(FP), R0
	MOVD srcStride+8(FP), R1
	MOVD block+16(FP), R2
	MOVD blockStride+24(FP), R3
	MOVD n+32(FP), R4

	VMOVI $0, V16.B16

	MOVD $0, R5

rowloop:
	MOVD R0, R6
	MOVD R2, R7
	MOVD $0, R8

colloop:
	SUB  R8, R4, R9
	CMP  $16, R9
	BLT  half

	VLD1 (R6), [V0.B16]
	VLD1 (R7), [V1.B16]

	VUXTL  V0.B8, V2.H8
	VUXTL  V1.B8, V3.H8
	VSUB   V3.H8, V2.H8, V2.H8
	VUXTL2 V0.B16, V4.H8
	VUXTL2 V1.B16, V5.H8
	VSUB   V5.H8, V4.H8, V4.H8

	SMLAL4S(16, 2, 2)
	SMLAL2_4S(16, 2, 2)
	SMLAL4S(16, 4, 4)
	SMLAL2_4S(16, 4, 4)

	ADD $16, R6
	ADD $16, R7
	ADD $16, R8

	B next

half:
	VLD1 (R6), [V0.B8]
	VLD1 (R7), [V1.B8]

	VUXTL V0.B8, V2.H8
	VUXTL V1.B8, V3.H8
	VSUB  V3.H8, V2.H8, V2.H8

	SMLAL4S(16, 2, 2)
	SMLAL2_4S(16, 2, 2)

	ADD $8, R6
	ADD $8, R7
	ADD $8, R8

next:
	CMP  R4, R8
	BLT  colloop

	ADD $1, R5
	ADD R1, R0
	ADD R3, R2
	CMP R4, R5
	BLT rowloop

	VADDV V16.S4, V17
	VMOV  V17.S[0], R6

	MOVD R6, ret+40(FP)
	RET
