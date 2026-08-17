package scan

import (
	"encoding/binary"
	"unicode"
	"unicode/utf8"
)

// Lane arithmetic over eight bytes at a time. Every constant here is chosen so
// that a lane cannot carry into its neighbour, which is what makes one 64-bit
// add stand in for eight independent byte comparisons:
//
//	ones     puts a value in every lane
//	highBits is the lane's top bit, the only bit the comparisons read
//	aboveA   lifts 'A' to the top bit, so a lane holding 'A' or more overflows
//	aboveZ   does the same one past 'Z'
//
// Both addends are applied only to lanes already known to be ASCII, so the
// largest lane value is 0x7f + 0x3f = 0xbe and nothing crosses a byte boundary.
const (
	ones     = 0x0101010101010101
	highBits = 0x8080808080808080
	aboveA   = (0x80 - 'A') * ones
	aboveZ   = (0x80 - 'Z' - 1) * ones
	toLower  = 'a' - 'A' // 0x20, which is highBits >> 2 in every lane
)

// declineBudget is how many times fold may ask the vector routine for a block
// and be refused before it stops asking for the rest of this text. A refusal
// means a rune sits inside the next sixteen bytes, and text whose ASCII runs are
// that short pays for the call and never gets a block folded — measured at 9-11%
// on accented prose, which is the one shape the wide path made worse.
//
// It is a budget rather than a single strike because one rune early in an
// otherwise long run would otherwise cost the whole rest of the turn its wide
// path, and the real corpus averages 328 bytes between runes.
const declineBudget = 4

// minVectorBytes is the shortest ASCII run worth handing to the hand-written
// vector routine. A benchmark raises it past any input so it can time the
// word-at-a-time path against the vector one in a single interleaved run;
// nothing else writes it.
var minVectorBytes = 16

// fold lowercases s into dst for case-insensitive matching, preserving every
// byte position so a hit offset locates the match in the original text.
//
// A rune whose lowercase form is a different width is left alone rather than
// folded: Unicode's few width-changing cases are worth less than offsets that
// can be trusted, and a renderer highlights by offset without re-searching.
//
// This is the hot loop of the whole tool — 87% of a scan's time before it was
// widened, because the substring search it feeds is hand-written assembly in the
// standard library and this was a byte at a time.
// The multi-byte rune is tested for first, and nothing is done ahead of it, so
// that text which is mostly not ASCII costs exactly what it did before this was
// widened. Two earlier arrangements both put work in front of that branch — a
// helper call in one, a word load in the other — and each made a corpus written
// in a language outside ASCII slower than it had been, by 13% and then by 5%.
func fold(dst []byte, s string) []byte {
	dst = append(dst[:0], s...)
	declines := 0
	for i := 0; i < len(dst); {
		if dst[i] >= utf8.RuneSelf {
			r, size := utf8.DecodeRune(dst[i:])
			if lower := unicode.ToLower(r); lower != r && utf8.RuneLen(lower) == size {
				utf8.EncodeRune(dst[i:], lower)
			}
			i += size
			continue
		}
		// Sixteen bytes to the round where there is hand-written vector code for
		// it. The length test keeps the call off short runs, which is where a call
		// that can only decline would cost more than it saves.
		if declines < declineBudget && len(dst)-i >= minVectorBytes {
			if n := foldASCIIBlocks(dst[i:]); n > 0 {
				i += n
			} else {
				declines++
			}
		}

		// An ASCII run: eight bytes to the add for as long as they last. This
		// stays a tight loop of its own rather than folding into the loop above,
		// because routing every word through the outer test cost 40% on ASCII.
		for len(dst)-i >= 8 {
			w := binary.LittleEndian.Uint64(dst[i:])
			if w&highBits != 0 {
				break
			}
			// A lane's top bit survives both adds exactly when it holds 'A'
			// through 'Z'; shifting that bit down by two is the 0x20 that
			// lowercases it.
			if m := (w + aboveA) &^ (w + aboveZ) & highBits; m != 0 {
				binary.LittleEndian.PutUint64(dst[i:], w|m>>2)
			}
			i += 8
		}
		// What no word covers: the last seven bytes, and the ASCII head of a word
		// whose tail holds a rune. Folding that head from the word already loaded
		// was tried — the lanes below the first non-ASCII one are still correct,
		// because an add carries upward and never down — and it was slower on every
		// shape measured, so the word is simply dropped and walked instead.
		for i < len(dst) && dst[i] < utf8.RuneSelf {
			if c := dst[i]; 'A' <= c && c <= 'Z' {
				dst[i] = c + toLower
			}
			i++
		}
	}
	return dst
}
