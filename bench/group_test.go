package bench_test

import (
	"testing"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/bench/groupbench"
)

// TestGroupReadOfOneStoreAllocatesNoMoreThanADirectRead is the bench-scale
// counterpart to internal/archive's own unit test: it runs the same
// comparison over a generated corpus large enough that a merge — not just a
// copy — would move the count, and it is what `make bench-gate` enforces on
// every run rather than only on a change to internal/archive/group_test.go.
func TestGroupReadOfOneStoreAllocatesNoMoreThanADirectRead(t *testing.T) {
	g, err := bench.Corpus(bench.SizeSmall)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	a, err := groupbench.MeasureGroupAllocs(g.Root, t.TempDir())
	if err != nil {
		t.Fatalf("MeasureGroupAllocs: %v", err)
	}
	if a.Breached() {
		t.Fatalf("Group.Turns of one store allocates %.1f time(s) per call, Store.Turns direct allocates %.1f: "+
			"the single-store path is no longer a pass-through", a.Grouped, a.Direct)
	}
}
