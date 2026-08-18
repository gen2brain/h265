//go:build amd64 && !noasm

#include "textflag.h"

TEXT ·forwardTransform8AVX2(SB), $4096-48
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ m+16(FP), R14
	MOVQ n+24(FP), R8
	MOVQ R8, R9
	SHLQ $2, R9

	CMPQ R8, $4
	JEQ  four

	MOVQ shift1+32(FP), CX
	MOVQ CX, X14
	DECQ CX
	MOVQ $1, R10
	SHLQ CL, R10
	MOVQ R10, X15
	VPBROADCASTD X15, Y15

	MOVQ SI, R10
	LEAQ 0(SP), R11
	MOVQ R8, R12

row8:
	XORQ BX, BX

coef8:
	VPXOR Y0, Y0, Y0
	MOVQ  R10, AX
	LEAQ  (R14)(BX*1), DX
	MOVQ  R8, CX

sum8:
	VPBROADCASTD (AX), Y1
	VMOVDQU      (DX), Y2
	VPMULLD      Y1, Y2, Y2
	VPADDD       Y2, Y0, Y0
	ADDQ         $4, AX
	ADDQ         R9, DX
	DECQ         CX
	JNZ          sum8

	VPADDD   Y15, Y0, Y0
	VPSRAD   X14, Y0, Y0
	VMOVDQU  Y0, (R11)(BX*1)
	ADDQ     $32, BX
	CMPQ     BX, R9
	JLT      coef8

	ADDQ R9, R10
	ADDQ R9, R11
	DECQ R12
	JNZ  row8

	MOVQ shift2+40(FP), CX
	MOVQ CX, X14
	DECQ CX
	MOVQ $1, R10
	SHLQ CL, R10
	MOVQ R10, X15
	VPBROADCASTD X15, Y15

	LEAQ 0(SP), R10
	MOVQ R14, R11
	MOVQ R8, R12

out8:
	XORQ BX, BX

outcoef8:
	VPXOR Y0, Y0, Y0
	MOVQ  R10, AX
	MOVQ  R11, DX
	MOVQ  R8, CX

outsum8:
	VMOVDQU      (AX)(BX*1), Y1
	VPBROADCASTD (DX), Y2
	VPMULLD      Y1, Y2, Y2
	VPADDD       Y2, Y0, Y0
	ADDQ         R9, AX
	ADDQ         R9, DX
	DECQ         CX
	JNZ          outsum8

	VPADDD  Y15, Y0, Y0
	VPSRAD  X14, Y0, Y0
	VMOVDQU Y0, (DI)(BX*1)
	ADDQ    $32, BX
	CMPQ    BX, R9
	JLT     outcoef8

	ADDQ R9, DI
	ADDQ $4, R11
	DECQ R12
	JNZ  out8

	VZEROUPPER
	RET

four:
	MOVQ shift1+32(FP), CX
	MOVQ CX, X14
	DECQ CX
	MOVQ $1, R10
	SHLQ CL, R10
	MOVQ R10, X15
	VPBROADCASTD X15, X15

	MOVQ SI, R10
	LEAQ 0(SP), R11
	MOVQ R8, R12

row4:
	XORQ BX, BX

coef4:
	VPXOR X0, X0, X0
	MOVQ  R10, AX
	LEAQ  (R14)(BX*1), DX
	MOVQ  R8, CX

sum4:
	VPBROADCASTD (AX), X1
	VMOVDQU      (DX), X2
	VPMULLD      X1, X2, X2
	VPADDD       X2, X0, X0
	ADDQ         $4, AX
	ADDQ         R9, DX
	DECQ         CX
	JNZ          sum4

	VPADDD   X15, X0, X0
	VPSRAD   X14, X0, X0
	VMOVDQU  X0, (R11)(BX*1)
	ADDQ     $16, BX
	CMPQ     BX, R9
	JLT      coef4

	ADDQ R9, R10
	ADDQ R9, R11
	DECQ R12
	JNZ  row4

	MOVQ shift2+40(FP), CX
	MOVQ CX, X14
	DECQ CX
	MOVQ $1, R10
	SHLQ CL, R10
	MOVQ R10, X15
	VPBROADCASTD X15, X15

	LEAQ 0(SP), R10
	MOVQ R14, R11
	MOVQ R8, R12

out4:
	XORQ BX, BX

outcoef4:
	VPXOR X0, X0, X0
	MOVQ  R10, AX
	MOVQ  R11, DX
	MOVQ  R8, CX

