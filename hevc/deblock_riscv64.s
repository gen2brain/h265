//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// The two luma filters of 8.7.2.5.7 over the eight lines of one edge, with
// position i of line l at p[i*pitch+l].

// LOAD8 widens the eight bytes of one position into eight halfwords.
#define LOAD8(addr, t, Vd) \
	VLE8V    (addr), t \
	VZEXTVF2 t, Vd

// CLIPC puts v inside base-c .. base+c.
#define CLIPC(v, base, c, t0, t1) \
	VSUBVV c, base, t0 \
	VADDVV c, base, t1 \
	VMAXVV t0, v, v    \
	VMINVV t1, v, v

// CLAMP holds v to what a byte can carry.
#define CLAMP(v, hi) \
	VMAXVX X0, v, v \
	VMINVX hi, v, v

// func deblockStrong8RVV(p *uint8, pitch int, tc0, tc1, flags int32)
TEXT ·deblockStrong8RVV(SB), NOSPLIT, $0-28
	MOV  p+0(FP), X10
	MOV  pitch+8(FP), X11
	MOVW tc0+16(FP), X12
	MOVW tc1+20(FP), X13
	MOVW flags+24(FP), X14

	MOV $255, X17

	VSETIVLI $8, E16, M1, TA, MA, X0

	MOV X10, X15
	LOAD8(X15, V16, V1)
	ADD X11, X15
	LOAD8(X15, V16, V2)
	ADD X11, X15
	LOAD8(X15, V16, V3)
	ADD X11, X15
	LOAD8(X15, V16, V4)
	ADD X11, X15
	LOAD8(X15, V16, V5)
	ADD X11, X15
	LOAD8(X15, V16, V6)
	ADD X11, X15
	LOAD8(X15, V16, V7)
	ADD X11, X15
	LOAD8(X15, V16, V8)

	// the first four lines take tc0 and the rest tc1
	SUB    X12, X13, X16
	VIDV   V9
	VSRLVI $2, V9, V9
	VMULVX X16, V9, V9
	VADDVX X12, V9, V9
	VADDVV V9, V9, V10

	// p0' from p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4
	VADDVV V3, V4, V11
	VADDVV V5, V11, V11
	VADDVV V11, V11, V11
	VADDVV V2, V11, V11
	VADDVV V6, V11, V11
	VADDVI $4, V11, V11
	VSRAVI $3, V11, V11
	CLIPC(V11, V4, V10, V15, V16)
	CLAMP(V11, X17)

	// p1' from p2 + p1 + p0 + q0 + 2
	VADDVV V2, V3, V12
	VADDVV V4, V12, V12
	VADDVV V5, V12, V12
	VADDVI $2, V12, V12
	VSRAVI $2, V12, V12
	CLIPC(V12, V3, V10, V15, V16)
	CLAMP(V12, X17)

	AND  $1, X14, X16
	BNE  X0, X16, qside

	// p2' from 2*p3 + 3*p2 + p1 + p0 + q0 + 4
	VADDVV V1, V1, V13
	VADDVV V2, V13, V13
	VADDVV V2, V13, V13
	VADDVV V2, V13, V13
	VADDVV V3, V13, V13
	VADDVV V4, V13, V13
	VADDVV V5, V13, V13
	VADDVI $4, V13, V13
	VSRAVI $3, V13, V13
	CLIPC(V13, V2, V10, V15, V16)
	CLAMP(V13, X17)

	MOV X10, X15
	ADD X11, X15

	VSETIVLI $8, E8, MF2, TA, MA, X0
	VNSRLWI  $0, V13, V14
	VSE8V    V14, (X15)
	ADD      X11, X15
	VNSRLWI  $0, V12, V14
	VSE8V    V14, (X15)
	ADD      X11, X15
	VNSRLWI  $0, V11, V14
	VSE8V    V14, (X15)
	VSETIVLI $8, E16, M1, TA, MA, X0

