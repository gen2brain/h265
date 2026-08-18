//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func forwardTransform8RVV(dst, src, m *int32, n, shift1, shift2 int)
TEXT ·forwardTransform8RVV(SB), $4096-48
	MOV dst+0(FP), X10
	MOV src+8(FP), X11
	MOV m+16(FP), X12
	MOV n+24(FP), X13
	MOV shift1+32(FP), X14
	MOV shift2+40(FP), X15

	ADD $8, SP, X16
	SLLI $2, X13, X17

	MOV  $1, X18
	ADD  $-1, X14, X19
	SLL  X19, X18, X18
	MOV  $1, X19
	ADD  $-1, X15, X20
	SLL  X20, X19, X19

	MOV X11, X20
	MOV X16, X21
	MOV X13, X22

rows:
	MOV  X13, X23
	MOV  $0, X24

cols:
	VSETVLI X23, E32, M1, TA, MA, X26
	VMVVI   $0, V1

	MOV X20, X28
	ADD X12, X24, X29
	MOV X13, X30

sum:
	MOVW     (X28), X31
	VLE32V   (X29), V2
	VMACCVX  V2, X31, V1
	ADD      $4, X28
	ADD      X17, X29
	ADD      $-1, X30
	BNEZ     X30, sum

	VADDVX X18, V1, V1
	VSRAVX X14, V1, V1
	ADD    X21, X24, X28
	VSE32V V1, (X28)

	SLLI $2, X26, X29
	ADD  X29, X24
	SUB  X26, X23
	BNEZ X23, cols

	ADD  X17, X20
	ADD  X17, X21
	ADD  $-1, X22
	BNEZ X22, rows

	MOV X13, X22
	MOV X10, X20
	MOV X12, X21

outrows:
	MOV  X13, X23
	MOV  $0, X24

outcols:
	VSETVLI X23, E32, M1, TA, MA, X26
	VMVVI   $0, V1

	ADD X16, X24, X28
	MOV X21, X29
	MOV X13, X30

outsum:
	VLE32V   (X28), V2
	MOVW     (X29), X31
	VMACCVX  V2, X31, V1
	ADD      X17, X28
	ADD      X17, X29
	ADD      $-1, X30
	BNEZ     X30, outsum

	VADDVX X19, V1, V1
	VSRAVX X15, V1, V1
	ADD    X20, X24, X28
	VSE32V V1, (X28)

	SLLI $2, X26, X29
	ADD  X29, X24
	SUB  X26, X23
	BNEZ X23, outcols

	ADD  X17, X20
	ADD  $4, X21
	ADD  $-1, X22
	BNEZ X22, outrows

	RET

// func quantize8RVV(dst, src *int32, count int, scale, offset int32, qbits int)
//
// The forward direction of 8.6.3. The magnitude is unsigned, which keeps the
// widening multiply exact over the whole int32 range.
TEXT ·quantize8RVV(SB), NOSPLIT, $0-40
	MOV dst+0(FP), X10
	MOV src+8(FP), X11
	MOV count+16(FP), X12
	MOVW scale+24(FP), X13
	MOVW offset+28(FP), X14
	MOV qbits+32(FP), X15

	SLLI $32, X14, X14
	SRLI $32, X14, X14

	MOV $0x7fff, X16

loop:
	VSETVLI X12, E32, M1, TA, MA, X17

	VLE32V (X11), V1

	VRSUBVX X0, V1, V2
	VMAXVV  V1, V2, V2

	VWMULUVX X13, V2, V4

	VSETVLI X17, E64, M2, TA, MA, X0
	VADDVX  X14, V4, V4
	VSRLVX  X15, V4, V4

	VSETVLI X17, E32, M1, TA, MA, X0
	VNSRLWI $0, V4, V6

	VMINVX X16, V6, V6

	VSRAVI $31, V1, V7
	VXORVV V7, V6, V6
	VSUBVV V7, V6, V6

	VSE32V V6, (X10)

	SLLI $2, X17, X18
	ADD  X18, X11
	ADD  X18, X10
	SUB  X17, X12
	BNEZ X12, loop

	RET
