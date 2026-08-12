//go:build arm64 && !noasm

#include "textflag.h"

#define MUL4S(Vd, Vn, Vm)  WORD $(0x4ea09c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHL4S(Vd, Vn, Vm) WORD $(0x4ea04400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMAX4S(Vd, Vn, Vm) WORD $(0x4ea06400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMIN4S(Vd, Vn, Vm) WORD $(0x4ea06c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// func idctCols4NEON(dst, src, m *int32, n, mstride, shift int, rnd, lo, hi int32)
//
// One pass of 8.6.4.2 over four columns at a time. Outputs come four at a
// time so the even and odd accumulators stay in registers, and j is unrolled
// by two so the parity is static.
TEXT ·idctCols4NEON(SB), NOSPLIT, $0-60
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD m+16(FP), R2
	MOVD n+24(FP), R3
	MOVD mstride+32(FP), R4
	MOVD shift+40(FP), R5
	MOVW rnd+48(FP), R6
	MOVW lo+52(FP), R7
	MOVW hi+56(FP), R19

	LSL $2, R4

	VDUP R6, V10.S4
	VDUP R7, V11.S4
	VDUP R19, V12.S4
	NEG  R5, R8
	VDUP R8, V13.S4

	LSR $1, R3, R9
	LSL $2, R3, R10

	MOVD $0, R11

xloop:
	MOVD $0, R12

iloop:
	VMOVI $0, V0.B16
	VMOVI $0, V1.B16
	VMOVI $0, V2.B16
	VMOVI $0, V3.B16
	VMOVI $0, V4.B16
	VMOVI $0, V5.B16
	VMOVI $0, V6.B16
	VMOVI $0, V7.B16

	ADD R11<<2, R1, R13
	ADD R12<<2, R2, R14

	MOVD R3, R15

jloop:
	VLD1 (R13), [V8.S4]
	VMOV V8.D[0], R16
	VMOV V8.D[1], R17
	ORR  R17, R16, R16
	CBZ  R16, skipeven

	VLD1 (R14), [V9.S4]

	VDUP V9.S[0], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V0.S4, V0.S4
	VDUP V9.S[1], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V1.S4, V1.S4
	VDUP V9.S[2], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V2.S4, V2.S4
	VDUP V9.S[3], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V3.S4, V3.S4

skipeven:
	ADD R10, R13
	ADD R4, R14

	VLD1 (R13), [V8.S4]
	VMOV V8.D[0], R16
	VMOV V8.D[1], R17
	ORR  R17, R16, R16
	CBZ  R16, skipodd

	VLD1 (R14), [V9.S4]

	VDUP V9.S[0], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V4.S4, V4.S4
	VDUP V9.S[1], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V5.S4, V5.S4
	VDUP V9.S[2], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V6.S4, V6.S4
	VDUP V9.S[3], V14.S4
	MUL4S(14, 14, 8)
	VADD V14.S4, V7.S4, V7.S4

skipodd:
	ADD R10, R13
	ADD R4, R14

	SUB  $2, R15
	CBNZ R15, jloop

	// The low output rows run forward from i0, the high ones backward from
	// n-1-i0, which is the M[j][n-1-i] symmetry.
	MUL R10, R12, R16
	ADD R0, R16
	ADD R11<<2, R16, R16

	SUB R12, R3, R17
	SUB $1, R17
	MUL R10, R17, R17
	ADD R0, R17
	ADD R11<<2, R17, R17

#define EMIT(Ve, Vo)                 \
	VADD Vo.S4, Ve.S4, V14.S4;   \
	VSUB Vo.S4, Ve.S4, V15.S4;   \
	VADD V10.S4, V14.S4, V14.S4; \
	VADD V10.S4, V15.S4, V15.S4; \
	SSHL4S(14, 14, 13);          \
	SSHL4S(15, 15, 13);          \
	SMAX4S(14, 14, 11);          \
	SMIN4S(14, 14, 12);          \
	SMAX4S(15, 15, 11);          \
	SMIN4S(15, 15, 12);          \
	VST1 [V14.S4], (R16);        \
	VST1 [V15.S4], (R17);        \
	ADD  R10, R16;               \
	SUB  R10, R17

	EMIT(V0, V4)
	EMIT(V1, V5)
	EMIT(V2, V6)
	EMIT(V3, V7)

	ADD  $4, R12
	CMP  R9, R12
	BLT  iloop

	ADD  $4, R11
	CMP  R3, R11
	BLT  xloop

	RET