qside:
	AND  $2, X14, X16
	BNE  X0, X16, done

	// q0' from p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4
	VADDVV V4, V5, V11
	VADDVV V6, V11, V11
	VADDVV V11, V11, V11
	VADDVV V3, V11, V11
	VADDVV V7, V11, V11
	VADDVI $4, V11, V11
	VSRAVI $3, V11, V11
	CLIPC(V11, V5, V10, V15, V16)
	CLAMP(V11, X17)

	// q1' from p0 + q0 + q1 + q2 + 2
	VADDVV V4, V5, V12
	VADDVV V6, V12, V12
	VADDVV V7, V12, V12
	VADDVI $2, V12, V12
	VSRAVI $2, V12, V12
	CLIPC(V12, V6, V10, V15, V16)
	CLAMP(V12, X17)

	// q2' from p0 + q0 + q1 + 3*q2 + 2*q3 + 4
	VADDVV V7, V7, V13
	VADDVV V7, V13, V13
	VADDVV V8, V13, V13
	VADDVV V8, V13, V13
	VADDVV V4, V13, V13
	VADDVV V5, V13, V13
	VADDVV V6, V13, V13
	VADDVI $4, V13, V13
	VSRAVI $3, V13, V13
	CLIPC(V13, V7, V10, V15, V16)
	CLAMP(V13, X17)

	MOV X10, X15
	ADD X11, X15
	ADD X11, X15
	ADD X11, X15
	ADD X11, X15

	VSETIVLI $8, E8, MF2, TA, MA, X0
	VNSRLWI  $0, V11, V14
	VSE8V    V14, (X15)
	ADD      X11, X15
	VNSRLWI  $0, V12, V14
	VSE8V    V14, (X15)
	ADD      X11, X15
	VNSRLWI  $0, V13, V14
	VSE8V    V14, (X15)

done:
	RET

// func deblockNormal8RVV(p *uint8, pitch int, tc0, tc1, nd, flags int32)
TEXT ·deblockNormal8RVV(SB), NOSPLIT, $0-32
	MOV  p+0(FP), X10
	MOV  pitch+8(FP), X11
	MOVW tc0+16(FP), X12
	MOVW tc1+20(FP), X13
	MOVW nd+24(FP), X18
	MOVW flags+28(FP), X14

	MOV $255, X17

	VSETIVLI $8, E16, M1, TA, MA, X0

	MOV X10, X15
	ADD X11, X15
	LOAD8(X15, V16, V2)
	ADD X11, X15
	LOAD8(X15, V16, V3)
	ADD X11, X15
	LOAD8(X15, V16, V4)
	ADD X11, X15
	LOAD8(X15, V16, V5)
	ADD X11, X15
	LOAD8(X15, V16, V6)
	ADD X11, X15
	LOAD8(X15, V16, V7)

	SUB    X12, X13, X16
	VIDV   V9
	VSRLVI $2, V9, V9
	VMULVX X16, V9, V9
	VADDVX X12, V9, V9

	// delta = (9*(q0-p0) - 3*(q1-p1) + 8) >> 4
	VSUBVV V4, V5, V11
	MOV    $9, X16
	VMULVX X16, V11, V11
	VSUBVV V3, V6, V12
	MOV    $3, X16
	VMULVX X16, V12, V12
	VSUBVV V12, V11, V11
	VADDVI $8, V11, V11
	VSRAVI $4, V11, V11

	// keep only the lanes 8.7.2.5.7 leaves inside ten times tc
	VRSUBVX X0, V11, V13
	VMAXVV  V13, V11, V13
	MOV     $10, X16
	VMULVX  X16, V9, V14
	VMSLTVV V14, V13, V19

	VRSUBVX X0, V9, V15
	VMAXVV  V15, V11, V11
	VMINVV  V9, V11, V11

	// the dEp and dEq of each half become the lanes their sample may move in
	AND     $1, X18, X19
	SRL     $2, X18, X20
	AND     $1, X20, X20
	SUB     X19, X20, X21
	VIDV    V14
	VSRLVI  $2, V14, V14
	VMULVX  X21, V14, V14
	VADDVX  X19, V14, V14
	VMSGTVX X0, V14, V20

	SRL     $1, X18, X19
	AND     $1, X19, X19
	SRL     $3, X18, X20
	AND     $1, X20, X20
	SUB     X19, X20, X21
	VIDV    V14
	VSRLVI  $2, V14, V14
	VMULVX  X21, V14, V14
	VADDVX  X19, V14, V14
	VMSGTVX X0, V14, V21

	VSRAVI  $1, V9, V15
	VRSUBVX X0, V15, V16

	AND $1, X14, X16
	BNE X0, X16, nqside

	VMV1RV    V19, V0
	VADDVV    V11, V4, V13
	VMERGEVVM V13, V4, V0, V13
	CLAMP(V13, X17)

	// p1' from ((p2+p0+1)>>1 - p1 + delta) >> 1
	VMANDMM   V19, V20, V0
	VADDVV    V2, V4, V14
	VADDVI    $1, V14, V14
	VSRAVI    $1, V14, V14
	VSUBVV    V3, V14, V14
	VADDVV    V11, V14, V14
	VSRAVI    $1, V14, V14
	VMAXVV    V16, V14, V14
	VMINVV    V15, V14, V14
	VADDVV    V3, V14, V14
	VMERGEVVM V14, V3, V0, V14
	CLAMP(V14, X17)

	MOV X10, X15
	ADD X11, X15
	ADD X11, X15

	VSETIVLI $8, E8, MF2, TA, MA, X0
	VNSRLWI  $0, V14, V17
	VSE8V    V17, (X15)
	ADD      X11, X15
	VNSRLWI  $0, V13, V17
	VSE8V    V17, (X15)
	VSETIVLI $8, E16, M1, TA, MA, X0

