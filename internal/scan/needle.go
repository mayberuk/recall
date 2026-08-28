package scan

import "bytes"

// byteWeight is roughly how often a byte turns up in the text this tool
// searches, on a scale where 1000 is the commonest byte there is. It exists to
// answer one question per needle — which of its bytes is the least likely to
// appear — so only the ordering matters, not the values.
//
// The letter figures are ordinary English relative frequencies. Everything else
// is set from what a session transcript actually holds: it is prose about code,
// so a space or a newline is the commonest byte of all, and digits and the
// punctuation that appears in identifiers and paths are commoner than the rarest
// letters but rarer than the common ones.
var byteWeight = func() [256]uint16 {
	var w [256]uint16
	// A byte no rule below covers is rare by default. Guessing high here would be
	// the costly mistake: it would rule out exactly the bytes worth searching for.
	for i := range w {
		w[i] = 5
	}
	letters := map[byte]uint16{
		'e': 127, 't': 91, 'a': 82, 'o': 75, 'i': 70, 'n': 67, 's': 63, 'h': 61,
		'r': 60, 'd': 43, 'l': 40, 'c': 28, 'u': 28, 'm': 24, 'w': 24, 'f': 22,
		'g': 20, 'y': 20, 'p': 19, 'b': 15, 'v': 10, 'k': 8, 'j': 2, 'x': 2,
		'q': 1, 'z': 1,
	}
	for b, f := range letters {
		w[b] = f
		// A needle is folded before it gets here, so an upper-case byte should
		// never be looked up. Weighting it the same as its lower-case form keeps a
		// caller that skipped the fold merely slow rather than wrong.
		w[b-'a'+'A'] = f
	}
	for _, b := range []byte(" \n\t") {
		w[b] = 1000
	}
	for b := byte('0'); b <= '9'; b++ {
		w[b] = 30
	}
	for _, b := range []byte(".,;:'\"()[]{}/\\-_=<>*#") {
		w[b] = 30
	}
	return w
}()

// rarestByte is the index of the byte in n least likely to appear in the corpus.
// Searching for that byte instead of the first one is what makes a needle like
// "escape" cheap: the stdlib scans for the first byte, and a needle opening with
// a common letter false-positives on nearly every word, paying for a verification
// pass each time.
//
// Ties go to the earliest index, which makes the choice a function of the needle
// alone. A search that picked differently between two runs would be a search
// whose cost nobody can reason about.
func rarestByte(n []byte) int {
	best := 0
	for i := 1; i < len(n); i++ {
		if byteWeight[n[i]] < byteWeight[n[best]] {
			best = i
		}
	}
	return best
}

// indexNeedle is bytes.Index with the scan anchored on n[at] rather than n[0].
// at must be a valid index into n, which rarestByte guarantees for a non-empty
// needle.
//
// An anchor of zero hands the search back to bytes.Index, which is nothing to
// gain and something to lose otherwise: that function is hand-written assembly on
// the architectures this runs on, and when its own anchor — the first byte — is
// already the rarest one, the loop below is a slower way to the same answer.
// Forcing the loop on rare-first needles cost between 1% and 11% over the medium
// generated corpus, depending on the query shape.
//
// The empty needle matches at 0, as bytes.Index has it: a query with no terms is
// rejected before it reaches here, so this only keeps the two functions
// interchangeable.
func indexNeedle(h, n []byte, at int) int {
	if at == 0 || len(n) < 2 {
		return bytes.Index(h, n)
	}
	if len(n) > len(h) {
		return -1
	}
	// A match starting at i puts n[at] at i+at, and i runs to len(h)-len(n), so
	// the anchor byte can only be in this window. Slicing to it is what keeps the
	// IndexByte scan from finding anchors no match could be built around.
	lo, hi := at, len(h)-len(n)+at+1
	for lo < hi {
		j := bytes.IndexByte(h[lo:hi], n[at])
		if j < 0 {
			return -1
		}
		found := lo + j
		start := found - at
		if bytes.Equal(h[start:start+len(n)], n) {
			return start
		}
		lo = found + 1
	}
	return -1
}

// found reports whether folded carries this term, through the needle the
// caller typed or any needle substituted for it. The zero-anchor case is
// spelled out here rather than left to indexNeedle, since this runs once per
// term per turn; alt, checked only when non-empty, is nil on every hit.
func (t *term) found(folded []byte) bool {
	if t.rare == 0 {
		if bytes.Contains(folded, t.needle) {
			return true
		}
	} else if indexNeedle(folded, t.needle, t.rare) >= 0 {
		return true
	}
	if len(t.alt) == 0 {
		return false
	}
	return t.altFound(folded)
}

func (t *term) altFound(folded []byte) bool {
	for i := range t.alt {
		if indexAt(folded, t.alt[i].needle, t.alt[i].rare) >= 0 {
			return true
		}
	}
	return false
}

// index is where this term first occurs in folded, or -1. The earliest needle
// wins, so a caller walking forward from one match never steps over another.
func (t *term) index(folded []byte) int {
	best := indexAt(folded, t.needle, t.rare)
	for i := range t.alt {
		if at := indexAt(folded, t.alt[i].needle, t.alt[i].rare); at >= 0 && (best < 0 || at < best) {
			best = at
		}
	}
	return best
}

// indexAt is one needle's first occurrence, taking the same zero-anchor
// shortcut found does and for the same reason.
func indexAt(folded, needle []byte, rare int) int {
	if rare == 0 {
		return bytes.Index(folded, needle)
	}
	return indexNeedle(folded, needle, rare)
}
