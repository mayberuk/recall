// Package scan is recall's linear search over stripped turns.
//
// Every byte of every tier it was asked to search is examined. Head and tail
// truncation was measured to index 19% of tool-result text, so a term sitting in
// the unindexed middle would be a false negative by construction — the one
// outcome the tool exists to prevent. What makes the narrow default honest is
// that a Result names the tiers it did not cover, so a caller can say so.
package scan

import (
	"slices"

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

	// CountWords asks for the counters that read the scanned text a second
	// time: words, and lines with them. Both are opt-in because both were
	// measured, and a second pass over the folded buffer costs 3.2% to 16.1%
	// depending on the query shape — too much to charge every search for a
	// figure most callers never ask for.
	CountWords bool
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

	// Expanded is the terms answered by a word the caller did not type.
	Expanded []Expansion
}

// Expansion is one query term searched under another spelling: what was typed,
// the corpus words put in its place, and how far from the typed term they are.
type Expansion struct {
	Term     string
	Variants []string
	Distance int
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

	// BytesScanned is the text this search read, over exactly the turns
	// TurnsScanned counts. It measures work rather than corpus size: a search
	// that walked the corpus twice reports both walks, which is what Passes is
	// there to explain.
	BytesScanned int64

	// LinesScanned and WordsScanned are populated only when Query.CountWords was
	// set, because both need a pass over the text that BytesScanned does not.
	// WordsCounted covers the pair, and distinguishes the two ways they can be
	// zero — text with no lines or words in it, and a search that was never
	// asked to count either.
	LinesScanned int64
	WordsScanned int64
	WordsCounted bool

	// Passes is how many readings the coverage footer explains: one to find
	// hits, one to explain a short result, one for a substitution re-run.
	// Dividing BytesScanned by Passes does not give corpus size.
	Passes int

	// TurnsBySession is conversation turns per session, counted over every turn
	// handed to Search whatever tiers were searched. It is ranking's
	// concentration denominator, which counts conversation turns only so that a
	// session is not penalised for having used tools.
	TurnsBySession map[string]int

	// Terms is populated when the search came back short: per query term, how
	// many turns carry it, and the closest corpus terms for one none does. A
	// substitution can still fill Hits without emptying it.
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

	res := Result{Tiers: tiers, WordsCounted: q.CountWords, Passes: 1}
	m := compile(q)
	mp := &m
	res.Match = Match{Total: len(m.terms), Dropped: m.dropped}
	for i := range m.terms {
		res.Match.Terms = append(res.Match.Terms, m.terms[i].text)
	}
	for i := range m.exclude {
		res.Match.Excluded = append(res.Match.Excluded, m.exclude[i].text)
	}

	// The corpus is cut into contiguous ranges scanned concurrently. What makes
	// that safe is that the walk is order-independent in what it finishes with:
	// see mergeShards for the argument and the rule that folds the ranges back
	// into one answer.
	found := mergeShards(scanShards(turns, q, mp, want), &res, len(m.terms))
	settle(&res, mp, found)

	// A full hit returns here, keeping the hit path at its one-pass cost. A
	// miss or a relaxed result goes on to the survey; --exact skips the pass
	// since it forecloses the substitution the pass exists for.
	miss := len(res.Hits) == 0
	partial := !miss && !q.Exact && slices.Contains(found.carried, false)
	if len(m.terms) == 0 || q.NearbyMax < 0 || !(miss || partial) {
		return res
	}

	reports, w := m.survey(turns, want, q.Keep, nearbyMax(q.NearbyMax), q.CountWords)
	res.BytesScanned += w.bytes
	res.LinesScanned += w.lines
	res.WordsScanned += w.words
	res.Passes++
	if miss {
		res.Terms = reports
	}
	if !q.Exact {
		substitute(turns, q, &res, mp, want, reports)
	}
	return res
}

// settle keeps below-level hits only when the full query was unsatisfiable.
func settle(res *Result, m *matcher, found merged) {
	res.Hits = found.hits
	res.Match.Required, res.Match.Carried = 0, nil
	if len(found.hits) == 0 {
		return
	}
	res.Match.Required = found.need
	if found.need < len(m.terms) && len(found.below) > 0 {
		res.Hits = append(res.Hits, found.below...)
		res.Match.Required = found.need - 1
		or(found.carried, found.belowCarried)
	}
	for j := range found.carried {
		if found.carried[j] {
			res.Match.Carried = append(res.Match.Carried, m.terms[j].text)
		}
	}
}

// substitute re-runs the search with the corpus words closest to the terms
// nothing carried, keeping the re-run only if it reaches one of them.
func substitute(turns []schema.Turn, q Query, res *Result, m *matcher, want map[schema.Tier]bool, reports []TermReport) {
	exps := substitutions(reports)
	if len(exps) == 0 {
		return
	}
	wide := m.widen(exps)

	// again is a throwaway; only its scan cost is carried back to res.
	var again Result
	found := mergeShards(scanShards(turns, q, &wide, want), &again, len(wide.terms))
	res.BytesScanned += again.BytesScanned
	res.LinesScanned += again.LinesScanned
	res.WordsScanned += again.WordsScanned
	res.Passes++

	var widened Result
	settle(&widened, &wide, found)

	// Kept only if it carries a term the first walk carried none of, else it
	// answered a different question for nothing.
	if len(widened.Match.Carried) <= len(res.Match.Carried) {
		return
	}
	res.Hits = widened.Hits
	res.Match.Required = widened.Match.Required
	res.Match.Carried = widened.Match.Carried
	res.Match.Expanded = exps
}

// appendHits records one turn's matches at a term count below the best seen so
// far, which are kept only in case nothing ever reaches the full query.
func appendHits(dst []schema.Hit, turn *schema.Turn, m *matcher, spans *[]span, buf, raw []byte, found int, carried []bool) []schema.Hit {
	for j := range carried {
		carried[j] = carried[j] || m.carried[j]
	}
	*spans = m.collect(*spans, buf, raw)
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
