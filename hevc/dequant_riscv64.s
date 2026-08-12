//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func dequant32RVV(coef *int32, m *uint8, n int, ls, rnd, sh, lo, hi int32)
//
// 8.6.3 once the left shift has been folded into the right one. m may be nil,
// which means a flat matrix of sixteen.
TEXT ·dequant32RVV(SB), NOSPLIT, $0-44
	MOV  coef+0(FP), X10
	MOV  m+8(FP), X11
	MOV  n+16(FP), X12
	MOVW ls+24(FP), X13
	MOVW rnd+28(FP), X14
	MOVW sh+32(FP), X15
	MOVW lo+36(FP), X16
	MOVW hi+40(FP), X17

	BEQZ X11, flat

scaled:
	VSETVLI X12, E32, M1, TA, MA, X18

	VLE32V   (X10), V1
	VLE8V    (X11), V2
	VZEXTVF4 V2, V3
	VMULVV   V3, V1, V1
	VMULVX   X13, V1, V1
	VADDVX   X14, V1, V1
	VSRAVX   X15, V1, V1
	VMAXVX   X16, V1, V1
	VMINVX   X17, V1, V1
	VSE32V   V1, (X10)

	SLLI $2, X18, X19
	ADD  X19, X10
	ADD  X18, X11
	SUB  X18, X12
	BNEZ X12, scaled

	RET

flat:
	VSETVLI X12, E32, M1, TA, MA, X18

	VLE32V (X10), V1
	VSLLVI $4, V1, V1
	VMULVX X13, V1, V1
	VADDVX X14, V1, V1
	VSRAVX X15, V1, V1
	VMAXVX X16, V1, V1
	VMINVX X17, V1, V1
	VSE32V V1, (X10)

	SLLI $2, X18, X19
	ADD  X19, X10
	SUB  X18, X12
	BNEZ X12, flat

	RET
