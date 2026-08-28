package scan

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/mayberuk/recall/internal/schema"
)

// minStem is the shortest stem a term may be cut down to. Below it expansion
// stops discriminating — "hits" cut to "hit" also matches "whither".
const minStem = 4

// suffixes are tried longest first so "ies" is not read as "es". Each cut
// leaves a prefix of the term, which is why one needle per term is enough: the
// stem matches every word the full term would have matched, and more.
var suffixes = []string{"ies", "ing", "es", "ed", "ly", "s", "y"}

// stopwords are dropped from a query of more than two terms. An agent writing
// keyword soup spends terms on words every turn carries, and a term carried
// everywhere adds nothing to the count that ranks a turn. Dropping is declared,
// and never applied to a quoted phrase or to a query it would leave under two
// terms.
// The list is the ordinary closed-class English words: articles, pronouns,
// auxiliaries, prepositions and conjunctions. Nothing here narrows a corpus of
// English prose, and a measured frequency cut would need a counting pass over
// the corpus before the search it is meant to speed up.
var stopwords = map[string]bool{
	"a": true, "about": true, "after": true, "all": true, "also": true,
	"am": true, "an": true, "and": true, "any": true, "are": true, "as": true,
	"at": true, "be": true, "been": true, "before": true, "being": true,
	"but": true, "by": true, "can": true, "could": true, "did": true,
	"do": true, "does": true, "for": true, "from": true, "get": true,
	"got": true, "had": true, "has": true, "have": true, "he": true,
	"her": true, "here": true, "him": true, "his": true, "how": true,
	"i": true, "if": true, "in": true, "into": true, "is": true, "it": true,
	"its": true, "just": true, "me": true, "more": true, "most": true,
	"my": true, "no": true, "not": true, "now": true, "of": true, "on": true,
	"one": true, "only": true, "or": true, "other": true, "our": true,
	"out": true, "over": true, "she": true, "should": true, "so": true,
	"some": true, "such": true, "than": true, "that": true, "the": true,
	"their": true, "them": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "to": true, "too": true,
	"up": true, "us": true, "very": true, "was": true, "we": true,
	"were": true, "what": true, "when": true, "where": true, "which": true,
	"while": true, "who": true, "why": true, "will": true, "with": true,
	"would": true, "you": true, "your": true,
}

type matcher struct {
	terms   []term
	exclude []term

	// strict keeps the old all-terms-or-nothing rule. Without it the walk keeps
	// the turns carrying the most terms, so a seven-term query degrades to its
	// best five-term matches instead of falling off a cliff.
	strict bool

	// expanded is set when any term carries a substituted needle: a second way
	// spans can arrive out of offset order, forcing collect to sort them.
	expanded bool

	dropped []string

	// carried is scratch reused across turns: which terms the current turn has.
	carried []bool
}

// term is one slot of the query, satisfied by the needle the caller typed or
// any needle in alt. It stays one slot however many needles back it, so mark,
// need and Match.Required stay a function of the query's word count alone.
type term struct {
	text   string // the term as typed, folded
	needle []byte // what is searched: the stem when expanding, else the term
	rare   int    // index into needle of the byte the corpus scan anchors on
	phrase bool   // came from quotes, so it is never stemmed and never dropped

	// alt are needles searched in this term's place, added on the miss path
	// and nil on every search that found something. The typed needle stays a
	// field of its own rather than becoming alt[0]: a one-element slice on
	// every term would add per-search allocations against a 2% regression gate.
	alt []variant
}

// variant is one substituted needle with its own anchor, because the rarest
// byte of a corpus word is not the rarest byte of the word it stands in for.
type variant struct {
	needle []byte
	rare   int
}

func newVariant(text string) variant {
	n := []byte(text)
	return variant{needle: n, rare: rarestByte(n)}
}

type rawTerm struct {
	text   string
	quoted bool
}