outsum4:
	VMOVDQU      (AX)(BX*1), X1
	VPBROADCASTD (DX), X2
	VPMULLD      X1, X2, X2
	VPADDD       X2, X0, X0
	ADDQ         R9, AX
	ADDQ         R9, DX
	DECQ         CX
	JNZ          outsum4

	VPADDD  X15, X0, X0
	VPSRAD  X14, X0, X0
	VMOVDQU X0, (DI)(BX*1)
	ADDQ    $16, BX
	CMPQ    BX, R9
	JLT     outcoef4

	ADDQ R9, DI
	ADDQ $4, R11
	DECQ R12
	JNZ  out4

	VZEROUPPER
	RET

TEXT ·forwardTransform8AVX512(SB), $4096-48
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ m+16(FP), R14
	MOVQ n+24(FP), R8
	MOVQ R8, R9
	SHLQ $2, R9

	MOVQ shift1+32(FP), CX
	MOVQ CX, X14
	DECQ CX
	MOVQ $1, R10
	SHLQ CL, R10
	MOVQ R10, X15
	VPBROADCASTD X15, Z15

	MOVQ SI, R10
	LEAQ 0(SP), R11
	MOVQ R8, R12

row16:
	XORQ BX, BX

coef16:
	VPXORD Z0, Z0, Z0
	MOVQ  R10, AX
	LEAQ  (R14)(BX*1), DX
	MOVQ  R8, CX

sum16:
	VPBROADCASTD (AX), Z1
	VMOVDQU32    (DX), Z2
	VPMULLD      Z1, Z2, Z2
	VPADDD       Z2, Z0, Z0
	ADDQ         $4, AX
	ADDQ         R9, DX
	DECQ         CX
	JNZ          sum16

	VPADDD     Z15, Z0, Z0
	VPSRAD     X14, Z0, Z0
	VMOVDQU32  Z0, (R11)(BX*1)
	ADDQ       $64, BX
	CMPQ       BX, R9
	JLT        coef16

	ADDQ R9, R10
	ADDQ R9, R11
	DECQ R12
	JNZ  row16

	MOVQ shift2+40(FP), CX
	MOVQ CX, X14
	DECQ CX
	MOVQ $1, R10
	SHLQ CL, R10
	MOVQ R10, X15
	VPBROADCASTD X15, Z15

	LEAQ 0(SP), R10
	MOVQ R14, R11
	MOVQ R8, R12

out16:
	XORQ BX, BX

outcoef16:
	VPXORD Z0, Z0, Z0
	MOVQ  R10, AX
	MOVQ  R11, DX
	MOVQ  R8, CX

outsum16:
	VMOVDQU32    (AX)(BX*1), Z1
	VPBROADCASTD (DX), Z2
	VPMULLD      Z1, Z2, Z2
	VPADDD       Z2, Z0, Z0
	ADDQ         R9, AX
	ADDQ         R9, DX
	DECQ         CX
	JNZ          outsum16

	VPADDD    Z15, Z0, Z0
	VPSRAD    X14, Z0, Z0
	VMOVDQU32 Z0, (DI)(BX*1)
	ADDQ      $64, BX
	CMPQ      BX, R9
	JLT       outcoef16

	ADDQ R9, DI
	ADDQ $4, R11
	DECQ R12
	JNZ  out16

	VZEROUPPER
	RET

// func quantize8AVX2(dst, src *int32, count int, scale, offset int32, qbits int)
//
// The forward direction of 8.6.3. The magnitude is unsigned, which keeps the
// widening multiply exact over the whole int32 range.
TEXT ·quantize8AVX2(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ count+16(FP), CX
	MOVL scale+24(FP), AX
	MOVL offset+28(FP), DX
	MOVQ qbits+32(FP), R8

	VMOVD        AX, X10
	VPBROADCASTD X10, Y10

	MOVLQZX      DX, DX
	VMOVQ        DX, X11
	VPBROADCASTQ X11, Y11

	MOVL         $0x7fff, AX
	VMOVD        AX, X12
	VPBROADCASTD X12, Y12

	VMOVQ R8, X13

	XORQ BX, BX

loop:
	VMOVDQU (SI)(BX*4), Y1
	VPABSD  Y1, Y2

	VPMULUDQ Y10, Y2, Y3
	VPSRLQ   $32, Y2, Y4
	VPMULUDQ Y10, Y4, Y4

	VPADDQ Y11, Y3, Y3
	VPADDQ Y11, Y4, Y4
	VPSRLQ X13, Y3, Y3
	VPSRLQ X13, Y4, Y4

	VPSLLQ $32, Y4, Y4
	VPOR   Y4, Y3, Y3

	VPMINSD Y12, Y3, Y3
	VPSIGND Y1, Y3, Y3
	VMOVDQU Y3, (DI)(BX*4)

	ADDQ $8, BX
	CMPQ BX, CX
	JLT  loop

	VZEROUPPER
	RET
