//go:build amd64 && !noasm

#include "textflag.h"

DATA lane<>+0x00(SB)/4, $0
DATA lane<>+0x04(SB)/4, $1
DATA lane<>+0x08(SB)/4, $2
DATA lane<>+0x0c(SB)/4, $3
DATA lane<>+0x10(SB)/4, $4
DATA lane<>+0x14(SB)/4, $5
DATA lane<>+0x18(SB)/4, $6
DATA lane<>+0x1c(SB)/4, $7
GLOBL lane<>(SB), RODATA|NOPTR, $32

DATA pack<>+0x00(SB)/8, $0x808080800c080400
DATA pack<>+0x08(SB)/8, $0x8080808080808080
DATA pack<>+0x10(SB)/8, $0x808080800c080400
DATA pack<>+0x18(SB)/8, $0x8080808080808080
GLOBL pack<>(SB), RODATA|NOPTR, $32

// func predPlanar8AVX2(dst *uint8, stride int, top *int32, left *int32, tr, bl, n, shift int)
//
// 8.4.4.2.4. left is read backwards, the order 8.4.4.2.2 stores it in, so
// left[-y] is the sample for row y. The result needs no clip.
TEXT ·predPlanar8AVX2(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ stride+8(FP), SI
	MOVQ top+16(FP), DX
	MOVQ left+24(FP), CX
	MOVQ n+48(FP), R8
	MOVQ shift+56(FP), R9

	VPBROADCASTD tr+32(FP), Y10
	VPBROADCASTD n+48(FP), Y12
	VMOVDQU      lane<>(SB), Y11
	VMOVDQU      pack<>(SB), Y13
	MOVQ         R9, X14

	XORQ R10, R10

rows:
	// l = left[-y], w = n-1-y, c = (y+1)*bl + n
	MOVQ R10, AX
	SHLQ $2, AX
	SUBQ AX, CX
	VPBROADCASTD (CX), Y0
	ADDQ AX, CX

	MOVQ R8, AX
	SUBQ R10, AX
	DECQ AX
	MOVQ AX, X1
	VPBROADCASTD X1, Y1

	MOVQ  R10, AX
	INCQ  AX
	IMULQ bl+40(FP), AX
	ADDQ  R8, AX
	MOVQ  AX, X2
	VPBROADCASTD X2, Y2

	MOVQ DI, R11
	MOVQ DX, R12
	XORQ R13, R13

cols:
	// xv = lane + base + 1, and n - xv is n-1-x.
	MOVQ         R13, AX
	INCQ         AX
	MOVQ         AX, X3
	VPBROADCASTD X3, Y3
	VPADDD       Y11, Y3, Y3
	VPSUBD       Y3, Y12, Y4

	VPMULLD Y0, Y4, Y4
	VPMULLD Y10, Y3, Y3
	VPADDD  Y3, Y4, Y4

	VMOVDQU (R12), Y5
	VPMULLD Y1, Y5, Y5
	VPADDD  Y5, Y4, Y4

	VPADDD Y2, Y4, Y4
	VPSRAD X14, Y4, Y4

	VPSHUFB      Y13, Y4, Y4
	VEXTRACTI128 $1, Y4, X6
	MOVL         X4, (R11)
	MOVL         X6, 4(R11)

	ADDQ $32, R12
	ADDQ $8, R11
	ADDQ $8, R13
	CMPQ R13, R8
	JLT  cols

	ADDQ SI, DI
	INCQ R10
	CMPQ R10, R8
	JLT  rows

	VZEROUPPER
	RET