nqside:
	AND $2, X14, X16
	BNE X0, X16, ndone

	VMV1RV    V19, V0
	VSUBVV    V11, V5, V13
	VMERGEVVM V13, V5, V0, V13
	CLAMP(V13, X17)

	// q1' from ((q2+q0+1)>>1 - q1 - delta) >> 1
	VMANDMM   V19, V21, V0
	VADDVV    V7, V5, V14
	VADDVI    $1, V14, V14
	VSRAVI    $1, V14, V14
	VSUBVV    V6, V14, V14
	VSUBVV    V11, V14, V14
	VSRAVI    $1, V14, V14
	VMAXVV    V16, V14, V14
	VMINVV    V15, V14, V14
	VADDVV    V6, V14, V14
	VMERGEVVM V14, V6, V0, V14
	CLAMP(V14, X17)

	MOV X10, X15
	ADD X11, X15
	ADD X11, X15
	ADD X11, X15
	ADD X11, X15

	VSETIVLI $8, E8, MF2, TA, MA, X0
	VNSRLWI  $0, V13, V17
	VSE8V    V17, (X15)
	ADD      X11, X15
	VNSRLWI  $0, V14, V17
	VSE8V    V17, (X15)

ndone:
	RET

// func turnIn8RVV(dst *uint8, src *uint8, stride int)
TEXT ·turnIn8RVV(SB), NOSPLIT, $0-24
	MOV dst+0(FP), X10
	MOV src+8(FP), X11
	MOV stride+16(FP), X12

	MOV $8, X13
	MOV $8, X14

	VSETIVLI $8, E8, M1, TA, MA, X0

turnin:
	VLE8V  (X11), V1
	VSSE8V V1, X13, (X10)

	ADD  X12, X11
	ADD  $1, X10
	ADD  $-1, X14
	BNE  X0, X14, turnin

	RET

// func turnOut8RVV(dst *uint8, stride int, src *uint8)
TEXT ·turnOut8RVV(SB), NOSPLIT, $0-24
	MOV dst+0(FP), X10
	MOV stride+8(FP), X11
	MOV src+16(FP), X12

	MOV $8, X13
	MOV $8, X14

	VSETIVLI $8, E8, M1, TA, MA, X0

turnout:
	VLSE8V (X12), X13, V1
	VSE8V  V1, (X10)

	ADD  X11, X10
	ADD  $1, X12
	ADD  $-1, X14
	BNE  X0, X14, turnout

	RET
