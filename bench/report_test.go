package bench_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/internal/schema"
)

func completeReport() bench.Report {
	return bench.Report{
		Machine:  bench.Machine{CPU: "Test CPU", Cores: 8, OS: "darwin", Arch: "arm64", Go: "go1.24.0"},
		Measured: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
		Corpora: []bench.CorpusFacts{{
			Size: bench.SizeSmall, Files: 17, DiskBytes: 5 << 20, Turns: 6287,
			Tiers: []bench.TierFacts{
				{Tier: schema.TierConversation, Turns: 6255, Bytes: 1 << 20},
				{Tier: schema.TierInvocation, Turns: 16, Bytes: 512},
				{Tier: schema.TierResult, Turns: 16, Bytes: 512},
			},
		}},
		Micro:     []bench.Sample{{Name: "BenchmarkSearch/small/single-term", Size: bench.SizeSmall, Ns: 999295, Bytes: 8744, Allocs: 37}},
		Scenarios: []bench.Measurement{{Name: "find bare", Size: bench.SizeSmall, Wall: 12 * time.Millisecond, OutBytes: 442, PeakBytes: 19 << 20}},
		Gates:     []bench.GateResult{{Gate: bench.FindConversation, Size: bench.SizeSmall, Measured: 11 * time.Millisecond, Detail: "1 hit"}},
	}
}

func TestValidateAcceptsAReportWithEveryCellFilled(t *testing.T) {
	if err := completeReport().Validate(); err != nil {
		t.Fatalf("Validate rejected a complete report: %v", err)
	}
}

// TestValidateRefusesAHole is the rule the report exists to enforce: a
// measurement that did not run is a failure to fix, not an empty cell to print.
func TestValidateRefusesAHole(t *testing.T) {
	for _, tc := range []struct {
		name string
		hole func(*bench.Report)
		want string
	}{
		{"a micro benchmark with no corpus", func(r *bench.Report) { r.Micro[0].Size = "" }, "names no corpus size"},
		{"a scenario with no corpus", func(r *bench.Report) { r.Scenarios[0].Size = "" }, "names no corpus size"},
		{"a scenario that was never timed", func(r *bench.Report) { r.Scenarios[0].Wall = 0 }, "no wall clock"},
		{"a gate that was never measured", func(r *bench.Report) { r.Gates[0].Measured = 0 }, "was not measured"},
		{"no scenarios at all", func(r *bench.Report) { r.Scenarios = nil }, "no scenarios"},
		{"an undescribed corpus", func(r *bench.Report) { r.Corpora[0].Files = 0 }, "is not described"},
		{"a corpus missing a tier", func(r *bench.Report) { r.Corpora[0].Tiers = r.Corpora[0].Tiers[:2] }, "is not described"},
		{"an unnamed machine", func(r *bench.Report) { r.Machine.CPU = "" }, "machine header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := completeReport()
			tc.hole(&r)
			err := r.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate said %q, which does not name the hole (%q)", err, tc.want)
			}
		})
	}
}

func TestMarkdownStampsTheMachineAndFillsEveryColumn(t *testing.T) {
	out := completeReport().Markdown()
	for _, want := range []string{
		"Test CPU", "8 cores", "darwin/arm64", "go1.24.0", "2026-08-14T12:00:00Z",
		"BenchmarkSearch/small/single-term", "recall find bare", bench.FindConversation.Name,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q", want)
		}
	}
	for _, row := range raggedOrEmptyRows(out) {
		t.Errorf("the report printed a row that does not line up with its header, or has a blank cell: %q", row)
	}
}

// raggedOrEmptyRows returns the table rows that do not carry one filled cell per
// header column. It is the check the report's own rule needs: "every cell must
// be filled" cannot be tested by looking for a placeholder, because the run that
// leaves a hole leaves it blank.
func raggedOrEmptyRows(markdown string) []string {
	var bad []string
	want := 0
	for _, line := range strings.Split(markdown, "\n") {
		if !strings.HasPrefix(line, "|") {
			want = 0
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if want == 0 {
			want = len(cells)
			continue
		}
		if len(cells) != want {
			bad = append(bad, line)
			continue
		}
		for _, cell := range cells {
			if strings.TrimSpace(cell) == "" {
				bad = append(bad, line)
				break
			}
		}
	}
	return bad
}

// TestMarkdownCallsOutABreachedGate keeps a breach from reading like a pass. A
// gate breach is a failure, and a table that renders it in the same words as a
// pass is a table that hides one.
func TestMarkdownCallsOutABreachedGate(t *testing.T) {
	r := completeReport()
	r.Gates[0].Measured = bench.FindConversation.Limit + time.Millisecond
	out := r.Markdown()
	if !strings.Contains(out, "BREACHED") {
		t.Errorf("a measurement over the gate rendered as %q", out[strings.Index(out, "## Gates"):])
	}
	if !r.Gates[0].Breached() {
		t.Error("Breached() is false one millisecond over the limit")
	}
}
