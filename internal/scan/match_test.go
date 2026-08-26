package scan

import (
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// classify reads raw for the case transitions fold has already erased from
// folded. These cases exercise it directly, without a full turn: a plain
// lowercase run needs no case signal to classify correctly, and case only
// changes the answer when the original text actually carries it.
func TestClassifyReadsCaseFromRawNotFolded(t *testing.T) {
	cases := []struct {
		name           string
		raw            string
		offset, length int
		want           schema.MatchKind
	}{
		// "limiter" is the identifier's second segment: raw carries the hump
		// fold erased, so the interior match becomes a whole word.
		{"non-leading segment, raw has the hump", "rateLimiter", 4, 7, schema.MatchWord},
		// Same span, but the source text was never camelCase to begin with —
		// there is no hump in raw for classify to find, so the old answer
		// holds. This is the negative control for the case above: the rule
		// fires on the identifier's actual case, not on where the term sits.
		{"same span, no case in raw", "ratelimiter", 4, 7, schema.MatchInside},
		// "no" inside "know" crosses no case boundary and stays interior —
		// the docstring's own example, unaffected by this change.
		{"interior substring, no transition at either edge", "know", 1, 2, schema.MatchInside},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			folded := fold(nil, tc.raw)
			if got := classify(folded, raw, tc.offset, tc.length); got != tc.want {
				t.Errorf("classify(%q, %d, %d) = %q, want %q", tc.raw, tc.offset, tc.length, got, tc.want)
			}
		})
	}
}

// caseBoundary is classify's raw-side test in isolation: each of the four
// rules in the phase's action block, plus the guards that keep it inert
// outside ASCII and off invalid indices.
func TestCaseBoundary(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		i    int
		want bool
	}{
		{"camel hump: lower then upper", "eL", 1, true},
		{"camel hump: digit then upper", "2L", 1, true},
		// The HTTPServer case: an acronym gives its last letter back to a
		// word only when the letter after it is lower-case.
		{"acronym to word: upper, upper, lower follows", "PSe", 1, true},
		// Without a lower-case follower this is still inside the acronym —
		// the condition a mutation dropping the look-ahead would break.
		{"all-caps run: upper, upper, upper follows", "ABC", 1, false},
		{"letter after digit", "2t", 1, true},
		{"digit after letter", "t2", 1, true},
		{"no transition: two lower-case letters", "no", 1, false},
		{"start of buffer: no byte to compare against", "ab", 0, false},
		{"end of buffer: no byte at i", "ab", 2, false},
		// The last byte of "é" (a UTF-8 continuation byte) sits where a lead
		// byte would; classify must not read case there and instead leaves
		// the boundary to the existing wordByte test.
		{"byte before i is part of a multi-byte rune", "caféBar", 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := caseBoundary([]byte(tc.raw), tc.i); got != tc.want {
				t.Errorf("caseBoundary(%q, %d) = %v, want %v", tc.raw, tc.i, got, tc.want)
			}
		})
	}
}

// FuzzClassifyAgreesWithASafeCopy is rawBytes's correctness argument. Every
// asm or unsafe path in this repo pairs with a fuzz target comparing it
// against the reference implementation; rawBytes's reference is the ordinary
// []byte(s) copy it exists to avoid, and the property worth checking is not
// that the two views hold equal bytes — unsafe.Slice(unsafe.StringData(s),
// len(s)) guarantees that by construction — but that classify's decision off
// raw never diverges between them. offset and length are folded into range
// the way FuzzIndexNeedle folds its anchor: a value outside the folded text is
// not a shape collect would ever hand classify, so it is normalized rather
// than left to panic.
func FuzzClassifyAgreesWithASafeCopy(f *testing.F) {
	f.Add("rateLimiter", 4, 7) // camelCase: the hump makes "Limiter" a whole word
	f.Add("HTTPServer", 4, 6)  // acronym run dropping back into a word at "Server"
	f.Add("v2beta", 2, 4)      // digit/letter transition ahead of "beta"
	f.Add("caféBar", 5, 3)     // "Bar" sits right after a multi-byte rune's last byte
	f.Add("", 0, 0)

	f.Fuzz(func(t *testing.T, s string, offset, length int) {
		if len(s) == 0 {
			offset, length = 0, 0
		} else {
			offset = ((offset % (len(s) + 1)) + (len(s) + 1)) % (len(s) + 1)
			length = ((length % (len(s) - offset + 1)) + (len(s) - offset + 1)) % (len(s) - offset + 1)
		}
		folded := fold(nil, s)
		want := classify(folded, []byte(s), offset, length)
		if got := classify(folded, rawBytes(s), offset, length); got != want {
			t.Fatalf("classify(rawBytes(%q), %d, %d) = %q, want %q (safe copy)", s, offset, length, got, want)
		}
	})
}