func compile(q Query) matcher {
	var m matcher
	m.strict = q.AllTerms

	kept, dropped := dropStopwords(splitTerms(q.Text))
	m.dropped = dropped
	seen := map[string]bool{}
	for _, r := range kept {
		t := newTerm(r, q.Exact)
		// A term repeated in one query is one term. Counting it twice would let
		// a turn carrying it score as if it had answered two parts of the query.
		if seen[t.text] {
			continue
		}
		seen[t.text] = true
		m.terms = append(m.terms, t)
	}
	for _, raw := range q.Not {
		for _, r := range splitTerms(raw) {
			m.exclude = append(m.exclude, newTerm(r, q.Exact))
		}
	}
	m.carried = make([]bool, len(m.terms))
	return m
}

// fork returns a matcher that can run on its own goroutine. Everything here is
// read-only once compiled except carried, the scratch that mark rewrites for
// every turn, so a fork is this struct plus a fresh scratch slice — cheaper than
// compiling the query again, and it cannot drift from the original the way a
// second compile could.
func (m *matcher) fork() matcher {
	f := *m
	f.carried = make([]bool, len(m.terms))
	return f
}

// widen returns a matcher whose named terms also search the corpus words in
// exps. The terms slice is copied rather than written through, since a re-run
// that comes to nothing has to leave the original matcher exactly as it stood.
func (m *matcher) widen(exps []Expansion) matcher {
	out := m.fork()
	out.terms = slices.Clone(m.terms)
	out.expanded = true
	for _, e := range exps {
		for i := range out.terms {
			if out.terms[i].text != e.Term {
				continue
			}
			alt := make([]variant, 0, len(e.Variants))
			for _, v := range e.Variants {
				alt = append(alt, newVariant(v))
			}
			out.terms[i].alt = alt
		}
	}
	return out
}

func newTerm(r rawTerm, exact bool) term {
	var buf []byte
	buf = fold(buf, r.text)
	text := string(buf)
	needle := text
	if !exact && !r.quoted {
		needle = stem(text)
	}
	n := []byte(needle)
	return term{text: text, needle: n, rare: rarestByte(n), phrase: r.quoted}
}

// splitTerms cuts a query into terms on whitespace, except inside double
// quotes: `"build number" ios` is two terms, one of them a phrase. Agents write
// quoted phrases unprompted, and without this the quotes became two ANDed words
// that could match anywhere in a turn.
func splitTerms(text string) []rawTerm {
	var out []rawTerm
	var cur strings.Builder
	quoted := false
	flush := func(fromQuote bool) {
		if cur.Len() > 0 {
			out = append(out, rawTerm{text: cur.String(), quoted: fromQuote})
			cur.Reset()
		}
	}
	for _, r := range text {
		switch {
		case r == '"':
			flush(quoted)
			quoted = !quoted
		case !quoted && unicode.IsSpace(r):
			flush(false)
		default:
			cur.WriteRune(r)
		}
	}
	flush(quoted)
	return out
}

