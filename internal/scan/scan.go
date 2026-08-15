// Package scan is recall's linear search over stripped turns.
//
// Every byte of every tier it was asked to search is examined. Head and tail
// truncation was measured to index 19% of tool-result text, so a term sitting in
// the unindexed middle would be a false negative by construction — the one
// outcome the tool exists to prevent. What makes the narrow default honest is
// that a Result names the tiers it did not cover, so a caller can say so.
package scan

import (
	"github.com/mayberuk/recall/internal/schema"
)

// Query is one search. The zero value is the safest configuration: the
// conversation tier, stem expansion on, best-partial matching, and
// terms-present-nearby on a miss.
type Query struct {
	// Text is split on whitespace into terms, except inside double quotes,
	// which make a phrase. A turn matches when it carries as many terms as the
	// best turn in the corpus does.
	Text string

	// Tiers is what to search. Empty means the conversation tier alone, which
	// is the coverage contract's default.
	Tiers []schema.Tier

	// Exact matches each term literally, without stem expansion.
	Exact bool

	// AllTerms requires every term, returning nothing rather than the best
	// partial match.
	AllTerms bool

	// Not excludes any turn carrying one of these terms.
	Not []string

	// Keep, when set, restricts the search to the turns it accepts. A rejected
	// turn is still counted in Turns and Sessions, so the coverage line's
	// searched-of-total figures state the narrowing rather than hiding it.
	Keep func(*schema.Turn) bool

	// NearbyMax caps the corpus terms reported per missing term on a
	// zero-result search. Zero takes DefaultNearbyMax; negative skips the pass.
	NearbyMax int
}

// Match is how a query was read and what the returned turns actually carry. A
// relaxed search is not a silent one: every field here is stated in the
// coverage line.
type Match struct {
	// Terms is what was searched, after phrase grouping and stopword removal.
	Terms []string

	// Dropped is the common words removed from a query of more than two terms.
	Dropped []string

	// Excluded is the --not terms.
	Excluded []string

	// Required is how many terms a returned turn carries. Below Total it means
	// no turn in the corpus carried them all and the best available was kept.
	Required int
	Total    int

	// Carried names the terms the returned turns hold, so a caller reading a
	// relaxed result knows which words the answer is actually about.
	Carried []string
}

// Relaxed reports whether the search returned turns carrying fewer than every
// term.
func (m Match) Relaxed() bool { return m.Required > 0 && m.Required < m.Total }

// Result is what one search covered and found.
//
// Turns and Sessions count everything handed to Search; TurnsScanned and
// SessionsScanned count the searched tiers alone. Both halves are here because
// the coverage line states both and a second pass to recover one would be a
// chance for the two numbers to disagree.
type Result struct {
	// Hits are every occurrence, in the order the turns arrived and by offset
	// within a turn. Two matches at different offsets in one turn are two hits.
	Hits  []schema.Hit
	Tiers []schema.Tier

	Turns    int
	Sessions int

	TurnsScanned    int
	SessionsScanned int

	// TurnsBySession is conversation turns per session, counted over every turn
	// handed to Search whatever tiers were searched. It is ranking's
	// concentration denominator, which counts conversation turns only so that a
	// session is not penalised for having used tools.
	TurnsBySession map[string]int

	// Terms is populated only when Hits is empty: per query term, how many turns
	// carry that term alone, and for a term no turn carries, the corpus terms
	// closest to it.
	Terms []TermReport

	// Match is how the query was read and what the hits carry.
	Match Match
}

// Unsearched is the tiers this search did not cover, in schema order. A
// coverage line names these, and an empty result with nothing unsearched is a
// different claim from an empty result with a tier left out.
func (r Result) Unsearched() []schema.Tier {
	searched := make(map[schema.Tier]bool, len(r.Tiers))
	for _, t := range r.Tiers {
		searched[t] = true
	}
	var out []schema.Tier
	for _, t := range allTiers {
		if !searched[t] {
			out = append(out, t)
		}
	}
	return out
}

var allTiers = []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}

// Tiers is the tier set for a run of `find`: conversation always, the
// invocation tier on --tools, the result tier on --results.
func Tiers(results, tools bool) []schema.Tier {
	out := []schema.Tier{schema.TierConversation}
	if tools {
		out = append(out, schema.TierInvocation)
	}
	if results {
		out = append(out, schema.TierResult)
	}
	return out
}

