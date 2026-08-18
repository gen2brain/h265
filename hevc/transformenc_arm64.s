//go:build arm64 && !noasm

#include "textflag.h"

#define MUL4S(Vd, Vn, Vm)  WORD $(0x4ea09c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL4S(Vd, Vn, Vm) WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func forwardTransform8NEON(dst, src, m *int32, n, shift1, shift2 int)
TEXT ·forwardTransform8NEON(SB), $4096-48
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD m+16(FP), R2
	MOVD n+24(FP), R3
	MOVD shift1+32(FP), R4

	LSL $2, R3, R5

	MOVD $1, R6
	SUB  $1, R4, R7
	LSL  R7, R6, R6
	VDUP R6, V3.S4
	NEG  R4, R7
	VDUP R7, V4.S4

	MOVD RSP, R10
	MOVD R3, R8

row:
	MOVD $0, R11

rowcoef:
	VMOVI $0, V0.B16
	MOVD  R1, R12
	ADD   R11, R2, R13
	MOVD  R3, R14

rowsum:
	MOVW (R12), R15
	VDUP R15, V1.S4
	VLD1 (R13), [V2.S4]
	MUL4S(2, 2, 1)
	VADD V2.S4, V0.S4, V0.S4
	ADD  $4, R12
	ADD  R5, R13
	SUB  $1, R14
	CBNZ R14, rowsum

	VADD V3.S4, V0.S4, V0.S4
	SSHL4S(0, 0, 4)
	ADD   R11, R10, R15
	VST1  [V0.S4], (R15)
	ADD   $16, R11
	CMP   R5, R11
	BLT   rowcoef

	ADD  R5, R1
	ADD  R5, R10
	SUB  $1, R8
	CBNZ R8, row

	MOVD shift2+40(FP), R4
	MOVD $1, R6
	SUB  $1, R4, R7
	LSL  R7, R6, R6
	VDUP R6, V3.S4
	NEG  R4, R7
	VDUP R7, V4.S4

	MOVD RSP, R10
	MOVD R2, R9
	MOVD R3, R8

out:
	MOVD $0, R11

outcoef:
	VMOVI $0, V0.B16
	ADD   R11, R10, R12
	MOVD  R9, R13
	MOVD  R3, R14

outsum:
	VLD1 (R12), [V1.S4]
	MOVW (R13), R15
	VDUP R15, V2.S4
	MUL4S(1, 1, 2)
	VADD V1.S4, V0.S4, V0.S4
	ADD  R5, R12
	ADD  R5, R13
	SUB  $1, R14
	CBNZ R14, outsum

	VADD V3.S4, V0.S4, V0.S4
	SSHL4S(0, 0, 4)
	ADD   R11, R0, R15
	VST1  [V0.S4], (R15)
	ADD   $16, R11
	CMP   R5, R11
	BLT   outcoef

	ADD  R5, R0
	ADD  $4, R9
	SUB  $1, R8
	CBNZ R8, out

	RET

#define ABS4S(Vd, Vn)         WORD $(0x4ea0b800 | ((Vn) << 5) | (Vd))
#define UMULL2D(Vd, Vn, Vm)   WORD $(0x2ea0c000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define UMULL2_2D(Vd, Vn, Vm) WORD $(0x6ea0c000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define USHL2D(Vd, Vn, Vm)    WORD $(0x6ee04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMIN4S(Vd, Vn, Vm)    WORD $(0x4ea06c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHR31_4S(Vd, Vn)     WORD $(0x4f210400 | ((Vn) << 5) | (Vd))

// func quantize8NEON(dst, src *int32, count int, scale, offset int32, qbits int)
//
// The forward direction of 8.6.3. The magnitude is unsigned, which keeps the
// widening multiply exact over the whole int32 range.
TEXT ·quantize8NEON(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD count+16(FP), R2
	MOVW scale+24(FP), R3
	MOVW offset+28(FP), R4
	MOVD qbits+32(FP), R5

	VDUP R3, V20.S4

	AND  $0xffffffff, R4, R4
	VDUP R4, V21.D2

	NEG  R5, R6
	VDUP R6, V22.D2

	MOVD $0x7fff, R7
	VDUP R7, V23.S4

	MOVD $0, R8

loop:
	VLD1 (R1), [V0.S4]

	ABS4S(1, 0)

	UMULL2D(2, 1, 20)
	UMULL2_2D(3, 1, 20)

	VADD V21.D2, V2.D2, V2.D2
	VADD V21.D2, V3.D2, V3.D2

	USHL2D(2, 2, 22)
	USHL2D(3, 3, 22)

	VUZP1 V3.S4, V2.S4, V4.S4

	SMIN4S(4, 4, 23)

	SSHR31_4S(5, 0)
	VEOR V5.B16, V4.B16, V4.B16
	VSUB V5.S4, V4.S4, V4.S4

	VST1 [V4.S4], (R0)

	ADD  $16, R1
	ADD  $16, R0
	ADD  $4, R8
	CMP  R2, R8
	BLT  loop

	RET
