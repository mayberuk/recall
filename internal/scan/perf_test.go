package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/bench/turns"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

func TestMain(m *testing.M) {
	code := m.Run()
	bench.Cleanup()
	os.Exit(code)
}

// realTurns strips the live session store into turns. It only ever reads:
// ~/.claude/projects is the single copy of the corpus.
func realTurns(t testing.TB) []schema.Turn {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	root := filepath.Join(home, ".claude", "projects")

	var files []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	}); err != nil || len(files) == 0 {
		t.Skipf("no readable session store under %s", root)
	}

	s := strip.New()
	var out []schema.Turn
	for _, path := range files {
		r, err := jsonl.Open(path)
		if err != nil {
			continue
		}
		for r.Next() {
			if rec, ok := r.Record(); ok {
				stripped, _ := s.Strip(rec)
				out = append(out, stripped...)
			}
		}
		_ = r.Close()
	}
	return out
}

func tierBytes(corpus []schema.Turn, tiers ...schema.Tier) (count int, size int64) {
	want := map[schema.Tier]bool{}
	for _, tier := range tiers {
		want[tier] = true
	}
	for i := range corpus {
		if want[corpus[i].Tier] {
			count++
			size += int64(len(corpus[i].Text))
		}
	}
	return count, size
}

func median(t testing.TB, q Query, corpus []schema.Turn) (time.Duration, Result) {
	t.Helper()
	runs := make([]time.Duration, 3)
	var res Result
	for i := range runs {
		start := time.Now()
		res = Search(corpus, q)
		runs[i] = time.Since(start)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
	t.Logf("runs=%v median=%s", runs, runs[1])
	return runs[1], res
}

// TestScanOverTheRealCorpusWithinGates measures the machine's own session store,
// which is the only corpus with the real shape and the wrong one to gate a
// contributor's build on. `make bench-gate` enforces the same thresholds
// against a generated corpus; this reports what they cost here.
func TestScanOverTheRealCorpusWithinGates(t *testing.T) {
	if os.Getenv("RECALL_REAL_CORPUS") != "1" {
		t.Skip("set RECALL_REAL_CORPUS=1 to measure against ~/.claude/projects")
	}
	if raceDetector {
		t.Skip("the race detector slows execution several-fold, so a wall-clock gate cannot be judged under it")
	}
	corpus := realTurns(t)
	if len(corpus) == 0 {
		t.Skip("the session store stripped to nothing")
	}

	convTurns, convBytes := tierBytes(corpus, schema.TierConversation)
	allTurns, allBytes := tierBytes(corpus, allTiers...)
	t.Logf("corpus: %d turns, conversation %d turns / %d MB, all tiers %d turns / %d MB",
		len(corpus), convTurns, convBytes>>20, allTurns, allBytes>>20)

	const query = "checkout"

	conv, convRes := median(t, Query{Text: query}, corpus)
	t.Logf("conversation tier: %d hits over %d sessions in %s (gate %s)",
		len(convRes.Hits), convRes.SessionsScanned, conv, bench.FindConversation.Limit)
	if len(convRes.Hits) == 0 {
		t.Fatalf("%q matches nothing, so this measures the miss path and not a find", query)
	}
	if conv > bench.FindConversation.Limit {
		t.Errorf("conversation-tier scan took %s, over the %s gate", conv, bench.FindConversation.Limit)
	}

	all, allRes := median(t, Query{Text: query, Tiers: allTiers}, corpus)
	t.Logf("all tiers: %d hits over %d sessions in %s (gate %s)",
		len(allRes.Hits), allRes.SessionsScanned, all, bench.FindAllTiers.Limit)
	if all > bench.FindAllTiers.Limit {
		t.Errorf("all-tier scan took %s, over the %s gate", all, bench.FindAllTiers.Limit)
	}

	// The miss token is drawn at random per run. A token written into this file
	// is in a transcript as soon as an agent reads the file, Claude Code writes
	// that transcript into the corpus below, and the miss path then measures a
	// find: the fixed token this test used to carry had nine hits by the time it
	// was replaced.
	absent, err := bench.Sentinel()
	if err != nil {
		t.Fatalf("Sentinel: %v", err)
	}

	missConv, missConvRes := median(t, Query{Text: absent}, corpus)
	missAll, missAllRes := median(t, Query{Text: absent, Tiers: allTiers}, corpus)
	for _, res := range []Result{missConvRes, missAllRes} {
		if len(res.Hits) != 0 || len(res.Terms) == 0 {
			t.Fatalf("the sentinel found %d hits and produced %d term reports, so this does not measure the miss path",
				len(res.Hits), len(res.Terms))
		}
	}
	t.Logf("miss path with terms-present-nearby: conversation %s, all tiers %s", missConv, missAll)
	if missConv > bench.FindConversation.Limit {
		t.Errorf("conversation-tier miss took %s, over the %s gate", missConv, bench.FindConversation.Limit)
	}
	if missAll > bench.FindAllTiers.Limit {
		t.Errorf("all-tier miss took %s, over the %s gate", missAll, bench.FindAllTiers.Limit)
	}
}

// BenchmarkSearch measures the query shapes a caller actually types, against a
// corpus generated from a seed: the same bytes on any machine, and a miss token
// that provably misses because every byte of the corpus was written here.
func BenchmarkSearch(b *testing.B) {
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

		b.Run(string(size), func(b *testing.B) {
			for _, q := range queries {
				b.Run(q.Name, func(b *testing.B) {
					query := Query{Text: q.Text, Tiers: q.Tiers, AllTerms: q.AllTerms}
					if err := q.Check(searched(corpus, query)); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						Search(corpus, query)
					}
				})
			}
			b.Run("single-term-all-tiers", func(b *testing.B) {
				query := Query{Text: queries[0].Text, Tiers: allTiers}
				if err := queries[0].Check(searched(corpus, query)); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					Search(corpus, query)
				}
			})
		})
	}
}

// searched reports what one search found, which every benchmark checks before
// starting its timer: a query that stopped matching would otherwise keep
// reporting a number under the name of a search it is no longer doing.
func searched(corpus []schema.Turn, q Query) (hits, terms int) {
	res := Search(corpus, q)
	return len(res.Hits), len(res.Terms)
}
