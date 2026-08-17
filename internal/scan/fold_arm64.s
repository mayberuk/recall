#include "textflag.h"

// foldASCIIBlocks lowercases whole sixteen-byte blocks of b, stopping at the
// first block that holds a byte above ASCII, and returns how many bytes it
// wrote. See fold.go for the lane arithmetic; this is that, sixteen lanes wide.
//
// Go's arm64 assembler has no unsigned vector compare before 1.27 and no VBIC
// or VUMAXV at all, so two things are spelled the long way: `a AND NOT b` is
// `(a XOR b) AND a`, and "does any lane exceed ASCII" moves both halves of the
// masked vector into general registers and ORs them.
//
// func foldASCIIBlocks(b []byte) int
TEXT ·foldASCIIBlocks(SB), NOSPLIT, $0-32
	MOVD	b_base+0(FP), R0
	MOVD	b_len+8(FP), R1
	MOVD	$0, R2			// bytes written so far
	VMOVI	$0x80, V4.B16		// the lane's top bit
	VMOVI	$0x3f, V5.B16		// 0x80 - 'A'
	VMOVI	$0x25, V6.B16		// 0x80 - 'Z' - 1

loop:
	SUB	R2, R1, R3
	CMP	$16, R3
	BLT	done
	ADD	R2, R0, R4
	VLD1	(R4), [V0.B16]

	// Leave a block holding a rune to the caller's scalar path: the lane adds
	// below are only valid where the top bit starts clear.
	VAND	V4.B16, V0.B16, V7.B16
	VMOV	V7.D[0], R5
	VMOV	V7.D[1], R6
	ORR	R6, R5, R5
	CBNZ	R5, done

	VADD	V0.B16, V5.B16, V1.B16	// top bit set where the lane is >= 'A'
	VADD	V0.B16, V6.B16, V2.B16	// top bit set where the lane is  > 'Z'
	VEOR	V1.B16, V2.B16, V3.B16
	VAND	V1.B16, V3.B16, V3.B16	// V1 AND NOT V2: the letters, plus noise
	VAND	V4.B16, V3.B16, V3.B16	// keep only the top bit
	VUSHR	$2, V3.B16, V3.B16	// 0x80 becomes 0x20
	VORR	V3.B16, V0.B16, V0.B16
	VST1	[V0.B16], (R4)

	ADD	$16, R2, R2
	B	loop

done:
	MOVD	R2, ret+24(FP)
	RET
