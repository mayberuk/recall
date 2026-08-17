package scan

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestIndexNeedleAgreesWithTheStandardLibraryAtEveryAnchor is the correctness
// argument for anchoring the scan on a byte other than the first. The property
// is stated for every valid anchor rather than for the one rarestByte picks, so
// changing the selection rule cannot quietly change what a search finds.
//
// The alphabet is four bytes wide on purpose: a needle and a haystack drawn from
// it collide constantly, which is the case that separates a correct verification
// step from one that stops at the anchor byte.
func TestIndexNeedleAgreesWithTheStandardLibraryAtEveryAnchor(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	alphabet := []byte("abz ")
	draw := func(n int) []byte {
		out := make([]byte, n)
		for i := range out {
			out[i] = alphabet[rnd.Intn(len(alphabet))]
		}
		return out
	}

	checked := 0
	for range 20000 {
		h := draw(rnd.Intn(48))
		n := draw(1 + rnd.Intn(6))
		want := bytes.Index(h, n)
		for at := range n {
			if got := indexNeedle(h, n, at); got != want {
				t.Fatalf("indexNeedle(%q, %q, %d) = %d, bytes.Index = %d", h, n, at, got, want)
			}
			checked++
		}
		tm := term{needle: n, rare: rarestByte(n)}
		if got := tm.index(h); got != want {
			t.Fatalf("term{%q, rare %d}.index(%q) = %d, bytes.Index = %d", n, tm.rare, h, got, want)
		}
		if got := tm.found(h); got != (want >= 0) {
			t.Fatalf("term{%q}.found(%q) = %v, want %v", n, h, got, want >= 0)
		}
	}
	t.Logf("%d anchored searches agreed with bytes.Index", checked)
}

// TestIndexNeedleHandlesTheEdgesOfTheBuffer covers the cases the random draw
// reaches rarely or never: a match flush against either end, a needle longer
// than the haystack, and the empty inputs.
func TestIndexNeedleHandlesTheEdgesOfTheBuffer(t *testing.T) {
	for _, c := range []struct {
		name string
		h, n string
		want int
	}{
		{"match at the very start", "value return", "value", 0},
		{"match at the very end", "return value", "value", 7},
		{"whole haystack", "value", "value", 0},
		{"needle longer than haystack", "val", "value", -1},
		{"empty haystack", "", "value", -1},
		{"empty needle", "value", "", 0},
		{"empty both", "", "", 0},
		{"single byte present", "value", "l", 2},
		{"single byte absent", "value", "z", -1},
		{"anchor byte present but no match", "zzz", "az", -1},
		{"repeated anchor before the match", "zz az", "az", 3},
		{"overlapping candidates", "aaab", "aab", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, n := []byte(c.h), []byte(c.n)
			for at := range max(len(n), 1) {
				if got := indexNeedle(h, n, at); got != c.want {
					t.Errorf("indexNeedle(%q, %q, %d) = %d, want %d", c.h, c.n, at, got, c.want)
				}
			}
			// The methods are what production calls, and they choose between the
			// standard library and the anchored loop on their own.
			tm := term{needle: n, rare: rarestByte(n)}
			if got := tm.found(h); got != (c.want >= 0) {
				t.Errorf("term{%q}.found(%q) = %v, want %v", c.n, c.h, got, c.want >= 0)
			}
			if got := tm.index(h); got != c.want {
				t.Errorf("term{%q}.index(%q) = %d, want %d", c.n, c.h, got, c.want)
			}
		})
	}
}

func TestRarestByteAnchorsOnTheLeastCommonByteOfTheNeedle(t *testing.T) {
	for _, c := range []struct {
		needle string
		want   int // index, chosen because the byte there is the rarest
	}{
		{"escape", 4},       // p, at 19, against e 127 · s 63 · c 28 · a 82
		{"analysis", 4},     // y, at 20, the only letter under l's 40
		{"zero", 0},         // z, at 1
		{"the", 1},          // h, at 61, against t 91 and e 127
		{"quick", 0},        // q, at 1
		{"a", 0},            // a single byte has one choice
		{"wallet queue", 7}, // q, not the space at index 6 — the commonest byte there is
	} {
		t.Run(c.needle, func(t *testing.T) {
			if got := rarestByte([]byte(c.needle)); got != c.want {
				t.Errorf("rarestByte(%q) = %d (byte %q, weight %d), want %d (byte %q, weight %d)",
					c.needle, got, c.needle[got], byteWeight[c.needle[got]],
					c.want, c.needle[c.want], byteWeight[c.needle[c.want]])
			}
		})
	}
}

// TestNoByteIsWeightedAboveASpace guards the one weighting mistake that would
// silently undo the optimization: anchoring on the commonest byte in the corpus
// means an IndexByte hit on nearly every word and a verification pass for each.
func TestNoByteIsWeightedAboveASpace(t *testing.T) {
	for b := range 256 {
		if byteWeight[b] > byteWeight[' '] {
			t.Errorf("byte %q is weighted %d, above the space's %d", byte(b), byteWeight[b], byteWeight[' '])
		}
	}
}

// FuzzIndexNeedle lets the fuzzer look for the disagreement the seeded draw
// above might miss. Every asm or unsafe path in this repo pairs with a fuzz
// target comparing it against the reference implementation; this is pure Go and
// gets the same treatment, because the reference here is the standard library
// and the comparison is free.
func FuzzIndexNeedle(f *testing.F) {
	f.Add([]byte("value return path"), []byte("return"), 0)
	f.Add([]byte("aaab"), []byte("aab"), 2)
	f.Add([]byte(""), []byte(""), 0)
	f.Fuzz(func(t *testing.T, h, n []byte, at int) {
		if len(n) == 0 {
			at = 0
		} else {
			// A negative or out-of-range anchor is a programming error, not an
			// input: rarestByte only ever returns an index into the needle.
			at = ((at % len(n)) + len(n)) % len(n)
		}
		if got, want := indexNeedle(h, n, at), bytes.Index(h, n); got != want {
			t.Fatalf("indexNeedle(%q, %q, %d) = %d, bytes.Index = %d", h, n, at, got, want)
		}
	})
}
