package archive

import (
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/schema"
)

// Group is several agents' stores read as one corpus. Each keeps its own
// directory, cursor and coverage — nothing is pooled on disk — and the Group
// only joins what a caller asked for across all of them.
type Group struct {
	stores []*Store
}

// OpenGroup opens one store per selected agent.
//
// A selection naming no agent is refused rather than opened empty. It is
// reachable: asking for every agent on a machine that has none of them
// resolves to nothing, and a group of no stores answers every search with no
// matches, which reads exactly like a search that ran and found nothing.
func OpenGroup(sel Selection, opt Options) (*Group, error) {
	if len(sel.Agents) == 0 {
		return nil, fperr.New(fperr.CorpusUnreadable,
			"no agent has a session store to read (%s)", sel.Reason)
	}
	g := &Group{stores: make([]*Store, 0, len(sel.Agents))}
	for _, agent := range sel.Agents {
		// Root and Strip name one agent's session store, so they cannot travel
		// across a selection; Dir can, because each store takes its own
		// subdirectory of it.
		each := opt
		each.Agent = agent
		each.Provider = nil
		each.Root = ""
		each.Strip = nil

		s, err := Open(each)
		if err != nil {
			return nil, err
		}
		g.stores = append(g.stores, s)
	}
	return g, nil
}

// Stores are the group's stores, in selection order.
func (g *Group) Stores() []*Store { return g.stores }

// Refresh is what one pass over a group did: what each store did, in the
// group's order, and the coverage the group can claim afterwards.
//
// The coverage travels with the results because the pass already computed it.
// Reading it back with Coverage would re-parse every store's metadata, which
// carries an mtime and an oldest-record timestamp per source file — about
// 1.5 ms a store against a 14 ms warm query.
type Refresh struct {
	Stores   []Result
	Coverage Coverage
}

// Update refreshes every store and reports what each one did, in the same
// order. A failure stops there and returns the results already gathered: those
// stores really were updated, and reporting nothing would misstate the disk.
// The coverage returned alongside a failure widens only over those, for the
// same reason.
func (g *Group) Update() (Refresh, error) {
	out := Refresh{Stores: make([]Result, 0, len(g.stores))}
	for _, s := range g.stores {
		res, err := s.Update()
		if err != nil {
			return out, err
		}
		out.Stores = append(out.Stores, res)
		out.Coverage = widen(out.Coverage, res.Coverage)
	}
	return out, nil
}

// WrittenAt is when the group was last written: the earliest write among its
// stores, and the zero time if any of them has never been written at all. A
// group is only as current as its stalest store, so the newest write would
// state a freshness the corpus as a whole does not have — and a store with no
// metadata yet is not stale, it is absent, which is what the zero time means
// for a single store too.
func (g *Group) WrittenAt() time.Time {
	var out time.Time
	for _, s := range g.stores {
		at := s.WrittenAt()
		if at.IsZero() {
			return time.Time{}
		}
		if out.IsZero() || at.Before(out) {
			out = at
		}
	}
	return out
}

// Turns reads the given tiers from every store and interleaves them into the
// archive's own order, so a search over several agents sees the corpus the way
// a search over one does.
//
// One store is returned untouched. That is the path every search takes today,
// and a merge of a single sorted slice is a copy of it — 340,000 turns of
// copying to reach the same order it already had.
func (g *Group) Turns(want ...schema.Tier) ([]schema.Turn, error) {
	if len(g.stores) == 1 {
		return g.stores[0].Turns(want...)
	}
	parts := make([][]schema.Turn, len(g.stores))
	total := 0
	for i, s := range g.stores {
		turns, err := s.Turns(want...)
		if err != nil {
			return nil, err
		}
		parts[i] = turns
		total += len(turns)
	}
	return mergeTurns(parts, total), nil
}

// mergeTurns interleaves slices that are each already in the archive's order.
// It only ever takes from the front of one, so nothing is reordered within a
// store — which is what carries over the part of that order a decoded turn
// cannot express: the turns stripped from one record share a timestamp and a
// uuid and are separated only by a sequence number the tier file frames but a
// turn does not carry.
func mergeTurns(parts [][]schema.Turn, total int) []schema.Turn {
	out := make([]schema.Turn, 0, total)
	at := make([]int, len(parts))
	for range total {
		pick := -1
		for i, part := range parts {
			if at[i] >= len(part) {
				continue
			}
			if pick < 0 || compareTurns(part[at[i]], parts[pick][at[pick]]) < 0 {
				pick = i
			}
		}
		if pick < 0 {
			break
		}
		out = append(out, parts[pick][at[pick]])
		at[pick]++
	}
	return out
}

// compareTurns is compare's field order over a decoded turn, which is the same
// order minus the sequence number a turn does not carry.
func compareTurns(a, b schema.Turn) int {
	if c := strings.Compare(a.TS, b.TS); c != 0 {
		return c
	}
	if c := strings.Compare(a.Session, b.Session); c != 0 {
		return c
	}
	if c := strings.Compare(a.UUID, b.UUID); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.Tier), string(b.Tier)); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.Author), string(b.Author)); c != 0 {
		return c
	}
	if c := strings.Compare(a.Agent, b.Agent); c != 0 {
		return c
	}
	if c := strings.Compare(a.Repo, b.Repo); c != 0 {
		return c
	}
	if c := strings.Compare(a.Branch, b.Branch); c != 0 {
		return c
	}
	if c := strings.Compare(a.CWD, b.CWD); c != 0 {
		return c
	}
	return strings.Compare(a.Text, b.Text)
}

// Coverage is what the group as a whole can claim. The boundaries widen to the
// outermost any store reaches and the tallies add up, but the per-file skew
// does not combine: it is one file's own measurement, so the group reports the
// widest one and the file that carries it.
func (g *Group) Coverage() (Coverage, error) {
	var out Coverage
	for _, s := range g.stores {
		cov, err := s.Coverage()
		if err != nil {
			return Coverage{}, err
		}
		out = widen(out, cov)
	}
	return out, nil
}

func widen(out, cov Coverage) Coverage {
	if out.LiveFrom.IsZero() || (!cov.LiveFrom.IsZero() && cov.LiveFrom.Before(out.LiveFrom)) {
		out.LiveFrom = cov.LiveFrom
	}
	if out.ContentFrom.IsZero() || (!cov.ContentFrom.IsZero() && cov.ContentFrom.Before(out.ContentFrom)) {
		out.ContentFrom = cov.ContentFrom
	}
	if cov.ContentTo.After(out.ContentTo) {
		out.ContentTo = cov.ContentTo
	}
	out.LiveFiles += cov.LiveFiles
	out.Turns += cov.Turns
	out.Sessions += cov.Sessions
	if cov.MaxFileSkew > out.MaxFileSkew {
		out.MaxFileSkew, out.MaxSkewFile = cov.MaxFileSkew, cov.MaxSkewFile
	}
	return out
}
