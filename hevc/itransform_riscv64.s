//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func addResidual8RVV(dst *uint8, stride int, coef *int32, n, shift int)
TEXT ·addResidual8RVV(SB), NOSPLIT, $0-40
	MOV dst+0(FP), X10
	MOV stride+8(FP), X11
	MOV coef+16(FP), X12
	MOV n+24(FP), X13
	MOV shift+32(FP), X14

	// rnd is 1<<(shift-1), and zero when nothing is shifted.
	MOV  $0, X15
	BLEZ X14, havernd
	MOV  $1, X15
	ADD  $-1, X14, X16
	SLL  X16, X15, X15

havernd:
	MOV $255, X17
	MOV X13, X18

rows:
	MOV X10, X19
	MOV X13, X20

cols:
	VSETVLI X20, E32, M1, TA, MA, X21

	VLE32V   (X12), V1
	VADDVX   X15, V1, V1
	VSRAVX   X14, V1, V1
	VLE8V    (X19), V2
	VZEXTVF4 V2, V3
	VADDVV   V3, V1, V1
	VMAXVX   X0, V1, V1
	VMINVX   X17, V1, V1

	VSETVLI X20, E16, MF2, TA, MA, X21
	VNSRLWI $0, V1, V4
	VSETVLI X20, E8, MF4, TA, MA, X21
	VNSRLWI $0, V4, V5
	VSE8V   V5, (X19)

	SLLI $2, X21, X22
	ADD  X22, X12
	ADD  X21, X19
	SUB  X21, X20
	BNEZ X20, cols

	ADD  X11, X10
	ADD  $-1, X18
	BNEZ X18, rows

	RET

// func odd16RVV(out *int32, in *int32, m *int8, stride int)
//
// The sixteen-wide butterfly of 8.6.4.2, chunked by the vector length so it
// does not assume VLEN.
TEXT ·odd16RVV(SB), NOSPLIT, $0-32
	MOV out+0(FP), X10
	MOV in+8(FP), X11
	MOV m+16(FP), X12
	MOV stride+24(FP), X13

	ADD $4, X11

	// The first basis row is stride rows in, and each step advances by two.
	SLLI $5, X13, X14
	ADD  X14, X12
	SLLI $1, X14

	MOV $16, X15

chunk:
	VSETVLI X15, E32, M1, TA, MA, X16

	VMVVI $0, V1

	MOV X11, X17
	MOV X12, X18
	MOV $16, X19

rows2:
	MOV  (X17), X20
	BEQZ X20, skip

	VLE8V    (X18), V2
	VSEXTVF4 V2, V3
	VMACCVX  V3, X20, V1

skip:
	ADD  $8, X17
	ADD  X14, X18
	ADD  $-1, X19
	BNEZ X19, rows2

	VSE32V V1, (X10)

	SLLI $2, X16, X21
	ADD  X21, X10
	ADD  X16, X12
	SUB  X16, X15
	BNEZ X15, chunk

	RET
