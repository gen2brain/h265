//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func predPlanar8RVV(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)
//
// 8.4.4.2.4. left is read backwards, the order 8.4.4.2.2 stores it in.
TEXT ·predPlanar8RVV(SB), NOSPLIT, $0-64
	MOV dst+0(FP), X10
	MOV stride+8(FP), X11
	MOV top+16(FP), X12
	MOV left+24(FP), X13
	MOV tr+32(FP), X14
	MOV bl+40(FP), X15
	MOV n+48(FP), X16
	MOV shift+56(FP), X17

	MOV $0, X18

rows:
	// l = left[-y], w = n-1-y, c = (y+1)*bl + n
	SLLI $2, X18, X19
	SUB  X19, X13, X20
	MOVW (X20), X21

	SUB  X18, X16, X22
	ADD  $-1, X22

	ADD  $1, X18, X23
	MUL  X15, X23, X23
	ADD  X16, X23

	MOV X10, X24
	MOV X12, X25
	MOV X16, X26
	MOV $0, X28

cols:
	VSETVLI X26, E32, M1, TA, MA, X30

	// v = l*(n-1-x) + tr*(x+1) + w*top[x] + c
	VIDV     V1
	VADDVX   X28, V1, V1
	VADDVI   $1, V1, V1
	VRSUBVX  X16, V1, V2
	VMULVX   X21, V2, V2
	VMULVX   X14, V1, V1
	VADDVV   V1, V2, V2

	VLE32V (X25), V3
	VMACCVX V3, X22, V2

	VADDVX X23, V2, V2
	VSRAVX X17, V2, V2

	VSETVLI X26, E16, MF2, TA, MA, X30
	VNSRLWI $0, V2, V4
	VSETVLI X26, E8, MF4, TA, MA, X30
	VNSRLWI $0, V4, V5
	VSE8V   V5, (X24)

	SLLI $2, X30, X31
	ADD  X31, X25
	ADD  X30, X24
	ADD  X30, X28
	SUB  X30, X26
	BNEZ X26, cols

	ADD  X11, X10
	ADD  $1, X18
	BLT  X18, X16, rows

	RET

// func predAngular8RVV(dst *uint8, stride int, ref *int32, angle, n int)
//
// The interpolation of 8.4.4.2.6 for a vertical mode. ref is the corner, and a
// negative angle reads the n entries before it. The weights sum to 32, so the
// result is a sample again, the narrowing shifts need no clamp, and a zero
// second weight lands on the sample itself.
TEXT ·predAngular8RVV(SB), NOSPLIT, $0-40
	MOV dst+0(FP), X10
	MOV stride+8(FP), X11
	MOV ref+16(FP), X12
	MOV angle+24(FP), X13
	MOV n+32(FP), X14

	MOV $16, X15
	MOV $0, X16
	MOV X10, X17

rowloop:
	ADD  $1, X16, X18
	MUL  X13, X18, X18
	SRAI $5, X18, X19
	ANDI $31, X18, X18

	SLLI $2, X19, X20
	ADD  X12, X20
	ADD  $4, X20
	ADD  $4, X20, X21

	MOV  $32, X22
	SUB  X18, X22, X22

	MOV X14, X23
	MOV X17, X24

cols:
	VSETVLI X23, E32, M1, TA, MA, X25

	VLE32V (X20), V1
	VLE32V (X21), V2
	VMULVX X22, V1, V1
	VMULVX X18, V2, V2
	VADDVV V2, V1, V1
	VADDVX X15, V1, V1

	VSETVLI X23, E16, MF2, TA, MA, X26
	VNSRLWI $5, V1, V3

	VSETVLI X23, E8, MF4, TA, MA, X26
	VNSRLWI $0, V3, V4
	VSE8V   V4, (X24)

	SLLI $2, X25, X26
	ADD  X26, X20
	ADD  X26, X21
	ADD  X25, X24
	SUB  X25, X23
	BNEZ X23, cols

	ADD  X11, X17
	ADD  $1, X16
	BNE  X14, X16, rowloop

	RET