func dropStopwords(raw []rawTerm) (kept []rawTerm, dropped []string) {
	if len(raw) <= 2 {
		return raw, nil
	}
	for _, r := range raw {
		if !r.quoted && stopwords[strings.ToLower(r.text)] {
			dropped = append(dropped, r.text)
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) < 2 {
		return raw, nil
	}
	return kept, dropped
}

// stem cuts one recognised suffix off term when enough of it survives. Measured
// worth on this corpus is close to zero — "wallets" appears 0 times against
// 2,225 turns of "wallet" — so this stays a suffix cut rather than a Porter
// stemmer. Widening a term can add a hit but never remove one, which is the
// direction the dealbreaker cares about.
func stem(s string) string {
	for _, suf := range suffixes {
		if len(s)-len(suf) >= minStem && hasSuffix(s, suf) {
			return s[:len(s)-len(suf)]
		}
	}
	return s
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

// span is where one match sits inside a turn's text.
type span struct {
	offset int
	length int
	kind   schema.MatchKind
}

// mark records which of the query's terms this turn carries and returns how
// many. It abandons the turn as soon as the terms left cannot lift the count to
// need, which keeps the overwhelming majority of turns at one pass per term —
// with need at the full term count this is exactly the old all-terms test. An
// abandoned turn's count is an underestimate, and always below need, so a
// caller comparing against need can never keep one by mistake.
func (m *matcher) mark(folded []byte, need int) int {
	found := 0
	for i := range m.terms {
		m.carried[i] = m.terms[i].found(folded)
		if m.carried[i] {
			found++
			continue
		}
		if found+len(m.terms)-i-1 < need {
			for j := i + 1; j < len(m.terms); j++ {
				m.carried[j] = false
			}
			return found
		}
	}
	return found
}

// excluded reports whether any --not term is present. Exclusion is checked
// before matching so a turn the caller ruled out costs one pass, not two.
func (m matcher) excluded(folded []byte) bool {
	for i := range m.exclude {
		if m.exclude[i].found(folded) {
			return true
		}
	}
	return false
}

// collect appends every occurrence of every needle backing every carried term
// to dst in offset order.
//
// Two matches at different offsets in one turn are two hits: ranking keys on
// session, uuid, tier, offset, length and text, so collapsing them here would
// undercount. Byte-identical spans, which two terms sharing a stem produce and
// which two needles of one term produce when they overlap, are the one case
// merged — they are the same match found twice.
func (m *matcher) collect(dst []span, folded, raw []byte) []span {
	dst = dst[:0]
	for i := range m.terms {
		if !m.carried[i] {
			continue
		}
		t := &m.terms[i]
		dst = appendSpans(dst, folded, raw, t.needle, t.rare)
		for j := range t.alt {
			dst = appendSpans(dst, folded, raw, t.alt[j].needle, t.alt[j].rare)
		}
	}
	if len(m.terms) > 1 || m.expanded {
		slices.SortFunc(dst, func(a, b span) int {
			if d := cmp.Compare(a.offset, b.offset); d != 0 {
				return d
			}
			return cmp.Compare(a.length, b.length)
		})
		dst = slices.Compact(dst)
	}
	return dst
}

// appendSpans walks each needle separately: a span's length is its own
// needle's, and two needles of one term rarely share a length.
func appendSpans(dst []span, folded, raw, needle []byte, rare int) []span {
	for base := 0; base+len(needle) <= len(folded); {
		at := indexAt(folded[base:], needle, rare)
		if at < 0 {
			break
		}
		off := base + at
		dst = append(dst, span{offset: off, length: len(needle), kind: classify(folded, raw, off, len(needle))})
		base = off + len(needle)
	}
	return dst
}

// rawBytes is a read-only, zero-copy view of s's bytes. Reinterpreting the
// string's backing array is safe only because classify never writes to raw;
// it avoids the copy a []byte(s) conversion would charge per matched turn.
func rawBytes(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// classify says whether a match stands as its own word, starts one, or sits
// inside one. All three are returned; ranking weighs them differently, because
// "no" matching inside "know" is a real occurrence and a poor answer.
//
// folded has lost case, so it alone can't see a camel hump ("limiter" inside
// "rateLimiter" reads as interior). raw, byte-aligned with folded by fold's
// guarantee, is checked for the case and letter/digit transitions that mark
// an identifier segment boundary instead.
func classify(folded, raw []byte, offset, length int) schema.MatchKind {
	lead := offset == 0 || !wordByte(folded[offset-1])
	end := offset + length
	trail := end >= len(folded) || !wordByte(folded[end])
	if !lead {
		lead = caseBoundary(raw, offset)
	}
	if !trail {
		trail = caseBoundary(raw, end)
	}
	switch {
	case lead && trail:
		return schema.MatchWord
	case lead:
		return schema.MatchPrefix
	default:
		return schema.MatchInside
	}
}

// caseBoundary reports whether raw starts a new identifier segment at i: a
// camel hump, an acronym dropping back to a word, or a letter/digit
// transition. ASCII-only — past utf8.RuneSelf this is left to wordByte.
func caseBoundary(raw []byte, i int) bool {
	if i <= 0 || i >= len(raw) || raw[i] >= utf8.RuneSelf || raw[i-1] >= utf8.RuneSelf {
		return false
	}
	cur, prev := raw[i], raw[i-1]
	switch {
	case isUpper(cur) && (isLower(prev) || isDigit(prev)):
		return true
	case isUpper(cur) && isUpper(prev) && i+1 < len(raw) && raw[i+1] < utf8.RuneSelf && isLower(raw[i+1]):
		return true
	case isLetter(cur) && isDigit(prev), isDigit(cur) && isLetter(prev):
		return true
	}
	return false
}

func isUpper(c byte) bool  { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool  { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return isUpper(c) || isLower(c) }
