//go:build arm64 && !noasm

#include "textflag.h"

// The two luma filters of 8.7.2.5.7 over the eight lines of one edge. A lane is
// one line, and the store saturates to the byte range the spec clips to anyway.

// Every sum 8.7.2.5.7 forms stays inside a signed halfword: the widest is
// 2*p3 + 3*p2 + p1 + p0 + q0 + 4, which eight bit samples cap at 2044, and the
// normal filter's 9*(q0-p0) - 3*(q1-p1) at 3060. Halfwords are what the loads
// and the stores want anyway, which is a widening step each way fewer.
#define SMAX8H(Vd, Vn, Vm)   WORD $(0x4e606400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SMIN8H(Vd, Vn, Vm)   WORD $(0x4e606c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SSHR8H(Vd, Vn, n)    WORD $(0x4f000400 | ((32 - (n)) << 16) | ((Vn) << 5) | (Vd))
#define SQXTUN8B(Vd, Vn)     WORD $(0x2e212800 | ((Vn) << 5) | (Vd))
#define ABS8H(Vd, Vn)        WORD $(0x4e60b800 | ((Vn) << 5) | (Vd))
#define MUL8H(Vd, Vn, Vm)    WORD $(0x4e609c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define CMGT8H(Vd, Vn, Vm)   WORD $(0x4e603400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define BSL16B(Vd, Vn, Vm)   WORD $(0x6e601c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define ADD8H(Vd, Vn, Vm)    WORD $(0x4e608400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define SUB8H(Vd, Vn, Vm)    WORD $(0x6e608400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define UXTL8H(Vd, Vn)       WORD $(0x2f08a400 | ((Vn) << 5) | (Vd))
#define DUP8H(Vd, Rn)        WORD $(0x4e020c00 | ((Rn) << 5) | (Vd))
#define EOR16B(Vd, Vn, Vm)   WORD $(0x6e201c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define AND16B(Vd, Vn, Vm)   WORD $(0x4e201c00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define MOV16B(Vd, Vn)       WORD $(0x4ea01c00 | ((Vn) << 16) | ((Vn) << 5) | (Vd))

// LOAD8 widens the eight bytes of one position into eight halfwords.
#define LOAD8(addr, Vd) \
	FMOVD (addr), F20 \
	UXTL8H(Vd, 20)

// CLIPC puts v inside base-c .. base+c.
#define CLIPC(v, base, c, t0, t1) \
	SUB8H(t0, base, c)  \
	ADD8H(t1, base, c)  \
	SMAX8H(v, v, t0)    \
	SMIN8H(v, v, t1)

// STORE8 narrows eight halfwords to bytes and writes them.
#define STORE8(v, addr) \
	SQXTUN8B(19, v) \
	FMOVD F19, (addr)

// TCV spreads the two thresholds over the lanes each governs.
#define TCV(Vd) \
	MOVD $0x0001000100010001, R5 \
	MUL  R5, R2, R2              \
	MUL  R5, R6, R6              \
	VMOV R2, Vd.D[0]             \
	VMOV R6, Vd.D[1]

// func deblockStrong8NEON(p *uint8, pitch int, tc0, tc1, flags int32)
TEXT ·deblockStrong8NEON(SB), NOSPLIT, $0-28
	MOVD p+0(FP), R0
	MOVD pitch+8(FP), R1
	MOVW tc0+16(FP), R2
	MOVW tc1+20(FP), R6
	MOVW flags+24(FP), R3

	MOVD R0, R4
	LOAD8(R4, 0)
	ADD  R1, R4
	LOAD8(R4, 1)
	ADD  R1, R4
	LOAD8(R4, 2)
	ADD  R1, R4
	LOAD8(R4, 3)
	ADD  R1, R4
	LOAD8(R4, 4)
	ADD  R1, R4
	LOAD8(R4, 5)
	ADD  R1, R4
	LOAD8(R4, 6)
	ADD  R1, R4
	LOAD8(R4, 7)

	TCV(V8)
	ADD8H(9, 8, 8)

	MOVD $4, R5
	DUP8H(10, 5)
	MOVD $2, R5
	DUP8H(11, 5)

	// p0' from p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4
	ADD8H(12, 3, 2)
	ADD8H(12, 12, 4)
	ADD8H(12, 12, 12)
	ADD8H(12, 12, 1)
	ADD8H(12, 12, 5)
	ADD8H(12, 12, 10)
	SSHR8H(12, 12, 3)
	CLIPC(12, 3, 9, 17, 18)

	// p1' from p2 + p1 + p0 + q0 + 2
	ADD8H(13, 2, 1)
	ADD8H(13, 13, 3)
	ADD8H(13, 13, 4)
	ADD8H(13, 13, 11)
	SSHR8H(13, 13, 2)
	CLIPC(13, 2, 9, 17, 18)

	TSTW $1, R3
	BNE  qside

	// p2' from 2*p3 + 3*p2 + p1 + p0 + q0 + 4
	ADD8H(14, 0, 0)
	ADD8H(14, 14, 1)
	ADD8H(14, 14, 1)
	ADD8H(14, 14, 1)
	ADD8H(14, 14, 2)
	ADD8H(14, 14, 3)
	ADD8H(14, 14, 4)
	ADD8H(14, 14, 10)
	SSHR8H(14, 14, 3)
	CLIPC(14, 1, 9, 17, 18)

	MOVD R0, R4
	ADD  R1, R4
	STORE8(14, R4)
	ADD  R1, R4
	STORE8(13, R4)
	ADD  R1, R4
	STORE8(12, R4)

