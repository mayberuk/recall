package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/bench/groupbench"
	"github.com/mayberuk/recall/bench/turns"
	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

func gate(args []string) error {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	size := fs.String("size", string(bench.SizeMedium), "corpus size to enforce the gates against")
	if err := fs.Parse(args); err != nil {
		return err
	}
	gens, cleanup, err := corpora([]bench.Size{bench.Size(*size)})
	if err != nil {
		return err
	}
	defer cleanup()

	results, err := measureGates(bench.Size(*size))
	if err != nil {
		return err
	}
	allocs, err := groupbench.MeasureGroupAllocs(gens[bench.Size(*size)].Root, mustTempDir())
	if err != nil {
		return err
	}

	return errors.Join(reportGates(results), reportGroupAllocs(allocs))
}

// reportGroupAllocs prints the group-vs-direct allocation check in the same
// shape reportGates prints a wall-clock gate, so both read the same way: a
// verdict, a name, the measured figure, and what it is judged against. It is
// not a time.Duration gate because a copy or merge small enough to leave a
// wall-clock gate untouched would still show up here.
func reportGroupAllocs(a groupbench.GroupAllocs) error {
	verdict := "ok"
	if a.Breached() {
		verdict = "BREACHED"
	}
	fmt.Fprintf(os.Stderr, "%-8s %-34s %8.1f allocs  (gate %.1f allocs)  %s\n",
		verdict, "group read vs direct read", a.Grouped, a.Direct,
		"Group.Turns of one store must allocate no more than Store.Turns")
	if a.Breached() {
		return fmt.Errorf("group read of one store allocates %.1f time(s) per call against a direct read's %.1f",
			a.Grouped, a.Direct)
	}
	return nil
}

// reportGates prints every measurement and fails on any breach, naming it. A
// breach is a failure and not a warning: the architecture has no index because
// linear scanning measured fast enough, so the measurement is what the design
// rests on.
func reportGates(results []bench.GateResult) error {
	var breached []string
	for _, r := range results {
		verdict := "ok"
		if r.Breached() {
			verdict = "BREACHED"
			breached = append(breached, fmt.Sprintf("%s took %s against a %s gate", r.Gate.Name, r.Measured, r.Gate.Limit))
		}
		fmt.Fprintf(os.Stderr, "%-8s %-34s %8.1f ms  (gate %.0f ms)  %s\n",
			verdict, r.Gate.Name, ms(r.Measured), ms(r.Gate.Limit), r.Detail)
	}
	if len(breached) == 0 {
		return nil
	}
	return fmt.Errorf("%d gate(s) breached:\n  %s", len(breached), joinLines(breached))
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// measureGates runs every threshold against one generated corpus. The
// measurements share a corpus and a strip pass on purpose: a gate measured
// against a different corpus than its neighbour is a gate nobody can compare.
func measureGates(size bench.Size) ([]bench.GateResult, error) {
	g, err := bench.Corpus(size)
	if err != nil {
		return nil, err
	}

	stripCold, err := medianErr(3, func() error {
		_, err := turns.Strip(g.Root)
		return err
	})
	if err != nil {
		return nil, err
	}

	corpus, err := turns.Load(size)
	if err != nil {
		return nil, err
	}
	queries, err := bench.Queries(g, nil)
	if err != nil {
		return nil, err
	}
	single := queries[0]

	conv, convHits, err := medianSearch(corpus, single, nil)
	if err != nil {
		return nil, err
	}
	allTiers := []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}
	all, allHits, err := medianSearch(corpus, single, allTiers)
	if err != nil {
		return nil, err
	}

	store, err := archive.Open(archive.Options{
		Dir:     mustTempDir(),
		Root:    g.Root,
		Strip:   strip.New().Strip,
		Resolve: repo.New().Repo,
	})
	if err != nil {
		return nil, err
	}
	start := time.Now()
	cold, err := store.Update()
	if err != nil {
		return nil, err
	}
	coldTook := time.Since(start)

	start = time.Now()
	warm, err := store.Update()
	if err != nil {
		return nil, err
	}
	warmTook := time.Since(start)
	if warm.FilesSkipped != warm.FilesSeen {
		return nil, fmt.Errorf("the incremental pass re-read %d of %d files of a corpus nothing appended to",
			warm.FilesSeen-warm.FilesSkipped, warm.FilesSeen)
	}

	loadConv, err := bestLoad(store, schema.TierConversation)
	if err != nil {
		return nil, err
	}
	loadAll, err := bestLoad(store)
	if err != nil {
		return nil, err
	}

	return []bench.GateResult{
		{Gate: bench.FindConversation, Size: size, Measured: conv, Detail: fmt.Sprintf("%d hits over %d turns", convHits, len(corpus))},
		{Gate: bench.FindAllTiers, Size: size, Measured: all, Detail: fmt.Sprintf("%d hits over %d turns", allHits, len(corpus))},
		{Gate: bench.StripCold, Size: size, Measured: stripCold, Detail: fmt.Sprintf("%d turns", len(corpus))},
		{Gate: bench.ArchiveCold, Size: size, Measured: coldTook, Detail: fmt.Sprintf("%d files, %d records", cold.FilesSeen, cold.RecordsRead)},
		{Gate: bench.ArchiveIncremental, Size: size, Measured: warmTook, Detail: fmt.Sprintf("%d of %d files skipped", warm.FilesSkipped, warm.FilesSeen)},
		{Gate: bench.LoadConversation, Size: size, Measured: loadConv, Detail: "best of three reads"},
		{Gate: bench.LoadAllTiers, Size: size, Measured: loadAll, Detail: "best of three reads"},
	}, nil
}

func mustTempDir() string {
	dir, err := os.MkdirTemp(os.TempDir(), "recall-benchgate-")
	if err != nil {
		return os.TempDir()
	}
	return dir
}

// medianSearch times a search three times and checks it found what the query
// promised, so a gate cannot pass by measuring a search that matched nothing.
func medianSearch(corpus []schema.Turn, q bench.Query, tiers []schema.Tier) (time.Duration, int, error) {
	var res scan.Result
	took, err := medianErr(3, func() error {
		res = scan.Search(corpus, scan.Query{Text: q.Text, Tiers: tiers, AllTerms: q.AllTerms})
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if err := q.Check(len(res.Hits), len(res.Terms)); err != nil {
		return 0, 0, err
	}
	return took, len(res.Hits), nil
}

// bestLoad reports the quickest of three reads, which is what a CLI invoked
// repeatedly against a warm page cache actually pays.
func bestLoad(store *archive.Store, tiers ...schema.Tier) (time.Duration, error) {
	best := time.Duration(1<<62 - 1)
	for range 3 {
		start := time.Now()
		if _, err := store.Turns(tiers...); err != nil {
			return 0, err
		}
		if took := time.Since(start); took < best {
			best = took
		}
	}
	return best, nil
}

func medianErr(runs int, fn func() error) (time.Duration, error) {
	took := make([]time.Duration, 0, runs)
	for range runs {
		start := time.Now()
		if err := fn(); err != nil {
			return 0, err
		}
		took = append(took, time.Since(start))
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	return took[len(took)/2], nil
}
