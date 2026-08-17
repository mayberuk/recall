package scan

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// foldReference is fold done a byte at a time — the implementation that shipped
// before the word-at-a-time one, kept as the thing every faster version has to
// agree with. Assembly for this is planned; when it lands it compares against
// this, not against the SWAR path, so a shared misreading cannot pass.
func foldReference(dst []byte, s string) []byte {
	dst = append(dst[:0], s...)
	for i := 0; i < len(dst); i++ {
		c := dst[i]
		if c < utf8.RuneSelf {
			if 'A' <= c && c <= 'Z' {
				dst[i] = c + 'a' - 'A'
			}
			continue
		}
		r, size := utf8.DecodeRune(dst[i:])
		if lower := unicode.ToLower(r); lower != r && utf8.RuneLen(lower) == size {
			utf8.EncodeRune(dst[i:], lower)
		}
		i += size - 1
	}
	return dst
}

// foldCases are the inputs worth naming: the boundaries of the ASCII range the
// lane arithmetic tests for, the word boundary the bulk loop steps over, and the
// Unicode shapes the byte path has to keep its hands off.
func foldCases() []string {
	var out []string
	add := func(s ...string) { out = append(out, s...) }

	add("", "a", "A", "az", "AZ", "aZ")
	// 0x40 and 0x5b sit either side of 'A'..'Z'. A range test off by one moves
	// '@' to a backtick or '[' to '{', and nothing else in ASCII would show it.
	add("@", "[", "@A", "Z[", "@AZ[", "?@AZ[\\")
	add(strings.Repeat("@[", 8), strings.Repeat("AZ", 8))

	// Every length either side of the eight-byte step, in both cases, so a bulk
	// loop that skipped or double-counted a word shows up as a wrong byte.
	for n := range 20 {
		add(strings.Repeat("A", n), strings.Repeat("Ab", n), strings.Repeat("aB@[", n))
	}
	// A non-ASCII byte at every offset within and across a word.
	for n := range 20 {
		add(strings.Repeat("A", n) + "É" + strings.Repeat("B", 19-n))
	}

	add(
		"É",       // two bytes, lowers to é at the same width
		"Ç",       // same
		"İ",       // U+0130 lowers to two runes; different width, so left alone
		"K",       // U+212A Kelvin lowers to ASCII k; three bytes to one, left alone
		"Σ", "ΣΣ", // Greek, same width
		"ǅ",            // a titlecase rune
		"\xff",         // not UTF-8 at all
		"\xc3",         // a truncated two-byte sequence
		"A\xffZ",       // invalid between two foldable bytes
		"\xed\xa0\x80", // a surrogate half, which DecodeRune rejects
		"ABCÉDEFİGHI\xffJKL",
		strings.Repeat("Mixed CASE Ünïcödé 123 ", 7),
	)
	return out
}

// TestFoldMatchesTheByteAtATimeReference is the whole correctness argument for
// widening fold: not that it looks right, but that it is indistinguishable from
// the implementation it replaced.
func TestFoldMatchesTheByteAtATimeReference(t *testing.T) {
	var got, want []byte
	for _, s := range foldCases() {
		got = fold(got, s)
		want = foldReference(want, s)
		if !bytes.Equal(got, want) {
			t.Errorf("fold(%q)\n  = %q\n  want %q", s, got, want)
		}
	}
}

// TestFoldPreservesEveryBytePosition pins the invariant two other things depend
// on. A hit's offset is an index into the unfolded text, so a renderer
// highlights the wrong span if a fold moves a byte; and the suggestion pass
// budgets by a turn's text length precisely because that is its folded length,
// so a fold that changed length would silently move where the budget runs out.
func TestFoldPreservesEveryBytePosition(t *testing.T) {
	var buf []byte
	for _, s := range foldCases() {
		if buf = fold(buf, s); len(buf) != len(s) {
			t.Errorf("fold(%q) is %d bytes, want %d", s, len(buf), len(s))
		}
	}
}

// TestFoldReusesItsBuffer keeps the scan's one allocation per goroutine from
// becoming one per turn: every caller folds turn after turn into the same slice.
func TestFoldReusesItsBuffer(t *testing.T) {
	buf := fold(nil, strings.Repeat("A", 4096))
	before := cap(buf)
	for _, s := range foldCases() {
		buf = fold(buf, s)
	}
	if cap(buf) != before {
		t.Errorf("capacity moved from %d to %d; fold reallocated on an input that fits", before, cap(buf))
	}
}

