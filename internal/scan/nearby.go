package scan

import (
	"bytes"
	"cmp"
	"math/bits"
	"slices"
	"unicode/utf8"

	"github.com/mayberuk/recall/internal/schema"
)

// DefaultNearbyMax is how many corpus terms a missing query term reports.
const DefaultNearbyMax = 8

// The miss path is allowed to cost more than the hit path, because it only runs
// when the caller has nothing and needs a next query. It is still bounded three
// ways: candidateCap on memory, nearbyBudget on bytes tokenized, and minToken on
// what is worth offering. The budget applies to suggestions only — the per-term
// turn counts are exhaustive, because they are a claim about the corpus rather
// than an offer, and an under-counted claim is the dealbreaker shape.
const (
	candidateCap = 4096
	minToken     = 3
)

// nearbyBudget is how many bytes of text the suggestion pass may tokenize. It
// covers the whole conversation tier several times over and stops an all-tier
// miss from tokenizing 173 MB of tool output. A test shrinks it; nothing else
// writes it.
var nearbyBudget = 64 << 20

// TermReport is one query term's fate on a search that found nothing.
type TermReport struct {
	Term string

	// Turns is how many turns in the searched tiers carry this term on its own.
	// A zero-result query where every term has a non-zero count means the terms
	// never co-occur, which is a different next move from a term nothing carries.
	Turns int

	// Nearby is populated only when Turns is zero: corpus terms close to this
	// one, nearest first.
	Nearby []Term
}

// Term is a word that exists in the searched tiers, offered as the next query.
type Term struct {
	// Text is lowercased, because matching is: it is a query to run, not a
	// quotation of the corpus.
	Text string

	// Count is occurrences, not turns: a term worth suggesting is one the corpus
	// uses, and counting turns would need a second dedup pass over the miss path.
	Count int

	// Distance is the edit distance from the query term it is offered for.
	Distance int
}

func nearbyMax(n int) int {
	if n == 0 {
		return DefaultNearbyMax
	}
	return n
}

// survey explains a zero-result search: what each term matched on its own, and
// for the terms nothing matched, what the corpus does carry near them.
func (m matcher) survey(turns []schema.Turn, want map[schema.Tier]bool, keep func(*schema.Turn) bool, max int) []TermReport {
	reports := make([]TermReport, len(m.terms))
	for i := range m.terms {
		reports[i].Term = m.terms[i].text
	}
	searched := func(t *schema.Turn) bool { return want[t.Tier] && (keep == nil || keep(t)) }

	var buf []byte
	// A single-term search that found nothing proves that term is carried by no
	// turn, so the counting pass would only re-derive a zero.
	if len(m.terms) > 1 {
		for i := range turns {
			if !searched(&turns[i]) {
				continue
			}
			buf = fold(buf, turns[i].Text)
			for j := range m.terms {
				if bytes.Contains(buf, m.terms[j].needle) {
					reports[j].Turns++
				}
			}
		}
	}

	var missing []*collector
	for i := range reports {
		if reports[i].Turns == 0 {
			missing = append(missing, newCollector(m.terms[i].text))
		}
	}
	if len(missing) == 0 {
		return reports
	}

	budget := nearbyBudget
	for i := range turns {
		if budget <= 0 {
			break
		}
		if !searched(&turns[i]) {
			continue
		}
		buf = fold(buf, turns[i].Text)
		budget -= len(buf)
		tokenize(buf, func(tok []byte) {
			for _, c := range missing {
				c.offer(tok)
			}
		})
	}

	next := 0
	for i := range reports {
		if reports[i].Turns != 0 {
			continue
		}
		reports[i].Nearby = missing[next].best(max)
		next++
	}
	return reports
}

// collector accumulates the corpus terms close to one missing query term.
type collector struct {
	term    string
	mask    uint32
	maxDist int
	prefix  int
	lo, hi  int
	counts  map[string]int
}

