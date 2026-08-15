package bench

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/mayberuk/recall/internal/corpusgen"
	"github.com/mayberuk/recall/internal/schema"
)

// Query describes one measured search, with what the corpus guarantees about
// its answer. It carries no scan.Query so that internal/scan's own benchmarks
// can import this package; each caller builds the query it runs.
//
// The guarantee is the part that matters: a miss query that started finding
// hits measures a find under the name of a miss, which is how the previous
// wall-clock gate quietly stopped measuring what it claimed to.
type Query struct {
	Name     string
	Text     string
	Tiers    []schema.Tier
	AllTerms bool
	Hits     bool
}

// The filler vocabulary is drawn uniformly at every word position, so any two
// of its words co-occur inside a turn, and adjacently somewhere, in a corpus of
// either measured size. These two are words the tokenizer keeps: a stopword
// would be dropped from a multi-term query and the benchmark would silently
// measure a shorter one.
const (
	commonA = "worker"
	commonB = "timeout"
)

// Queries is the search shapes worth timing, in reporting order. The needles
// come from the generator rather than from this file, because a term written
// here is in a transcript as soon as an agent reads it, and from then on it has
// hits nobody planted.
func Queries(g Generated, tiers []schema.Tier) ([]Query, error) {
	single, err := g.Plant(corpusgen.KindSingleSession)
	if err != nil {
		return nil, err
	}
	cross, err := g.Plant(corpusgen.KindCrossCheckout)
	if err != nil {
		return nil, err
	}
	miss, err := Sentinel()
	if err != nil {
		return nil, err
	}
	return []Query{
		{Name: "single-term", Text: single.Term, Tiers: tiers, Hits: true},
		{Name: "conjunction", Text: commonA + " " + commonB, Tiers: tiers, AllTerms: true, Hits: true},
		// Two needles planted in different sessions cannot both sit in one turn,
		// so this is the relaxation path: the best turn carries one of the two.
		{Name: "relaxed", Text: single.Term + " " + cross.Term, Tiers: tiers, Hits: true},
		{Name: "phrase", Text: `"` + commonA + " " + commonB + `"`, Tiers: tiers, Hits: true},
		{Name: "miss", Text: miss, Tiers: tiers, Hits: false},
	}, nil
}

// Sentinel is a token no corpus written before this moment can carry. A fixed
// one is guaranteed to end up in the operator's session store — this harness
// names its token in prose, Claude Code writes that prose to a transcript, and
// from then on the token has hits. Its length is fixed so that two runs compile
// the same size of matcher and allocate identically.
func Sentinel() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("bench: cannot draw a miss-path token: %w", err)
	}
	return "zzq" + hex.EncodeToString(b), nil
}

// Check reports whether a search matched what the query promised. terms is the
// count of per-term reports, which a miss must produce: a zero-hit search that
// skipped the nearby-term survey took a cheaper path than the one being timed.
func (q Query) Check(hits, terms int) error {
	switch {
	case q.Hits && hits == 0:
		return fmt.Errorf("bench: query %s found nothing, so it measures the miss path and not a find", q.Name)
	case !q.Hits && hits != 0:
		return fmt.Errorf("bench: query %s found %d hits, so it does not measure the miss path", q.Name, hits)
	case !q.Hits && terms == 0:
		return fmt.Errorf("bench: query %s reported no terms, so the miss path skipped the nearby-term survey", q.Name)
	}
	return nil
}
