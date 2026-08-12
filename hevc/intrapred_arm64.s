//go:build arm64 && !noasm

#include "textflag.h"

#define MUL4S(Vd, Vn, Vm)   WORD $(0x4ea09c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL4S_(Vd, Vn, Vm) WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SQXTN4H_(Vd, Vn)    WORD $(0x0e614800 | ((Vn) << 5) | (Vd))
#define SQXTN2_8H_(Vd, Vn)  WORD $(0x4e614800 | ((Vn) << 5) | (Vd))
#define SQXTUN8B_(Vd, Vn)   WORD $(0x2e212800 | ((Vn) << 5) | (Vd))

DATA lanes<>+0x00(SB)/4, $0
DATA lanes<>+0x04(SB)/4, $1
DATA lanes<>+0x08(SB)/4, $2
DATA lanes<>+0x0c(SB)/4, $3
DATA lanes<>+0x10(SB)/4, $4
DATA lanes<>+0x14(SB)/4, $5
DATA lanes<>+0x18(SB)/4, $6
DATA lanes<>+0x1c(SB)/4, $7
GLOBL lanes<>(SB), RODATA|NOPTR, $32

// func predPlanar8NEON(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)
//
// 8.4.4.2.4. left is read backwards, the order 8.4.4.2.2 stores it in.
TEXT ·predPlanar8NEON(SB), NOSPLIT, $0-64
	MOVD dst+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD top+16(FP), R2
	MOVD left+24(FP), R3
	MOVD tr+32(FP), R4
	MOVD bl+40(FP), R5
	MOVD n+48(FP), R6
	MOVD shift+56(FP), R7

	MOVD $lanes<>(SB), R8
	VLD1 (R8), [V16.S4, V17.S4]

	VDUP R6, V18.S4
	VDUP R4, V19.S4

	NEG  R7, R9
	VDUP R9, V20.S4

	MOVD $0, R10

rows:
	// l = left[-y], w = n-1-y, c = (y+1)*bl + n
	LSL  $2, R10, R11
	SUB  R11, R3, R12
	MOVW (R12), R13
	VDUP R13, V21.S4

	SUB  R10, R6, R14
	SUB  $1, R14
	VDUP R14, V22.S4

	ADD   $1, R10, R15
	MUL   R5, R15, R15
	ADD   R6, R15
	VDUP  R15, V23.S4

	MOVD R0, R16
	MOVD R2, R17
	MOVD $0, R19

cols:
	// xv = lanes + base + 1, and n - xv is n-1-x.
	ADD  $1, R19, R20
	VDUP R20, V0.S4
	VADD V16.S4, V0.S4, V1.S4
	VADD V17.S4, V0.S4, V2.S4
	VSUB V1.S4, V18.S4, V3.S4
	VSUB V2.S4, V18.S4, V4.S4

	MUL4S(3, 3, 21)
	MUL4S(4, 4, 21)
	MUL4S(1, 1, 19)
	MUL4S(2, 2, 19)
	VADD V1.S4, V3.S4, V3.S4
	VADD V2.S4, V4.S4, V4.S4

	VLD1.P 32(R17), [V5.S4, V6.S4]
	MUL4S(5, 5, 22)
	MUL4S(6, 6, 22)
	VADD V5.S4, V3.S4, V3.S4
	VADD V6.S4, V4.S4, V4.S4

	VADD V23.S4, V3.S4, V3.S4
	VADD V23.S4, V4.S4, V4.S4
	SSHL4S_(3, 3, 20)
	SSHL4S_(4, 4, 20)

	SQXTN4H_(7, 3)
	SQXTN2_8H_(7, 4)
	SQXTUN8B_(7, 7)
	VST1.P [V7.B8], 8(R16)

	ADD  $8, R19
	CMP  R6, R19
	BLT  cols

	ADD  R1, R0
	ADD  $1, R10
	CMP  R6, R10
	BLT  rows

	RET
