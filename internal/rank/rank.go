// Package rank turns scan hits into ranked sessions with facet counts.
//
// The session is the unit of result: 947 of 1,077 files are subagent
// transcripts and 6 files carry two sessions, so a file is never one. Counts
// deduplicate by record uuid first, because 17,659 records exist in more than
// one file and a count that skips that perturbs the ranking it feeds.
package rank

import (
	"cmp"
	"slices"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

// Mode is how ranked sessions are ordered. Sort follows the verb — find is
// Concentration, when is Chronological — and --sort recent overrides either, so
// the caller names the order instead of re-sorting the result. Any other value
// is ranked as Concentration and Result.Mode reports what was applied.
type Mode string

const (
	Concentration Mode = "concentration"
	Chronological Mode = "chronological"
	Recent        Mode = "recent"
)

// Matched is the display unit: one turn the query matched, however many times
// it matched inside it. Six terms landing in one long turn used to print as six
// windows onto the same sentence at sliding offsets, which reads as six results
// and is one.
type Matched struct {
	schema.Hit

	// Occurrences is how many matches the turn holds. Hit is the best of them:
	// a whole-word match ahead of a match inside a longer word, earliest first.
	Occurrences int

	// Signal is what the turn is worth, and is what both the session's score and
	// the choice of which turns to print are built on.
	Signal float64
}

// Session is one ranked session with its subagent hits folded in. Repo and
// Branch are the dominant value among its hits, since a session can span both.
// Hits are ordered oldest first, undated last; Turnwise is the same matches
// collapsed per turn and ordered by what they are worth.
type Session struct {
	ID         string
	Repo       string
	Branch     string
	Hits       []schema.Hit
	Turnwise   []Matched
	HitCount   int
	HitTurns   int // distinct conversation turns the hits occupy: the denominator's floor
	Turns      int // denominator actually used
	TurnsKnown bool
	AgentHits  int
	Score      float64
	First      time.Time
	Last       time.Time
	Dated      bool
}

// Result is the ranked outcome. Redundant is how many duplicate record copies
// dedup dropped, and UnknownTurns names the sessions the caller supplied no
// usable turn count for — those rank below every session that has one, so a
// missing denominator can only demote a session, never promote it.
type Result struct {
	Mode         Mode
	Sessions     []Session
	Facets       Facets
	HitCount     int
	Redundant    int
	UnknownTurns []string
}

// Rank deduplicates the hits, groups them by session, scores each session and
// orders the result by mode. Turns holds conversation turns per session, which
// must come from the caller: counting tool-result records in the denominator
// would penalise a session for using tools.
func Rank(hits []schema.Hit, turns map[string]int, mode Mode) Result {
	deduped, redundant := Dedup(hits)

	order := []string{}
	groups := map[string]*Session{}
	repos := map[string]map[string]int{}
	branches := map[string]map[string]int{}
	hitTurns := map[string]map[string]struct{}{}

	for _, h := range deduped {
		s := groups[h.Session]
		if s == nil {
			s = &Session{ID: h.Session}
			groups[h.Session] = s
			order = append(order, h.Session)
			repos[h.Session] = map[string]int{}
			branches[h.Session] = map[string]int{}
			hitTurns[h.Session] = map[string]struct{}{}
		}
		s.Hits = append(s.Hits, h)
		s.HitCount++
		if h.Author == schema.AuthorAgent {
			s.AgentHits++
		}
		repos[h.Session][h.Repo]++
		branches[h.Session][h.Branch]++
		if h.Tier == schema.TierConversation {
			hitTurns[h.Session][h.UUID] = struct{}{}
		}
		if ts, ok := parseTS(h.TS); ok {
			if !s.Dated || ts.Before(s.First) {
				s.First = ts
			}
			if !s.Dated || ts.After(s.Last) {
				s.Last = ts
			}
			s.Dated = true
		}
	}

	var unknown []string
	sessions := make([]Session, 0, len(order))
	for _, id := range order {
		s := groups[id]
		s.Repo = dominant(repos[id])
		s.Branch = dominant(branches[id])
		s.HitTurns = len(hitTurns[id])
		s.Turns, s.TurnsKnown = denominator(turns, id, s.HitTurns)
		sortHits(s.Hits)
		s.Turnwise = collapse(s.Hits)
		s.Score = Score(totalSignal(s.Turnwise), s.Turns)
		if !s.TurnsKnown {
			unknown = append(unknown, id)
		}
		sessions = append(sessions, *s)
	}
	slices.Sort(unknown)

	mode = normalize(mode)
	sortSessions(sessions, mode)

	return Result{
		Mode:         mode,
		Sessions:     sessions,
		Facets:       facetsOf(deduped),
		HitCount:     len(deduped),
		Redundant:    redundant,
		UnknownTurns: unknown,
	}
}

// denominator prefers the caller's count but never lets it fall below the
// conversation turns the hits themselves prove exist. A count that is absent or
// non-positive contradicts a session that has hits, so it is reported unknown
// rather than divided by.
func denominator(turns map[string]int, id string, hitTurns int) (int, bool) {
	supplied, ok := turns[id]
	if !ok || supplied <= 0 {
		return hitTurns, false
	}
	return max(supplied, hitTurns), true
}

func normalize(mode Mode) Mode {
	switch mode {
	case Chronological, Recent:
		return mode
	default:
		return Concentration
	}
}

func sortSessions(sessions []Session, mode Mode) {
	slices.SortFunc(sessions, func(a, b Session) int {
		if d := compareDated(a, b); d != 0 {
			return d
		}
		switch mode {
		case Chronological:
			if d := a.First.Compare(b.First); d != 0 {
				return d
			}
		case Recent:
			if d := b.Last.Compare(a.Last); d != 0 {
				return d
			}
		default:
			if d := boolCompare(b.TurnsKnown, a.TurnsKnown); d != 0 {
				return d
			}
			if d := cmp.Compare(b.Score, a.Score); d != 0 {
				return d
			}
			if d := cmp.Compare(b.HitCount, a.HitCount); d != 0 {
				return d
			}
			if d := b.Last.Compare(a.Last); d != 0 {
				return d
			}
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// compareDated keeps undated sessions last in every mode: a zero timestamp
// would otherwise sort first in Chronological and claim to be the oldest.
func compareDated(a, b Session) int {
	if a.Dated == b.Dated {
		return 0
	}
	return boolCompare(b.Dated, a.Dated)
}

// collapse folds a session's hits down to one entry per matched turn, keeping
// the best occurrence as the one to print and the count of the rest. Hits
// arrive time-ordered, so the result is too; the caller reorders by Signal when
// it has to choose which few to show.
func collapse(hits []schema.Hit) []Matched {
	out := make([]Matched, 0, len(hits))
	at := make(map[[2]string]int, len(hits))
	for _, h := range hits {
		key := [2]string{h.UUID, string(h.Tier)}
		i, seen := at[key]
		if !seen {
			at[key] = len(out)
			out = append(out, Matched{Hit: h, Occurrences: 1, Signal: Signal(h)})
			continue
		}
		out[i].Occurrences++
		if better(h, out[i].Hit) {
			offset, length, kind := h.Offset, h.Length, h.Match
			out[i].Hit.Offset, out[i].Hit.Length, out[i].Hit.Match = offset, length, kind
			out[i].Signal = Signal(out[i].Hit)
		}
	}
	return out
}

// Best is the n most worthwhile matched turns, returned in the order they
// happened. Taking the first n chronologically is what buried the answer: the
// injected preamble at the top of a session outranked nothing, it merely came
// first.
func Best(matched []Matched, n int) []Matched {
	if n <= 0 {
		return nil
	}
	if len(matched) <= n {
		return matched
	}
	byWorth := slices.Clone(matched)
	slices.SortStableFunc(byWorth, func(a, b Matched) int {
		if d := cmp.Compare(b.Signal, a.Signal); d != 0 {
			return d
		}
		return cmp.Compare(b.Occurrences, a.Occurrences)
	})
	keep := make(map[[2]string]bool, n)
	for _, m := range byWorth[:n] {
		keep[[2]string{m.UUID, string(m.Tier)}] = true
	}
	out := make([]Matched, 0, n)
	for _, m := range matched {
		if keep[[2]string{m.UUID, string(m.Tier)}] {
			out = append(out, m)
		}
	}
	return out
}

// better prefers the occurrence a reader can act on: a whole word over a match
// inside a longer word, and the earliest of equals.
func better(a, b schema.Hit) bool {
	if r := kindRank(a.Match) - kindRank(b.Match); r != 0 {
		return r > 0
	}
	return a.Offset < b.Offset
}

func kindRank(k schema.MatchKind) int {
	switch k {
	case schema.MatchWord:
		return 2
	case schema.MatchPrefix:
		return 1
	default:
		return 0
	}
}

func totalSignal(matched []Matched) float64 {
	total := 0.0
	for _, m := range matched {
		total += m.Signal
	}
	return total
}

func sortHits(hits []schema.Hit) {
	slices.SortStableFunc(hits, func(a, b schema.Hit) int {
		at, aok := parseTS(a.TS)
		bt, bok := parseTS(b.TS)
		if aok != bok {
			return boolCompare(bok, aok)
		}
		if aok {
			if d := at.Compare(bt); d != 0 {
				return d
			}
		}
		if d := cmp.Compare(a.UUID, b.UUID); d != 0 {
			return d
		}
		return cmp.Compare(a.Offset, b.Offset)
	})
}

func parseTS(ts string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// dominant is the most common value, preferring a present one over an absent
// one at equal count and breaking any remaining tie lexically.
func dominant(counts map[string]int) string {
	values := make([]string, 0, len(counts))
	for v := range counts {
		values = append(values, v)
	}
	slices.Sort(values)

	best := ""
	found := false
	for _, v := range values {
		better := counts[v] > counts[best]
		unblank := counts[v] == counts[best] && best == "" && v != ""
		if !found || better || unblank {
			best, found = v, true
		}
	}
	return best
}

func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}
