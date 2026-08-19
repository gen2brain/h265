//go:build amd64 && !noasm

#include "textflag.h"

// The two luma filters of 8.7.2.5.7 over the eight lines of one edge, with
// position i of line l at p[i*pitch+l].

DATA deblkW<>+0(SB)/2, $1
DATA deblkW<>+2(SB)/2, $2
DATA deblkW<>+4(SB)/2, $3
DATA deblkW<>+6(SB)/2, $4
DATA deblkW<>+8(SB)/2, $8
DATA deblkW<>+10(SB)/2, $9
DATA deblkW<>+12(SB)/2, $10
DATA deblkW<>+14(SB)/2, $0
GLOBL deblkW<>(SB), RODATA|NOPTR, $16

#define ONE   deblkW<>+0(SB)
#define TWO   deblkW<>+2(SB)
#define THREE deblkW<>+4(SB)
#define FOUR  deblkW<>+6(SB)
#define EIGHT deblkW<>+8(SB)
#define NINE  deblkW<>+10(SB)
#define TEN   deblkW<>+12(SB)

// ndMask is the four ways 8.7.2.5.7's dEp or dEq can fall over the two halves.
DATA ndMask<>+0(SB)/8, $0x0000000000000000
DATA ndMask<>+8(SB)/8, $0x0000000000000000
DATA ndMask<>+16(SB)/8, $0xffffffffffffffff
DATA ndMask<>+24(SB)/8, $0x0000000000000000
DATA ndMask<>+32(SB)/8, $0x0000000000000000
DATA ndMask<>+40(SB)/8, $0xffffffffffffffff
DATA ndMask<>+48(SB)/8, $0xffffffffffffffff
DATA ndMask<>+56(SB)/8, $0xffffffffffffffff
GLOBL ndMask<>(SB), RODATA|NOPTR, $64

// TCV spreads the thresholds in R9 and R10 over the lanes each governs.
#define TCV(v, t) \
	MOVQ        $0x0001000100010001, R11 \
	IMULQ       R11, R9          \
	IMULQ       R11, R10         \
	MOVQ        R9, v            \
	MOVQ        R10, t           \
	VPUNPCKLQDQ t, v, v

// CLIPC puts v inside base-c .. base+c.
#define CLIPC(v, base, c, t0, t1) \
	VPSUBW  c, base, t0  \
	VPADDW  c, base, t1  \
	VPMAXSW t0, v, v     \
	VPMINSW t1, v, v

// STORE8 saturates a lane to a byte and writes the eight of them.
#define STORE8(v, t, dst) \
	VPACKUSWB v, v, t \
	MOVQ      t, dst

// func deblockStrong8AVX2(p *uint8, pitch int, tc0, tc1, flags int32)
TEXT ·deblockStrong8AVX2(SB), NOSPLIT, $0-28
	MOVQ p+0(FP), AX
	MOVQ pitch+8(FP), R8

	VPMOVZXBW (AX), X0
	LEAQ      (AX)(R8*1), CX
	VPMOVZXBW (CX), X1
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X2
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X3
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X4
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X5
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X6
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X7

	MOVL tc0+16(FP), R9
	MOVL tc1+20(FP), R10
	TCV(X8, X15)
	VPADDW X8, X8, X9

	VPBROADCASTW FOUR, X12
	VPBROADCASTW TWO, X13

	// p0' from p2 + 2*p1 + 2*p0 + 2*q0 + q1 + 4
	VPADDW X2, X3, X10
	VPADDW X4, X10, X10
	VPADDW X10, X10, X10
	VPADDW X1, X10, X10
	VPADDW X5, X10, X10
	VPADDW X12, X10, X10
	VPSRAW $3, X10, X10
	CLIPC(X10, X3, X9, X14, X15)

	// p1' from p2 + p1 + p0 + q0 + 2
	VPADDW X1, X2, X11
	VPADDW X3, X11, X11
	VPADDW X4, X11, X11
	VPADDW X13, X11, X11
	VPSRAW $2, X11, X11
	CLIPC(X11, X2, X9, X14, X15)

	MOVL  flags+24(FP), DX
	TESTL $1, DX
	JNZ   qside

	// p2' from 2*p3 + 3*p2 + p1 + p0 + q0 + 4
	VPADDW X0, X0, X14
	VPADDW X1, X14, X14
	VPADDW X1, X14, X14
	VPADDW X1, X14, X14
	VPADDW X2, X14, X14
	VPADDW X3, X14, X14
	VPADDW X4, X14, X14
	VPADDW X12, X14, X14
	VPSRAW $3, X14, X14
	CLIPC(X14, X1, X9, X8, X15)

	LEAQ (AX)(R8*1), CX
	STORE8(X14, X15, (CX))
	LEAQ (CX)(R8*1), CX
	STORE8(X11, X15, (CX))
	LEAQ (CX)(R8*1), CX
	STORE8(X10, X15, (CX))