// Search scans turns for q and reports both what it found and what it covered.
//
// It never returns an error: a search that cannot answer is an empty result with
// its coverage stated, not a failure. A query with no terms matches nothing
// rather than everything.
func Search(turns []schema.Turn, q Query) Result {
	tiers := normalize(q.Tiers)
	want := make(map[schema.Tier]bool, len(tiers))
	for _, t := range tiers {
		want[t] = true
	}

	res := Result{Tiers: tiers}
	m := compile(q)
	mp := &m
	res.Match = Match{Total: len(m.terms), Dropped: m.dropped}
	for i := range m.terms {
		res.Match.Terms = append(res.Match.Terms, m.terms[i].text)
	}
	for i := range m.exclude {
		res.Match.Excluded = append(res.Match.Excluded, m.exclude[i].text)
	}

	sessions := make(map[string]*sessionState, 128)
	lastID := ""
	var last *sessionState

	// need is the term count a turn must carry to be kept, and it only ever
	// rises: the first turn carrying more terms than anything before it makes
	// every hit collected so far obsolete in one comparison, which is what
	// turns "best partial match" into a single pass rather than one pass per
	// relaxation level.
	need := 1
	if m.strict {
		need = len(m.terms)
	}
	carried := make([]bool, len(m.terms))

	// A long turn carries more query terms by carrying more words, so keeping
	// only the very best count hands a degraded query to whatever injected
	// 20 KB summary happened to contain everything. One level of slack lets the
	// short, dense turns compete, and ranking then decides between them. It
	// applies only when the query could not be met in full: a satisfiable query
	// keeps the strict all-terms result it has always had.
	var below []schema.Hit
	belowCarried := make([]bool, len(m.terms))

	var buf []byte
	var spans []span
	for i := range turns {
		turn := &turns[i]
		res.Turns++

		if turn.Session != lastID || last == nil {
			lastID = turn.Session
			if last = sessions[lastID]; last == nil {
				last = &sessionState{}
				sessions[lastID] = last
			}
		}
		if turn.Tier == schema.TierConversation {
			last.conversation++
		}
		if !want[turn.Tier] || (q.Keep != nil && !q.Keep(turn)) {
			continue
		}
		last.scanned = true
		res.TurnsScanned++

		if len(m.terms) == 0 {
			continue
		}
		buf = fold(buf, turn.Text)
		if m.excluded(buf) {
			continue
		}
		found := mp.mark(buf, need-1)
		if found < need-1 || found == 0 {
			continue
		}
		switch {
		case found == need-1:
			// Held in case the query turns out not to be satisfiable at all.
			// Once one turn has carried every term it never will be used, and
			// holding it costs a hit per occurrence for the rest of the walk.
			if need < len(m.terms) {
				below = appendHits(below, turn, mp, &spans, buf, found, belowCarried)
			}
			continue
		case found > need:
			if found == need+1 {
				below, belowCarried = res.Hits, carried
			} else {
				below, belowCarried = nil, make([]bool, len(m.terms))
			}
			need = found
			res.Hits, carried = nil, make([]bool, len(m.terms))
			if need == len(m.terms) {
				below, belowCarried = nil, make([]bool, len(m.terms))
			}
		}
		for j := range carried {
			carried[j] = carried[j] || m.carried[j]
		}
		spans = mp.collect(spans, buf)
		for _, s := range spans {
			res.Hits = append(res.Hits, schema.Hit{
				Session: turn.Session,
				UUID:    turn.UUID,
				TS:      turn.TS,
				Tier:    turn.Tier,
				Author:  turn.Author,
				Agent:   turn.Agent,
				Repo:    turn.Repo,
				Branch:  turn.Branch,
				Offset:  s.offset,
				Length:  s.length,
				Match:   s.kind,
				Terms:   found,
				Text:    turn.Text,
			})
		}
	}

	if len(res.Hits) > 0 {
		res.Match.Required = need
		if need < len(m.terms) && len(below) > 0 {
			res.Hits = append(res.Hits, below...)
			res.Match.Required = need - 1
			for j := range carried {
				carried[j] = carried[j] || belowCarried[j]
			}
		}
		for j := range carried {
			if carried[j] {
				res.Match.Carried = append(res.Match.Carried, m.terms[j].text)
			}
		}
	}

	res.Sessions = len(sessions)
	res.TurnsBySession = make(map[string]int, len(sessions))
	for id, state := range sessions {
		res.TurnsBySession[id] = state.conversation
		if state.scanned {
			res.SessionsScanned++
		}
	}

	if len(res.Hits) == 0 && len(m.terms) > 0 && q.NearbyMax >= 0 {
		res.Terms = m.survey(turns, want, q.Keep, nearbyMax(q.NearbyMax))
	}
	return res
}

// appendHits records one turn's matches at a term count below the best seen so
// far, which are kept only in case nothing ever reaches the full query.
func appendHits(dst []schema.Hit, turn *schema.Turn, m *matcher, spans *[]span, buf []byte, found int, carried []bool) []schema.Hit {
	for j := range carried {
		carried[j] = carried[j] || m.carried[j]
	}
	*spans = m.collect(*spans, buf)
	for _, s := range *spans {
		dst = append(dst, schema.Hit{
			Session: turn.Session,
			UUID:    turn.UUID,
			TS:      turn.TS,
			Tier:    turn.Tier,
			Author:  turn.Author,
			Agent:   turn.Agent,
			Repo:    turn.Repo,
			Branch:  turn.Branch,
			Offset:  s.offset,
			Length:  s.length,
			Match:   s.kind,
			Terms:   found,
			Text:    turn.Text,
		})
	}
	return dst
}

// normalize copies the requested tiers into schema order, dropping repeats and
// anything unrecognised. Copying matters: Result.Tiers is what a caller prints
// as its coverage, and a shared backing array would let one search rewrite
// another's declaration.
func normalize(tiers []schema.Tier) []schema.Tier {
	if len(tiers) == 0 {
		return []schema.Tier{schema.TierConversation}
	}
	asked := make(map[schema.Tier]bool, len(tiers))
	for _, t := range tiers {
		asked[t] = true
	}
	out := make([]schema.Tier, 0, len(allTiers))
	for _, t := range allTiers {
		if asked[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []schema.Tier{schema.TierConversation}
	}
	return out
}

// sessionState is what one session accumulated over the walk. It is held by
// pointer so the common case — consecutive turns of one session — touches the
// map once per session rather than once per turn.
type sessionState struct {
	conversation int
	scanned      bool
}