qside:
	TSTW $2, R3
	BNE  done

	// q0' from p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4
	ADD8H(12, 4, 3)
	ADD8H(12, 12, 5)
	ADD8H(12, 12, 12)
	ADD8H(12, 12, 2)
	ADD8H(12, 12, 6)
	ADD8H(12, 12, 10)
	SSHR8H(12, 12, 3)
	CLIPC(12, 4, 9, 17, 18)

	// q1' from p0 + q0 + q1 + q2 + 2
	ADD8H(13, 4, 3)
	ADD8H(13, 13, 5)
	ADD8H(13, 13, 6)
	ADD8H(13, 13, 11)
	SSHR8H(13, 13, 2)
	CLIPC(13, 5, 9, 17, 18)

	// q2' from p0 + q0 + q1 + 3*q2 + 2*q3 + 4
	ADD8H(14, 6, 6)
	ADD8H(14, 14, 6)
	ADD8H(14, 14, 7)
	ADD8H(14, 14, 7)
	ADD8H(14, 14, 3)
	ADD8H(14, 14, 4)
	ADD8H(14, 14, 5)
	ADD8H(14, 14, 10)
	SSHR8H(14, 14, 3)
	CLIPC(14, 6, 9, 17, 18)

	MOVD R0, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	STORE8(12, R4)
	ADD  R1, R4
	STORE8(13, R4)
	ADD  R1, R4
	STORE8(14, R4)

done:
	RET

// func deblockNormal8NEON(p *uint8, pitch int, tc0, tc1, nd, flags int32)
TEXT ·deblockNormal8NEON(SB), NOSPLIT, $0-32
	MOVD p+0(FP), R0
	MOVD pitch+8(FP), R1
	MOVW tc0+16(FP), R2
	MOVW tc1+20(FP), R6
	MOVW nd+24(FP), R7
	MOVW flags+28(FP), R3

	MOVD R0, R4
	ADD  R1, R4
	LOAD8(R4, 1)
	ADD  R1, R4
	LOAD8(R4, 2)
	ADD  R1, R4
	LOAD8(R4, 3)
	ADD  R1, R4
	LOAD8(R4, 4)
	ADD  R1, R4
	LOAD8(R4, 5)
	ADD  R1, R4
	LOAD8(R4, 6)

	TCV(V8)

	// delta = (9*(q0-p0) - 3*(q1-p1) + 8) >> 4
	SUB8H(12, 4, 3)
	MOVD $9, R5
	DUP8H(15, 5)
	MUL8H(12, 12, 15)
	SUB8H(13, 5, 2)
	MOVD $3, R5
	DUP8H(15, 5)
	MUL8H(13, 13, 15)
	SUB8H(12, 12, 13)
	MOVD $8, R5
	DUP8H(15, 5)
	ADD8H(12, 12, 15)
	SSHR8H(12, 12, 4)

	// keep only the lanes 8.7.2.5.7 leaves inside ten times tc
	ABS8H(21, 12)
	MOVD $10, R5
	DUP8H(15, 5)
	MUL8H(15, 8, 15)
	CMGT8H(16, 15, 21)

	EOR16B(15, 15, 15)
	SUB8H(17, 15, 8)
	SMAX8H(12, 12, 17)
	SMIN8H(12, 12, 8)

	SSHR8H(18, 8, 1)
	EOR16B(15, 15, 15)
	SUB8H(17, 15, 18)

	// the dEp and dEq of each half become the lanes their sample may move in
	AND  $1, R7, R5
	NEG  R5, R5
	VMOV R5, V9.D[0]
	LSR  $2, R7, R5
	AND  $1, R5, R5
	NEG  R5, R5
	VMOV R5, V9.D[1]
	AND16B(9, 9, 16)

	LSR  $1, R7, R5
	AND  $1, R5, R5
	NEG  R5, R5
	VMOV R5, V10.D[0]
	LSR  $3, R7, R5
	AND  $1, R5, R5
	NEG  R5, R5
	VMOV R5, V10.D[1]
	AND16B(10, 10, 16)

	TSTW $1, R3
	BNE  nqside

	ADD8H(14, 3, 12)
	MOV16B(22, 16)
	BSL16B(22, 14, 3)
	MOVD R0, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	STORE8(22, R4)

	// p1' from ((p2+p0+1)>>1 - p1 + delta) >> 1
	ADD8H(14, 1, 3)
	MOVD $1, R5
	DUP8H(15, 5)
	ADD8H(14, 14, 15)
	SSHR8H(14, 14, 1)
	SUB8H(14, 14, 2)
	ADD8H(14, 14, 12)
	SSHR8H(14, 14, 1)
	SMAX8H(14, 14, 17)
	SMIN8H(14, 14, 18)
	ADD8H(14, 14, 2)
	MOV16B(22, 9)
	BSL16B(22, 14, 2)
	MOVD R0, R4
	ADD  R1, R4
	ADD  R1, R4
	STORE8(22, R4)

