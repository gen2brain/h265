//go:build amd64 && !noasm

#include "textflag.h"

// cpuidAVX2 reports whether the CPU has AVX2 and the OS saves the YMM state.
TEXT ·cpuidAVX2(SB), NOSPLIT, $0-1
	MOVL $0, AX
	CPUID
	CMPL AX, $7
	JL   no

	MOVL $1, AX
	MOVL $0, CX
	CPUID
	BTL  $27, CX // OSXSAVE
	JNC  no

	MOVL   $0, CX
	XGETBV
	ANDL   $6, AX // XMM and YMM state
	CMPL   AX, $6
	JNE    no

	MOVL $7, AX
	MOVL $0, CX
	CPUID
	BTL  $5, BX // AVX2
	JNC  no

	MOVB $1, ret+0(FP)
	RET

no:
	MOVB $0, ret+0(FP)
	RET

// cpuidAVX512ICL reports whether the CPU has the Ice Lake AVX-512 feature set
// and the OS saves the ZMM state.
TEXT ·cpuidAVX512ICL(SB), NOSPLIT, $0-1
	MOVL $0, AX
	CPUID
	CMPL AX, $7
	JL   noicl

	MOVL $1, AX
	MOVL $0, CX
	CPUID
	ANDL $0x18000000, CX // OSXSAVE and AVX
	CMPL CX, $0x18000000
	JNE  noicl

	MOVL   $0, CX
	XGETBV
	MOVL   AX, DX
	ANDL   $6, AX    // XMM and YMM state
	CMPL   AX, $6
	JNE    noicl
	ANDL   $0xe0, DX // opmask, ZMM_Hi256 and Hi16_ZMM
	CMPL   DX, $0xe0
	JNE    noicl

	MOVL $7, AX
	MOVL $0, CX
	CPUID
	ANDL $0xd0230000, BX // F, DQ, IFMA, CD, BW, VL
	CMPL BX, $0xd0230000
	JNE  noicl
	ANDL $0x00005f42, CX // VBMI, VBMI2, GFNI, VAES, VPCLMULQDQ, VNNI, BITALG, VPOPCNTDQ
	CMPL CX, $0x00005f42
	JNE  noicl

	MOVB $1, ret+0(FP)
	RET

noicl:
	MOVB $0, ret+0(FP)
	RET