func newCollector(t string) *collector {
	dist := 2
	if len(t) < 5 {
		dist = 1
	}
	// A term nothing carries cannot be a prefix of a term the corpus does carry:
	// that word would have matched it as a substring. So the family rule wants a
	// shared prefix and a divergent ending — "retries" for "retry", 3 edits away.
	prefix := len(t) - 3
	if prefix < minStem {
		prefix = minStem
	}
	lo := len(t) - dist
	if prefix < lo {
		lo = prefix
	}
	if lo < minToken {
		lo = minToken
	}
	return &collector{
		term:    t,
		mask:    maskOf([]byte(t)),
		maxDist: dist,
		prefix:  prefix,
		lo:      lo,
		hi:      len(t) + 4,
		counts:  make(map[string]int, 64),
	}
}

func (c *collector) offer(tok []byte) {
	if len(tok) < c.lo || len(tok) > c.hi {
		return
	}
	if !c.near(tok) {
		return
	}
	if n, ok := c.counts[string(tok)]; ok {
		c.counts[string(tok)] = n + 1
		return
	}
	if len(c.counts) < candidateCap {
		c.counts[string(tok)] = 1
	}
}

// near is ordered cheapest test first: the shared-prefix family rule needs no
// arithmetic, and the character-set mask rejects most of what survives the
// length window before any edit distance is computed.
func (c *collector) near(tok []byte) bool {
	if commonPrefix(c.term, tok) >= c.prefix {
		return true
	}
	d := len(tok) - len(c.term)
	if d < 0 {
		d = -d
	}
	if d > c.maxDist {
		return false
	}
	if bits.OnesCount32(c.mask^maskOf(tok)) > 2*c.maxDist {
		return false
	}
	return distance(c.term, tok, c.maxDist) <= c.maxDist
}

func (c *collector) best(max int) []Term {
	out := make([]Term, 0, len(c.counts))
	for text, n := range c.counts {
		out = append(out, Term{
			Text:     text,
			Count:    n,
			Distance: distance(c.term, []byte(text), len(c.term)+len(text)),
		})
	}
	slices.SortFunc(out, func(a, b Term) int {
		if d := cmp.Compare(a.Distance, b.Distance); d != 0 {
			return d
		}
		if d := cmp.Compare(b.Count, a.Count); d != 0 {
			return d
		}
		return cmp.Compare(a.Text, b.Text)
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// tokenize calls fn for every word in folded text. Bytes above ASCII stay inside
// a token: splitting them out would offer half a word in a language whose words
// are not ASCII.
func tokenize(folded []byte, fn func(tok []byte)) {
	start := -1
	for i := 0; i < len(folded); i++ {
		if wordByte(folded[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			fn(folded[start:i])
			start = -1
		}
	}
	if start >= 0 {
		fn(folded[start:])
	}
}

func wordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c >= utf8.RuneSelf:
		return true
	}
	return false
}

// maskOf is a set of the character classes a word uses: one bit per lowercase
// letter, one for digits, one for everything else. Two words within edit
// distance d differ by at most 2d bits, so this rejects most candidates without
// touching the distance table.
func maskOf(tok []byte) uint32 {
	var mask uint32
	for _, c := range tok {
		switch {
		case c >= 'a' && c <= 'z':
			mask |= 1 << (c - 'a')
		case c >= '0' && c <= '9':
			mask |= 1 << 26
		default:
			mask |= 1 << 27
		}
	}
	return mask
}

func commonPrefix(a string, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// distance is Levenshtein, abandoned once every cell in a row exceeds limit.
func distance(a string, b []byte, limit int) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(a)+1)
	cur := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= len(b); j++ {
		cur[0] = j
		best := cur[0]
		for i := 1; i <= len(a); i++ {
			sub := prev[i-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			m := sub
			if d := prev[i] + 1; d < m {
				m = d
			}
			if d := cur[i-1] + 1; d < m {
				m = d
			}
			cur[i] = m
			if m < best {
				best = m
			}
		}
		if best > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(a)]
}