qside:
	TESTL $2, DX
	JNZ   done

	// q0' from p1 + 2*p0 + 2*q0 + 2*q1 + q2 + 4
	VPADDW X3, X4, X10
	VPADDW X5, X10, X10
	VPADDW X10, X10, X10
	VPADDW X2, X10, X10
	VPADDW X6, X10, X10
	VPADDW X12, X10, X10
	VPSRAW $3, X10, X10
	CLIPC(X10, X4, X9, X14, X15)

	// q1' from p0 + q0 + q1 + q2 + 2
	VPADDW X3, X4, X11
	VPADDW X5, X11, X11
	VPADDW X6, X11, X11
	VPADDW X13, X11, X11
	VPSRAW $2, X11, X11
	CLIPC(X11, X5, X9, X14, X15)

	// q2' from p0 + q0 + q1 + 3*q2 + 2*q3 + 4
	VPADDW X6, X6, X14
	VPADDW X6, X14, X14
	VPADDW X7, X14, X14
	VPADDW X7, X14, X14
	VPADDW X3, X14, X14
	VPADDW X4, X14, X14
	VPADDW X5, X14, X14
	VPADDW X12, X14, X14
	VPSRAW $3, X14, X14
	CLIPC(X14, X6, X9, X8, X15)

	LEAQ (AX)(R8*4), CX
	STORE8(X10, X15, (CX))
	LEAQ (CX)(R8*1), CX
	STORE8(X11, X15, (CX))
	LEAQ (CX)(R8*1), CX
	STORE8(X14, X15, (CX))

done:
	VZEROUPPER
	RET

// func deblockNormal8AVX2(p *uint8, pitch int, tc0, tc1, nd, flags int32)
TEXT ·deblockNormal8AVX2(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), AX
	MOVQ pitch+8(FP), R8

	LEAQ      (AX)(R8*1), CX
	VPMOVZXBW (CX), X1        // p2
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X2        // p1
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X3        // p0
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X4        // q0
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X5        // q1
	LEAQ      (CX)(R8*1), CX
	VPMOVZXBW (CX), X6        // q2

	MOVL tc0+16(FP), R9
	MOVL tc1+20(FP), R10
	TCV(X8, X15)

	// delta = (9*(q0-p0) - 3*(q1-p1) + 8) >> 4
	VPSUBW       X3, X4, X10
	VPBROADCASTW NINE, X11
	VPMULLW      X11, X10, X10
	VPSUBW       X2, X5, X12
	VPBROADCASTW THREE, X11
	VPMULLW      X11, X12, X12
	VPSUBW       X12, X10, X10
	VPBROADCASTW EIGHT, X11
	VPADDW       X11, X10, X10
	VPSRAW       $4, X10, X10

	// keep only the lanes 8.7.2.5.7 leaves inside ten times tc
	VPABSW       X10, X11
	VPBROADCASTW TEN, X12
	VPMULLW      X12, X8, X12
	VPCMPGTW     X11, X12, X15   // mask = tc*10 > |delta|

	VPXOR   X0, X0, X0
	VPSUBW  X8, X0, X13
	VPMAXSW X13, X10, X10
	VPMINSW X8, X10, X10         // delta clipped to +-tc

	VPSRAW $1, X8, X14           // tc>>1
	VPSUBW X14, X0, X7           // -(tc>>1)

	// the dEp and dEq of each half become the lanes their sample may move in
	MOVL nd+24(FP), BX
	LEAQ ndMask<>(SB), R12

	MOVL  BX, R9
	ANDL  $1, R9
	MOVL  BX, R10
	SHRL  $1, R10
	ANDL  $2, R10
	ORL   R10, R9
	SHLQ  $4, R9
	VMOVDQU (R12)(R9*1), X9
	VPAND X15, X9, X9

	MOVL  BX, R10
	SHRL  $1, R10
	ANDL  $1, R10
	MOVL  BX, R11
	SHRL  $2, R11
	ANDL  $2, R11
	ORL   R11, R10
	SHLQ  $4, R10
	VMOVDQU (R12)(R10*1), X13
	VPAND X15, X13, X13

	MOVL  flags+28(FP), DX
	TESTL $1, DX
	JNZ   nqside

	VPADDW    X10, X3, X11
	VPBLENDVB X15, X11, X3, X11
	LEAQ      (AX)(R8*2), CX
	LEAQ      (CX)(R8*1), CX
	STORE8(X11, X12, (CX))       // p0

	// p1' from ((p2+p0+1)>>1 - p1 + delta) >> 1
	VPADDW       X1, X3, X11
	VPBROADCASTW ONE, X12
	VPADDW       X12, X11, X11
	VPSRAW       $1, X11, X11
	VPSUBW       X2, X11, X11
	VPADDW       X10, X11, X11
	VPSRAW       $1, X11, X11
	VPMAXSW      X7, X11, X11
	VPMINSW      X14, X11, X11
	VPADDW       X2, X11, X11
	VPBLENDVB    X9, X11, X2, X11
	LEAQ         (AX)(R8*2), CX
	STORE8(X11, X12, (CX))       // p1

