package bench_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mayberuk/recall/bench"
)

// benchOutput is a run of `go test -bench . -benchmem` as the toolchain prints
// it: a header, a benchmark line per sub-benchmark, and the trailing status.
const benchOutput = `goos: darwin
goarch: arm64
pkg: github.com/mayberuk/recall/internal/scan
cpu: Apple M4 Max
BenchmarkSearch/small/single-term-16         	    1246	    999295 ns/op	    8744 B/op	      37 allocs/op
BenchmarkSearch/medium/single-term-16        	     100	  10000000 ns/op	   10264 B/op	      76 allocs/op
BenchmarkNoMemStats-16                       	    1000	      1234 ns/op
PASS
ok  	github.com/mayberuk/recall/internal/scan	9.317s
`

func TestParseBenchmarksReadsNameSizeAndMetrics(t *testing.T) {
	samples, err := bench.ParseBenchmarks(strings.NewReader(benchOutput))
	if err != nil {
		t.Fatalf("ParseBenchmarks: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("parsed %d samples, want the three benchmark lines", len(samples))
	}

	byName := map[string]bench.Sample{}
	for _, s := range samples {
		byName[s.Name] = s
	}
	small, ok := byName["BenchmarkSearch/small/single-term"]
	if !ok {
		t.Fatalf("the GOMAXPROCS suffix was not trimmed; got %v", samples)
	}
	if small.Size != bench.SizeSmall {
		t.Errorf("size is %q, want small — the corpus size is read from the benchmark name", small.Size)
	}
	if small.Allocs != 37 || small.Bytes != 8744 || small.Ns != 999295 {
		t.Errorf("small = %+v, want 37 allocs, 8744 B, 999295 ns", small)
	}
	if medium := byName["BenchmarkSearch/medium/single-term"]; medium.Size != bench.SizeMedium || medium.Allocs != 76 {
		t.Errorf("medium = %+v, want size medium and 76 allocs", medium)
	}
	// A benchmark run without -benchmem still has a cost worth reading, and
	// dropping the line would make it look as though the benchmark vanished.
	if plain := byName["BenchmarkNoMemStats"]; plain.Ns != 1234 || plain.Allocs != 0 {
		t.Errorf("the line without memory stats parsed as %+v, want 1234 ns and no allocations", plain)
	}
}

func TestParseBenchmarksIgnoresEverythingElse(t *testing.T) {
	samples, err := bench.ParseBenchmarks(strings.NewReader("PASS\nok  \tpkg\t1.0s\n--- FAIL: TestX\n"))
	if err != nil {
		t.Fatalf("ParseBenchmarks: %v", err)
	}
	if len(samples) != 0 {
		t.Errorf("parsed %d samples from output with no benchmark lines", len(samples))
	}
}

func TestCompareFailsOnASingleExtraAllocation(t *testing.T) {
	base := []bench.Sample{{Name: "BenchmarkSearch/small/single-term", Size: bench.SizeSmall, Allocs: 37, Bytes: 8744}}
	current := []bench.Sample{{Name: "BenchmarkSearch/small/single-term", Size: bench.SizeSmall, Allocs: 38, Bytes: 8744}}

	c := bench.Compare(base, current, 0.01)
	if len(c.Regressions) != 1 {
		t.Fatalf("Compare reported %d regressions, want the one added allocation", len(c.Regressions))
	}
	if got := c.Regressions[0].Ratio(); got < 1.02 || got > 1.03 {
		t.Errorf("ratio is %v, want 38/37", got)
	}
	if !strings.Contains(c.Regressions[0].String(), "37 -> 38") {
		t.Errorf("the regression reads %q, which does not name both numbers", c.Regressions[0])
	}
}

func TestCompareAcceptsGrowthInsideTheThreshold(t *testing.T) {
	base := []bench.Sample{{Name: "BenchmarkArchive/small/cold", Allocs: 157750}}
	// The measured run-to-run spread of the concurrent archive walk is a handful
	// of allocations in 157,750; a threshold of one percent must absorb it.
	current := []bench.Sample{{Name: "BenchmarkArchive/small/cold", Allocs: 157755}}

	if c := bench.Compare(base, current, 0.01); len(c.Regressions) != 0 {
		t.Errorf("a 0.003%% move was reported as a regression: %v", c.Regressions)
	}
	if c := bench.Compare(base, current, 0); len(c.Regressions) != 1 {
		t.Errorf("at a zero threshold the same move must fail, got %d regressions", len(c.Regressions))
	}
}

func TestCompareNamesWhatNeitherSideHolds(t *testing.T) {
	base := []bench.Sample{{Name: "BenchmarkGone", Allocs: 5}}
	current := []bench.Sample{{Name: "BenchmarkNew", Allocs: 5}}

	c := bench.Compare(base, current, 0)
	if len(c.Unrecorded) != 1 || c.Unrecorded[0] != "BenchmarkNew" {
		t.Errorf("Unrecorded = %v, want the benchmark missing from the baseline", c.Unrecorded)
	}
	if len(c.Unmeasured) != 1 || c.Unmeasured[0] != "BenchmarkGone" {
		t.Errorf("Unmeasured = %v, want the baseline entry this run skipped", c.Unmeasured)
	}
	if len(c.Regressions) != 0 {
		t.Errorf("a benchmark present on one side only was judged as a regression: %v", c.Regressions)
	}
}

// TestCompareTreatsAnAllocationWhereThereWasNoneAsARegression covers the
// boundary a ratio cannot express: any threshold multiplied by zero is zero, so
// the first allocation on a previously allocation-free path has to fail.
func TestCompareTreatsAnAllocationWhereThereWasNoneAsARegression(t *testing.T) {
	base := []bench.Sample{{Name: "BenchmarkFree", Allocs: 0}}
	current := []bench.Sample{{Name: "BenchmarkFree", Allocs: 1}}

	c := bench.Compare(base, current, 10)
	if len(c.Regressions) != 1 {
		t.Fatalf("Compare reported %d regressions, want the first allocation on a free path", len(c.Regressions))
	}
}

func TestBaselineRoundTripsWithoutWallClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allocs.json")
	samples := []bench.Sample{{Name: "BenchmarkSearch/small/single-term", Size: bench.SizeSmall, Allocs: 37, Bytes: 8744, Ns: 999295}}
	if err := bench.WriteBaseline(path, samples); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	got, err := bench.ReadBaseline(path)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if len(got.Samples) != 1 {
		t.Fatalf("read %d samples, want 1", len(got.Samples))
	}
	if got.Samples[0].Allocs != 37 || got.Samples[0].Bytes != 8744 {
		t.Errorf("read %+v, want the allocation figures back unchanged", got.Samples[0])
	}
	if got.Samples[0].Ns != 0 {
		t.Errorf("the baseline carried a wall clock of %v; it records hardware-independent figures only", got.Samples[0].Ns)
	}
	if got.Note == "" {
		t.Error("the baseline carries no note saying which figure is enforced")
	}
}

// TestTheCommittedBaselineCoversEveryMeasuredBenchmark is what keeps the
// committed file honest: a benchmark added without re-recording is a benchmark
// CI cannot judge.
func TestTheCommittedBaselineCoversEveryMeasuredBenchmark(t *testing.T) {
	b, err := bench.ReadBaseline(filepath.Join("baselines", "allocs.json"))
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	for _, s := range b.Samples {
		if s.Size == "" {
			t.Errorf("%s names no corpus size", s.Name)
		}
		if s.Allocs == 0 && s.Bytes == 0 {
			t.Errorf("%s records neither allocations nor bytes", s.Name)
		}
	}
}
