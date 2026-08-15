package scan

import (
	"bytes"
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

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

	dropped []string

	// carried is scratch reused across turns: which terms the current turn has.
	carried []bool
}

type term struct {
	text   string // the term as typed, folded
	needle []byte // what is searched: the stem when expanding, else the term
	phrase bool   // came from quotes, so it is never stemmed and never dropped
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

func newTerm(r rawTerm, exact bool) term {
	var buf []byte
	buf = fold(buf, r.text)
	text := string(buf)
	needle := text
	if !exact && !r.quoted {
		needle = stem(text)
	}
	return term{text: text, needle: []byte(needle), phrase: r.quoted}
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
		m.carried[i] = bytes.Contains(folded, m.terms[i].needle)
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
		if bytes.Contains(folded, m.exclude[i].needle) {
			return true
		}
	}
	return false
}

// collect appends every occurrence of every carried term to dst in offset
// order.
//
// Two matches at different offsets in one turn are two hits: ranking keys on
// session, uuid, tier, offset, length and text, so collapsing them here would
// undercount. Byte-identical spans, which two terms sharing a stem produce, are
// the one case merged — they are the same match found twice.
func (m *matcher) collect(dst []span, folded []byte) []span {
	dst = dst[:0]
	for i := range m.terms {
		if !m.carried[i] {
			continue
		}
		needle := m.terms[i].needle
		for base := 0; base+len(needle) <= len(folded); {
			at := bytes.Index(folded[base:], needle)
			if at < 0 {
				break
			}
			off := base + at
			dst = append(dst, span{offset: off, length: len(needle), kind: classify(folded, off, len(needle))})
			base = off + len(needle)
		}
	}
	if len(m.terms) > 1 {
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

// classify says whether a match stands as its own word, starts one, or sits
// inside one. All three are returned; ranking weighs them differently, because
// "no" matching inside "know" is a real occurrence and a poor answer.
func classify(folded []byte, offset, length int) schema.MatchKind {
	lead := offset == 0 || !wordByte(folded[offset-1])
	end := offset + length
	trail := end >= len(folded) || !wordByte(folded[end])
	switch {
	case lead && trail:
		return schema.MatchWord
	case lead:
		return schema.MatchPrefix
	default:
		return schema.MatchInside
	}
}

// fold lowercases s into dst for case-insensitive matching, preserving every
// byte position so a hit offset locates the match in the original text.
//
// A rune whose lowercase form is a different width is left alone rather than
// folded: Unicode's few width-changing cases are worth less than offsets that
// can be trusted, and a renderer highlights by offset without re-searching.
func fold(dst []byte, s string) []byte {
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
