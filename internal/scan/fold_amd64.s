#include "textflag.h"

// The lane constants live in read-only data because SSE2 has no way to broadcast
// an immediate into a vector register.
DATA topBit<>+0(SB)/8, $0x8080808080808080
DATA topBit<>+8(SB)/8, $0x8080808080808080
GLOBL topBit<>(SB), RODATA|NOPTR, $16

DATA addA<>+0(SB)/8, $0x3f3f3f3f3f3f3f3f
DATA addA<>+8(SB)/8, $0x3f3f3f3f3f3f3f3f
GLOBL addA<>(SB), RODATA|NOPTR, $16

DATA addZ<>+0(SB)/8, $0x2525252525252525
DATA addZ<>+8(SB)/8, $0x2525252525252525
GLOBL addZ<>(SB), RODATA|NOPTR, $16

// foldASCIIBlocks lowercases whole sixteen-byte blocks of b, stopping at the
// first block that holds a byte above ASCII, and returns how many bytes it
// wrote. See fold.go for the lane arithmetic; this is that, sixteen lanes wide.
//
// SSE2 only, with no runtime detection: it is baseline for every amd64 Go
// targets, so there is no CPU feature to test and no second dependency to take
// on for testing it. Two spellings are worth knowing. PSRLW shifts sixteen-bit
// words rather than bytes, which is still right here because the only bit set in
// each byte is the top one, so 0x8080 shifts to 0x2020 with nothing crossing a
// byte. And PMOVMSKB gathers the top bit of all sixteen bytes into one integer
// register, which is the cheapest "does this block hold a rune" there is.
//
// func foldASCIIBlocks(b []byte) int
TEXT ·foldASCIIBlocks(SB), NOSPLIT, $0-32
	MOVQ	b_base+0(FP), SI
	MOVQ	b_len+8(FP), CX
	XORQ	AX, AX			// bytes written so far
	MOVOU	topBit<>(SB), X5
	MOVOU	addA<>(SB), X6
	MOVOU	addZ<>(SB), X7

loop:
	MOVQ	CX, BX
	SUBQ	AX, BX
	CMPQ	BX, $16
	JLT	done
	MOVOU	(SI)(AX*1), X0

	// Leave a block holding a rune to the caller's scalar path: the lane adds
	// below are only valid where the top bit starts clear.
	PMOVMSKB X0, DX
	TESTL	DX, DX
	JNZ	done

	MOVOU	X0, X1
	PADDB	X6, X1			// top bit set where the lane is >= 'A'
	MOVOU	X0, X2
	PADDB	X7, X2			// top bit set where the lane is  > 'Z'
	PANDN	X1, X2			// X2 = NOT X2 AND X1: the letters, plus noise
	PAND	X5, X2			// keep only the top bit
	PSRLW	$2, X2			// 0x80 becomes 0x20
	POR	X2, X0
	MOVOU	X0, (SI)(AX*1)

	ADDQ	$16, AX
	JMP	loop

done:
	MOVQ	AX, ret+24(FP)
	RET
