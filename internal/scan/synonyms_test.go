package scan

import "testing"

// The table's whole value is that it is small, curated and shipped: a caller
// who reads the source can account for every entry. "Under about forty pairs"
// is the phase's own ceiling.
func TestTableStaysUnderTheCuratedSizeLimit(t *testing.T) {
	if n := len(synonymPairs); n == 0 || n >= 40 {
		t.Errorf("synonymPairs has %d entries, want a curated table under 40 and not empty", n)
	}
}

// The table is bidirectional by construction: every pair a maintainer wrote
// must be reachable from either spelling, or a lookup starting from the
// abbreviation would silently fall through to nothing.
func TestEveryTablePairIsReachableFromEitherSpelling(t *testing.T) {
	for _, p := range synonymPairs {
		long, short := p[0], p[1]
		if !contains(synonymsFor(long), short) {
			t.Errorf("synonymsFor(%q) = %v, want it to name %q", long, synonymsFor(long), short)
		}
		if !contains(synonymsFor(short), long) {
			t.Errorf("synonymsFor(%q) = %v, want it to name %q", short, synonymsFor(short), long)
		}
	}
}

// The two pairs the phase names explicitly, checked by exact value rather than
// membership: a lookup that returned every table entry instead of the one
// counterpart would still pass a membership-only check.
func TestSynonymsForReturnsExactlyTheTableEntry(t *testing.T) {
	cases := []struct {
		term string
		want []string
	}{
		{"authentication", []string{"auth"}},
		{"database", []string{"db"}},
	}
	for _, c := range cases {
		if got := synonymsFor(c.term); !equalStrings(got, c.want) {
			t.Errorf("synonymsFor(%q) = %v, want %v", c.term, got, c.want)
		}
	}
}

// The negative control: a word that earns no table entry, because substring
// matching already bridges it in both directions, gets nothing back.
func TestSynonymsForReturnsNilForAWordNotInTheTable(t *testing.T) {
	for _, term := range []string{"wallet", "batch", "settlement", ""} {
		if got := synonymsFor(term); got != nil {
			t.Errorf("synonymsFor(%q) = %v, want nil", term, got)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
