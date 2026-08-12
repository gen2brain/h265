//go:build arm64 && !noasm

#include "textflag.h"

// Instructions the Go assembler does not know, encoded as gav1d does it. The
// bases came from assembling each form on the board and reading objdump.
#define SSHL4S(Vd, Vn, Vm)  WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SQXTN4H(Vd, Vn)     WORD $(0x0e614800 | ((Vn) << 5) | (Vd))
#define SQXTN2_8H(Vd, Vn)   WORD $(0x4e614800 | ((Vn) << 5) | (Vd))
#define SQXTUN8B(Vd, Vn)    WORD $(0x2e212800 | ((Vn) << 5) | (Vd))
#define SXTL8H(Vd, Vn)      WORD $(0x0f08a400 | ((Vn) << 5) | (Vd))
#define SXTL2_8H(Vd, Vn)    WORD $(0x4f08a400 | ((Vn) << 5) | (Vd))
#define SMLAL4S(Vd, Vn, Vm) WORD $(0x0e608000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMLAL2_4S(Vd, Vn, Vm) WORD $(0x4e608000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func addResidual8NEON(dst *uint8, stride int, coef *int32, n, shift int)
TEXT ·addResidual8NEON(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD coef+16(FP), R2
	MOVD n+24(FP), R3
	MOVD shift+32(FP), R4

	// rnd is 1<<(shift-1), and zero when nothing is shifted.
	MOVD $0, R5
	CMP  $0, R4
	BLE  havernd
	MOVD $1, R5
	SUB  $1, R4, R6
	LSL  R6, R5, R5

havernd:
	VDUP R5, V0.S4
	NEG  R4, R7
	VDUP R7, V1.S4

	MOVD R3, R8

rows:
	MOVD R0, R9
	MOVD R3, R10

cols:
	VLD1.P 32(R2), [V4.S4, V5.S4]

	VADD  V0.S4, V4.S4, V4.S4
	VADD  V0.S4, V5.S4, V5.S4
	SSHL4S(4, 4, 1)
	SSHL4S(5, 5, 1)

	VLD1   (R9), [V6.B8]
	VUXTL  V6.B8, V6.H8
	VUXTL  V6.H4, V7.S4
	VUXTL2 V6.H8, V8.S4

	VADD V7.S4, V4.S4, V4.S4
	VADD V8.S4, V5.S4, V5.S4

	SQXTN4H(9, 4)
	SQXTN2_8H(9, 5)
	SQXTUN8B(9, 9)

	VST1.P [V9.B8], 8(R9)

	SUB  $8, R10
	CBNZ R10, cols

	ADD  R1, R0
	SUB  $1, R8
	CBNZ R8, rows

	RET

// func odd16NEON(out *int32, in *int32, m *int8, stride int)
//
// The sixteen-wide butterfly of 8.6.4.2. Coefficients fit int16 after 8.6.3
// clips them, so the basis row widens to int16 and SMLAL does the widening
// multiply-accumulate into the four int32 accumulators.
TEXT ·odd16NEON(SB), NOSPLIT, $0-32
	MOVD out+0(FP), R0
	MOVD in+8(FP), R1
	MOVD m+16(FP), R2
	MOVD stride+24(FP), R3

	ADD $4, R1

	LSL  $5, R3, R4
	ADD  R4, R2
	LSL  $1, R4

	VMOVI $0, V0.B16
	VMOVI $0, V1.B16
	VMOVI $0, V2.B16
	VMOVI $0, V3.B16

	MOVD $16, R5

loop:
	MOVWU (R1), R6
	CBZ   R6, skip

	VDUP R6, V4.H8

	VLD1 (R2), [V5.B16]
	SXTL8H(6, 5)
	SXTL2_8H(7, 5)

	SMLAL4S(0, 6, 4)
	SMLAL2_4S(1, 6, 4)
	SMLAL4S(2, 7, 4)
	SMLAL2_4S(3, 7, 4)

skip:
	ADD  $8, R1
	ADD  R4, R2
	SUB  $1, R5
	CBNZ R5, loop

	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R0)
	RET