nqside:
	TESTL $2, DX
	JNZ   ndone

	VPSUBW    X10, X4, X11
	VPBLENDVB X15, X11, X4, X11
	LEAQ      (AX)(R8*4), CX
	STORE8(X11, X12, (CX))       // q0

	// q1' from ((q2+q0+1)>>1 - q1 - delta) >> 1
	VPADDW       X6, X4, X11
	VPBROADCASTW ONE, X12
	VPADDW       X12, X11, X11
	VPSRAW       $1, X11, X11
	VPSUBW       X5, X11, X11
	VPSUBW       X10, X11, X11
	VPSRAW       $1, X11, X11
	VPMAXSW      X7, X11, X11
	VPMINSW      X14, X11, X11
	VPADDW       X5, X11, X11
	VPBLENDVB    X13, X11, X5, X11
	LEAQ         (AX)(R8*4), CX
	LEAQ         (CX)(R8*1), CX
	STORE8(X11, X12, (CX))       // q1

ndone:
	VZEROUPPER
	RET

// TRANSPOSE8 turns eight rows of eight bytes in the low half of X0 through X7
// into eight columns, two to a register, in X8 through X11.
#define TRANSPOSE8() \
	VPUNPCKLBW X1, X0, X8    \
	VPUNPCKLBW X3, X2, X9    \
	VPUNPCKLBW X5, X4, X10   \
	VPUNPCKLBW X7, X6, X11   \
	VPUNPCKLWD X9, X8, X12   \
	VPUNPCKHWD X9, X8, X13   \
	VPUNPCKLWD X11, X10, X14 \
	VPUNPCKHWD X11, X10, X15 \
	VPUNPCKLDQ X14, X12, X8  \
	VPUNPCKHDQ X14, X12, X9  \
	VPUNPCKLDQ X15, X13, X10 \
	VPUNPCKHDQ X15, X13, X11

// func turnIn8AVX2(dst *uint8, src *uint8, stride int)
TEXT ·turnIn8AVX2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ stride+16(FP), R8

	MOVQ (SI), X0
	MOVQ (SI)(R8*1), X1
	LEAQ (SI)(R8*2), CX
	MOVQ (CX), X2
	MOVQ (CX)(R8*1), X3
	LEAQ (CX)(R8*2), CX
	MOVQ (CX), X4
	MOVQ (CX)(R8*1), X5
	LEAQ (CX)(R8*2), CX
	MOVQ (CX), X6
	MOVQ (CX)(R8*1), X7

	TRANSPOSE8()

	VMOVDQU X8, (DI)
	VMOVDQU X9, 16(DI)
	VMOVDQU X10, 32(DI)
	VMOVDQU X11, 48(DI)

	VZEROUPPER
	RET

// func turnOut8AVX2(dst *uint8, stride int, src *uint8)
TEXT ·turnOut8AVX2(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ stride+8(FP), R8
	MOVQ src+16(FP), SI

	MOVQ (SI), X0
	MOVQ 8(SI), X1
	MOVQ 16(SI), X2
	MOVQ 24(SI), X3
	MOVQ 32(SI), X4
	MOVQ 40(SI), X5
	MOVQ 48(SI), X6
	MOVQ 56(SI), X7

	TRANSPOSE8()

	MOVQ    X8, (DI)
	VPEXTRQ $1, X8, R9
	MOVQ    R9, (DI)(R8*1)
	LEAQ    (DI)(R8*2), CX
	MOVQ    X9, (CX)
	VPEXTRQ $1, X9, R9
	MOVQ    R9, (CX)(R8*1)
	LEAQ    (CX)(R8*2), CX
	MOVQ    X10, (CX)
	VPEXTRQ $1, X10, R9
	MOVQ    R9, (CX)(R8*1)
	LEAQ    (CX)(R8*2), CX
	MOVQ    X11, (CX)
	VPEXTRQ $1, X11, R9
	MOVQ    R9, (CX)(R8*1)

	VZEROUPPER
	RET
