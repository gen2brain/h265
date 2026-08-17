//go:build arm64 && !noasm

#include "textflag.h"

#define MUL4S(Vd, Vn, Vm)  WORD $(0x4ea09c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL4S(Vd, Vn, Vm) WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define ABS4S(Vd, Vn)      WORD $(0x4ea0b800 | ((Vn) << 5) | (Vd))
#define SMULL2S(Vd, Vn, Vm) WORD $(0x0ea0c000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMULL2_4S(Vd, Vn, Vm) WORD $(0x4ea0c000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define USHL2D(Vd, Vn, Vm) WORD $(0x6ee04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

TEXT ·quantizeNEON(SB), NOSPLIT, $32-48
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2
	MOVD scale+24(FP), R3
	MOVD offset+32(FP), R4
	MOVD qbits+40(FP), R5

	VDUP R3, V16.S4
	VDUP R4, V17.D2
	NEG R5, R5
	VDUP R5, V19.D2

quantloop:
	VLD1 (R1), [V0.S4]
	ABS4S(1, 0)
	SMULL2S(2, 1, 16)
	SMULL2_4S(3, 1, 16)
	VADD V17.D2, V2.D2, V2.D2
	VADD V17.D2, V3.D2, V3.D2
	USHL2D(2, 2, 19)
	USHL2D(3, 3, 19)
	VST1 [V2.D2], (RSP)
	ADD $16, RSP, R9
	VST1 [V3.D2], (R9)
	MOVD $32767, R8

	MOVD 0(RSP), R6
	CMP R8, R6
	BLE quant0sat
	MOVD R8, R6
quant0sat:
	MOVW 0(R1), R7
	CMPW $0, R7
	BGE quant0
	NEG R6, R6
quant0:
	MOVW R6, 0(R0)
	MOVD 8(RSP), R6
	CMP R8, R6
	BLE quant1sat
	MOVD R8, R6
quant1sat:
	MOVW 4(R1), R7
	CMPW $0, R7
	BGE quant1
	NEG R6, R6
quant1:
	MOVW R6, 4(R0)
	MOVD 16(RSP), R6
	CMP R8, R6
	BLE quant2sat
	MOVD R8, R6
quant2sat:
	MOVW 8(R1), R7
	CMPW $0, R7
	BGE quant2
	NEG R6, R6
quant2:
	MOVW R6, 8(R0)
	MOVD 24(RSP), R6
	CMP R8, R6
	BLE quant3sat
	MOVD R8, R6
quant3sat:
	MOVW 12(R1), R7
	CMPW $0, R7
	BGE quant3
	NEG R6, R6
quant3:
	MOVW R6, 12(R0)

	ADD $16, R0
	ADD $16, R1
	SUB $4, R2
	CBNZ R2, quantloop
	RET

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
