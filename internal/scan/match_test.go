package scan

import (
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func TestClassifyReadsCaseFromRawNotFolded(t *testing.T) {
	cases := []struct {
		name           string
		raw            string
		offset, length int
		want           schema.MatchKind
	}{
		{"non-leading segment, raw has the hump", "rateLimiter", 4, 7, schema.MatchWord},
		{"same span, no case in raw", "ratelimiter", 4, 7, schema.MatchInside},
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

func TestATermIsFoundAndLocatedThroughAnyOfItsNeedles(t *testing.T) {
	typed := newTerm(rawTerm{text: "settlemint"}, true)
	widened := typed
	widened.alt = []variant{newVariant("settlement"), newVariant("batch")}

	for _, c := range []struct {
		name  string
		t     term
		text  string
		found bool
		index int
	}{
		{"typed needle only, present", typed, "the settlemint cleared", true, 4},
		{"typed needle only, absent", typed, "the settlement cleared", false, -1},
		{"substituted needle present", widened, "the settlement cleared", true, 4},
		{"neither needle present", widened, "nothing of the sort", false, -1},
		{"earliest needle wins, not the first listed", widened, "the batch and the settlement", true, 4},
		{"typed needle behind a substituted one", widened, "batch then settlemint", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			folded := fold(nil, c.text)
			if got := c.t.found(folded); got != c.found {
				t.Errorf("found(%q) = %v, want %v", c.text, got, c.found)
			}
			if got := c.t.index(folded); got != c.index {
				t.Errorf("index(%q) = %d, want %d", c.text, got, c.index)
			}
		})
	}
}

func TestCaseBoundary(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		i    int
		want bool
	}{
		{"camel hump: lower then upper", "eL", 1, true},
		{"camel hump: digit then upper", "2L", 1, true},
		{"acronym to word: upper, upper, lower follows", "PSe", 1, true},
		// A mutation dropping the look-ahead would misclassify this as a boundary too.
		{"all-caps run: upper, upper, upper follows", "ABC", 1, false},
		{"letter after digit", "2t", 1, true},
		{"digit after letter", "t2", 1, true},
		{"no transition: two lower-case letters", "no", 1, false},
		{"start of buffer: no byte to compare against", "ab", 0, false},
		{"end of buffer: no byte at i", "ab", 2, false},
		// A UTF-8 continuation byte must never be read as a case boundary.
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

// rawBytes's correctness argument: classify must never diverge between it and
// a plain []byte(s) copy.
func FuzzClassifyAgreesWithASafeCopy(f *testing.F) {
	f.Add("rateLimiter", 4, 7)
	f.Add("HTTPServer", 4, 6)
	f.Add("v2beta", 2, 4)
	f.Add("caféBar", 5, 3)
	f.Add("", 0, 0)

	f.Fuzz(func(t *testing.T, s string, offset, length int) {
		// Normalized into range: not a shape collect would ever hand classify.
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
