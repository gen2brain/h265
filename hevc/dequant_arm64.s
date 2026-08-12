//go:build arm64 && !noasm

#include "textflag.h"

#define MUL4S_(Vd, Vn, Vm)  WORD $(0x4ea09c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL4S3(Vd, Vn, Vm) WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMAX4S(Vd, Vn, Vm)  WORD $(0x4ea06400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMIN4S(Vd, Vn, Vm)  WORD $(0x4ea06c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func dequant32NEON(coef *int32, m *uint8, n int, ls, rnd, sh, lo, hi int32)
//
// 8.6.3 once the left shift has been folded into the right one. m may be nil,
// which means a flat matrix of sixteen.
TEXT ·dequant32NEON(SB), NOSPLIT, $0-44
	MOVD coef+0(FP), R0
	MOVD m+8(FP), R1
	MOVD n+16(FP), R2
	MOVW ls+24(FP), R3
	MOVW rnd+28(FP), R4
	MOVW sh+32(FP), R5
	MOVW lo+36(FP), R6
	MOVW hi+40(FP), R7

	VDUP R3, V1.S4
	VDUP R4, V2.S4
	NEG  R5, R8
	VDUP R8, V3.S4
	VDUP R6, V4.S4
	VDUP R7, V5.S4

	MOVD $16, R9
	VDUP R9, V6.S4

	CBZ R1, flat

scaled:
	VLD1   (R0), [V16.S4, V17.S4]
	VLD1.P 8(R1), [V7.B8]
	VUXTL  V7.B8, V7.H8
	VUXTL  V7.H4, V18.S4
	VUXTL2 V7.H8, V19.S4

	MUL4S_(16, 16, 18)
	MUL4S_(17, 17, 19)
	MUL4S_(16, 16, 1)
	MUL4S_(17, 17, 1)

	VADD V2.S4, V16.S4, V16.S4
	VADD V2.S4, V17.S4, V17.S4

	SSHL4S3(16, 16, 3)
	SSHL4S3(17, 17, 3)
	SMAX4S(16, 16, 4)
	SMAX4S(17, 17, 4)
	SMIN4S(16, 16, 5)
	SMIN4S(17, 17, 5)

	VST1.P [V16.S4, V17.S4], 32(R0)

	SUB  $8, R2
	CBNZ R2, scaled

	RET

flat:
	VLD1 (R0), [V16.S4, V17.S4]

	MUL4S_(16, 16, 6)
	MUL4S_(17, 17, 6)
	MUL4S_(16, 16, 1)
	MUL4S_(17, 17, 1)

	VADD V2.S4, V16.S4, V16.S4
	VADD V2.S4, V17.S4, V17.S4

	SSHL4S3(16, 16, 3)
	SSHL4S3(17, 17, 3)
	SMAX4S(16, 16, 4)
	SMAX4S(17, 17, 4)
	SMIN4S(16, 16, 5)
	SMIN4S(17, 17, 5)

	VST1.P [V16.S4, V17.S4], 32(R0)

	SUB  $8, R2
	CBNZ R2, flat

	RET
