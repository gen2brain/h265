//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func idctColsRVV(dst, src, m *int32, n, mstride, shift int, rnd, lo, hi int32)
//
// One pass of 8.6.4.2 over as many columns as the vector length takes. Outputs
// come four at a time so the even and odd accumulators stay in registers, and
// j is unrolled by two so the parity is static.
TEXT ·idctColsRVV(SB), NOSPLIT, $0-60
	MOV  dst+0(FP), X10
	MOV  src+8(FP), X11
	MOV  m+16(FP), X12
	MOV  n+24(FP), X13
	MOV  mstride+32(FP), X14
	MOV  shift+40(FP), X15
	MOVW rnd+48(FP), X16
	MOVW lo+52(FP), X17
	MOVW hi+56(FP), X18

	SLLI $2, X14
	SRLI $3, X13, X19
	SLLI $2, X13, X20

	MOV $0, X21
	MOV X13, X22

xloop:
	VSETVLI X22, E32, M1, TA, MA, X23

	MOV $0, X24
	MOV X19, X8

iloop:
	VMVVI $0, V1
	VMVVI $0, V2
	VMVVI $0, V3
	VMVVI $0, V4
	VMVVI $0, V5
	VMVVI $0, V6
	VMVVI $0, V7
	VMVVI $0, V8

	SLLI $2, X21, X30
	ADD  X11, X30, X25

	SLLI $2, X24, X28
	ADD  X12, X28

	MOV X13, X29

jloop:
	VLE32V (X25), V9

	MOVW   (X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V1, V1
	MOVW   4(X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V2, V2
	MOVW   8(X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V3, V3
	MOVW   12(X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V4, V4

	ADD X20, X25
	ADD X14, X28

	VLE32V (X25), V9

	MOVW   (X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V5, V5
	MOVW   4(X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V6, V6
	MOVW   8(X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V7, V7
	MOVW   12(X28), X7
	VMULVX X7, V9, V10
	VADDVV V10, V8, V8

	ADD X20, X25
	ADD X14, X28

	ADD  $-2, X29
	BNEZ X29, jloop

	// The low output rows run forward from i0, the high ones backward from
	// n-1-i0, which is the M[j][n-1-i] symmetry.
	MUL X20, X24, X5
	ADD X10, X5
	ADD X30, X5

	SUB X24, X13, X6
	ADD $-1, X6
	MUL X20, X6, X6
	ADD X10, X6
	ADD X30, X6

#define EMIT(Ve, Vo)          \
	VADDVV Vo, Ve, V11;   \
	VSUBVV Vo, Ve, V12;   \
	VADDVX X16, V11, V11; \
	VADDVX X16, V12, V12; \
	VSRAVX X15, V11, V11; \
	VSRAVX X15, V12, V12; \
	VMAXVX X17, V11, V11; \
	VMINVX X18, V11, V11; \
	VMAXVX X17, V12, V12; \
	VMINVX X18, V12, V12; \
	VSE32V V11, (X5);     \
	VSE32V V12, (X6);     \
	ADD    X20, X5;       \
	SUB    X20, X6

	EMIT(V1, V5)
	EMIT(V2, V6)
	EMIT(V3, V7)
	EMIT(V4, V8)

	ADD  $4, X24
	ADD  $-1, X8
	BNEZ X8, iloop

	ADD  X23, X21
	SUB  X23, X22
	BNEZ X22, xloop

	RET