nqside:
	TSTW $2, R3
	BNE  ndone

	SUB8H(14, 4, 12)
	MOV16B(22, 16)
	BSL16B(22, 14, 4)
	MOVD R0, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	STORE8(22, R4)

	// q1' from ((q2+q0+1)>>1 - q1 - delta) >> 1
	ADD8H(14, 6, 4)
	MOVD $1, R5
	DUP8H(15, 5)
	ADD8H(14, 14, 15)
	SSHR8H(14, 14, 1)
	SUB8H(14, 14, 5)
	SUB8H(14, 14, 12)
	SSHR8H(14, 14, 1)
	SMAX8H(14, 14, 17)
	SMIN8H(14, 14, 18)
	ADD8H(14, 14, 5)
	MOV16B(22, 10)
	BSL16B(22, 14, 5)
	MOVD R0, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	ADD  R1, R4
	STORE8(22, R4)

ndone:
	RET

// TRANSPOSE8 turns eight rows of eight bytes in the low half of V0 through V7
// into eight columns, two to a register, in V8 through V11.
#define TRANSPOSE8() \
	VZIP1 V1.B16, V0.B16, V12.B16  \
	VZIP1 V3.B16, V2.B16, V13.B16  \
	VZIP1 V5.B16, V4.B16, V14.B16  \
	VZIP1 V7.B16, V6.B16, V15.B16  \
	VZIP1 V13.H8, V12.H8, V8.H8    \
	VZIP2 V13.H8, V12.H8, V9.H8    \
	VZIP1 V15.H8, V14.H8, V10.H8   \
	VZIP2 V15.H8, V14.H8, V11.H8   \
	VZIP1 V10.S4, V8.S4, V12.S4    \
	VZIP2 V10.S4, V8.S4, V13.S4    \
	VZIP1 V11.S4, V9.S4, V14.S4    \
	VZIP2 V11.S4, V9.S4, V15.S4    \
	VMOV  V12.B16, V8.B16          \
	VMOV  V13.B16, V9.B16          \
	VMOV  V14.B16, V10.B16         \
	VMOV  V15.B16, V11.B16

// func turnIn8NEON(dst *uint8, src *uint8, stride int)
TEXT ·turnIn8NEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD stride+16(FP), R2

	VLD1 (R1), [V0.B8]
	ADD  R2, R1
	VLD1 (R1), [V1.B8]
	ADD  R2, R1
	VLD1 (R1), [V2.B8]
	ADD  R2, R1
	VLD1 (R1), [V3.B8]
	ADD  R2, R1
	VLD1 (R1), [V4.B8]
	ADD  R2, R1
	VLD1 (R1), [V5.B8]
	ADD  R2, R1
	VLD1 (R1), [V6.B8]
	ADD  R2, R1
	VLD1 (R1), [V7.B8]

	TRANSPOSE8()

	VST1 [V8.B16, V9.B16, V10.B16, V11.B16], (R0)
	RET

// func turnOut8NEON(dst *uint8, stride int, src *uint8)
TEXT ·turnOut8NEON(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD src+16(FP), R2

	VLD1.P 8(R2), [V0.B8]
	VLD1.P 8(R2), [V1.B8]
	VLD1.P 8(R2), [V2.B8]
	VLD1.P 8(R2), [V3.B8]
	VLD1.P 8(R2), [V4.B8]
	VLD1.P 8(R2), [V5.B8]
	VLD1.P 8(R2), [V6.B8]
	VLD1   (R2), [V7.B8]

	TRANSPOSE8()

	VST1 [V8.B8], (R0)
	VMOV V8.D[1], R3
	ADD  R1, R0
	MOVD R3, (R0)
	ADD  R1, R0
	VST1 [V9.B8], (R0)
	VMOV V9.D[1], R3
	ADD  R1, R0
	MOVD R3, (R0)
	ADD  R1, R0
	VST1 [V10.B8], (R0)
	VMOV V10.D[1], R3
	ADD  R1, R0
	MOVD R3, (R0)
	ADD  R1, R0
	VST1 [V11.B8], (R0)
	VMOV V11.D[1], R3
	ADD  R1, R0
	MOVD R3, (R0)
	RET
