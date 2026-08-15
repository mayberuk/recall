package bench_test

import (
	"testing"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/bench/turns"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/scan"
)

// BenchmarkRank measures what a search costs after the scan: collapsing the
// copies of a record, and scoring what survives. It lives here rather than in
// internal/rank because it exercises the scan→rank boundary — feeding
// scan.Search output into rank.Dedup and rank.Rank — not rank in isolation.
func BenchmarkRank(b *testing.B) {
	for _, size := range bench.Sizes {
		g, err := bench.Corpus(size)
		if err != nil {
			b.Fatalf("Corpus(%s): %v", size, err)
		}
		corpus, err := turns.Load(size)
		if err != nil {
			b.Fatalf("Load(%s): %v", size, err)
		}
		queries, err := bench.Queries(g, nil)
		if err != nil {
			b.Fatalf("Queries(%s): %v", size, err)
		}

		// The conjunction is the shape worth ranking: a search matching one turn
		// would measure the cost of ranking nothing.
		var conjunction bench.Query
		for _, q := range queries {
			if q.Name == "conjunction" {
				conjunction = q
			}
		}
		res := scan.Search(corpus, scan.Query{Text: conjunction.Text, AllTerms: conjunction.AllTerms})
		if err := conjunction.Check(len(res.Hits), len(res.Terms)); err != nil {
			b.Fatal(err)
		}

		b.Run(string(size), func(b *testing.B) {
			b.Run("collapse", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					rank.Dedup(res.Hits)
				}
			})
			b.Run("score", func(b *testing.B) {
				hits, _ := rank.Dedup(res.Hits)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					rank.Rank(hits, res.TurnsBySession, rank.Concentration)
				}
			})
		})
	}
}
