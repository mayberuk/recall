package bench_test

import (
	"os"
	"testing"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/bench/turns"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
)

func TestMain(m *testing.M) {
	code := m.Run()
	bench.Cleanup()
	os.Exit(code)
}

// TestEveryQueryMeasuresWhatItNames is the check the previous wall-clock gate
// lacked: each query shape is asserted against the corpus, so a miss query that
// started finding hits fails here instead of silently measuring a find.
func TestEveryQueryMeasuresWhatItNames(t *testing.T) {
	g, err := bench.Corpus(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	corpus, err := turns.Load(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	queries, err := bench.Queries(g, nil)
	if err != nil {
		t.Fatalf("Queries: %v", err)
	}
	if len(queries) != 5 {
		t.Fatalf("Queries returned %d shapes, want the five the report is built from", len(queries))
	}

	for _, q := range queries {
		res := scan.Search(corpus, scan.Query{Text: q.Text, Tiers: q.Tiers, AllTerms: q.AllTerms})
		if err := q.Check(len(res.Hits), len(res.Terms)); err != nil {
			t.Errorf("%v", err)
		}
		t.Logf("%-12s hits=%d sessions=%d carried=%d/%d", q.Name,
			len(res.Hits), res.SessionsScanned, res.Match.Required, res.Match.Total)
		switch q.Name {
		case "conjunction":
			if res.Match.Required != res.Match.Total {
				t.Errorf("the conjunction carried %d of %d terms, so it does not measure an all-terms search",
					res.Match.Required, res.Match.Total)
			}
		case "relaxed":
			if !res.Match.Relaxed() {
				t.Errorf("the relaxed query carried %d of %d terms, so it measures a satisfiable search",
					res.Match.Required, res.Match.Total)
			}
		}
	}
}

// TestTheResultTierNeedleIsInvisibleToAConversationSearch pins the tier split
// the all-tiers gate exists to measure: the two tier sets really do search
// different bytes.
func TestTheResultTierNeedleIsInvisibleToAConversationSearch(t *testing.T) {
	g, err := bench.Corpus(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	corpus, err := turns.Load(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	needle, err := g.Plant("result-only")
	if err != nil {
		t.Fatalf("Plant: %v", err)
	}

	if res := scan.Search(corpus, scan.Query{Text: needle.Term}); len(res.Hits) != 0 {
		t.Errorf("a conversation-tier search found the result-tier needle %d times", len(res.Hits))
	}
	all := []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}
	if res := scan.Search(corpus, scan.Query{Text: needle.Term, Tiers: all}); len(res.Hits) == 0 {
		t.Error("an all-tier search did not find the result-tier needle")
	}
}

// TestTheSentinelIsDifferentEveryTime guards the property the miss benchmark
// rests on. A sentinel that repeated could be in a corpus by the time it is
// searched for.
func TestTheSentinelIsDifferentEveryTime(t *testing.T) {
	seen := map[string]bool{}
	for range 16 {
		s, err := bench.Sentinel()
		if err != nil {
			t.Fatalf("Sentinel: %v", err)
		}
		if seen[s] {
			t.Fatalf("Sentinel repeated %q", s)
		}
		seen[s] = true
	}
}