// FuzzFold looks for the disagreement the named cases miss. The seeds are the
// shapes above plus whatever the corpus of a previous run turned up.
func FuzzFold(f *testing.F) {
	for _, s := range foldCases() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, want := fold(nil, s), foldReference(nil, s)
		if !bytes.Equal(got, want) {
			t.Fatalf("fold(%q)\n  = %q\n  want %q", s, got, want)
		}
	})
}

// BenchmarkFold reports what the scan's hottest loop costs per byte, for the
// shipping implementation and for the byte-at-a-time reference side by side —
// the claim in docs/design.md is a ratio between these two, and a ratio nobody
// can reproduce is a number nobody should trust.
//
// The sizes bracket a turn: a prompt, an ordinary assistant answer, and an
// injected summary. Mostly-ASCII is the real distribution, since a transcript is
// prose about code; the all-ASCII and heavily-Unicode rows are here so that a
// regression on either path cannot hide behind the average, which is exactly how
// the first draft of this shipped 13% slower on Unicode.
//
// Both go through a func value so neither gets an inlining advantage the other
// does not. That costs both of them a call and makes the absolute figures
// slightly pessimistic; the ratio is what this is for.
func BenchmarkFold(b *testing.B) {
	// vector and swar are the same code path on an architecture with no
	// hand-written routine, and will report the same figure there.
	impls := []struct {
		name  string
		floor int
		fn    func([]byte, string) []byte
	}{
		{"vector", 16, fold},
		{"swar", math.MaxInt, fold},
		{"reference", math.MaxInt, foldReference},
	}
	kinds := []struct {
		name string
		unit string
	}{
		{"ascii", "return the walk value at the offset FOR THE READER "},
		{"mostly-ascii", "return the walk vàlue at the offset FOR THE READER "},
		{"unicode", "rétürn thé wälk vâlue át thé öffsét FÖR THÉ RÉADÉR "},
	}
	for _, size := range []int{64, 1024, 20480} {
		for _, kind := range kinds {
			s := strings.Repeat(kind.unit, size/len(kind.unit)+1)[:size]
			for _, impl := range impls {
				b.Run(fmt.Sprintf("%s/%dB/%s", kind.name, size, impl.name), func(b *testing.B) {
					was := minVectorBytes
					minVectorBytes = impl.floor
					b.Cleanup(func() { minVectorBytes = was })
					var buf []byte
					b.SetBytes(int64(len(s)))
					for range b.N {
						buf = impl.fn(buf, s)
					}
				})
			}
		}
	}
}

// TestFoldASCIIBlocksKeepsItsContract checks the assembly against the promise
// fold relies on, separately from checking the whole fold: it writes only whole
// sixteen-byte blocks, it never writes past what it reports, and it stops at a
// block holding a rune rather than corrupting one — the lane adds are only valid
// where the top bit starts clear, so a routine that ran on past a rune would
// produce plausible garbage instead of failing.
func TestFoldASCIIBlocksKeepsItsContract(t *testing.T) {
	for _, s := range foldCases() {
		b := []byte(s)
		want := foldReference(nil, s)
		n := foldASCIIBlocks(b)

		if n < 0 || n > len(b) {
			t.Fatalf("%q: reported %d bytes of %d", s, n, len(b))
		}
		if n%16 != 0 {
			t.Errorf("%q: reported %d bytes, which is not whole blocks", s, n)
		}
		if !bytes.Equal(b[:n], want[:n]) {
			t.Errorf("%q: folded %q, want %q", s, b[:n], want[:n])
		}
		if !bytes.Equal(b[n:], []byte(s)[n:]) {
			t.Errorf("%q: wrote past the %d bytes it reported", s, n)
		}
		if !foldHasAssembly {
			continue
		}
		// Where the routine exists, stopping short has to have a reason: either
		// fewer than sixteen bytes are left, or the next block holds a rune.
		if rest := b[n:]; len(rest) >= 16 {
			block := rest[:16]
			ascii := true
			for _, c := range block {
				if c >= utf8.RuneSelf {
					ascii = false
					break
				}
			}
			if ascii {
				t.Errorf("%q: stopped at %d with a whole ASCII block still there", s, n)
			}
		}
	}
}
