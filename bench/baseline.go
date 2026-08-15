package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Sample is one benchmark's measured cost. Ns is deliberately absent from the
// committed baseline: wall clock says as much about the machine as about the
// code, where an allocation count is the same number everywhere.
type Sample struct {
	Name   string  `json:"name"`
	Size   Size    `json:"size"`
	Allocs float64 `json:"allocs_per_op"`
	Bytes  float64 `json:"bytes_per_op"`
	Ns     float64 `json:"-"`
}

// Baseline is the committed allocation record CI compares against.
type Baseline struct {
	Go      string   `json:"go"`
	Note    string   `json:"note"`
	Samples []Sample `json:"samples"`
}

// BaselineNote travels with the file so a reader who opens it without the
// surrounding docs still knows which figure is enforced and why the other is
// not.
const BaselineNote = "allocs_per_op is enforced; bytes_per_op is recorded but advisory, because the corpus root path is embedded in every turn and its length differs per machine"

// ParseBenchmarks reads `go test -bench` output and returns one Sample per
// benchmark line. Lines it does not recognise are skipped: the same stream
// carries the toolchain's own header and any log a benchmark printed.
func ParseBenchmarks(r io.Reader) ([]Sample, error) {
	var out []Sample
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		s, ok := parseBenchmarkLine(sc.Text())
		if ok {
			out = append(out, s)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("bench: cannot read benchmark output: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseBenchmarkLine(line string) (Sample, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
		return Sample{}, false
	}
	s := Sample{Name: trimProcs(fields[0])}
	s.Size = sizeIn(s.Name)
	found := false
	for i := 1; i < len(fields); i++ {
		value, err := strconv.ParseFloat(fields[i-1], 64)
		if err != nil {
			continue
		}
		switch fields[i] {
		case "ns/op":
			s.Ns, found = value, true
		case "B/op":
			s.Bytes = value
		case "allocs/op":
			s.Allocs = value
		}
	}
	return s, found
}

// trimProcs drops the -N suffix the toolchain appends for GOMAXPROCS, so a
// baseline taken on an 8-core machine is comparable on a 12-core one.
func trimProcs(name string) string {
	i := strings.LastIndexByte(name, '-')
	if i <= 0 {
		return name
	}
	if _, err := strconv.Atoi(name[i+1:]); err != nil {
		return name
	}
	return name[:i]
}

func sizeIn(name string) Size {
	for _, part := range strings.Split(name, "/") {
		switch Size(part) {
		case SizeSmall, SizeMedium, SizeLarge:
			return Size(part)
		}
	}
	return ""
}

// Change is one metric moving between the baseline and the current run.
type Change struct {
	Name     string
	Metric   string
	Baseline float64
	Current  float64
}

// Ratio is how much the current run costs relative to the baseline. A baseline
// of zero allocations that now allocates is reported as an infinite ratio
// rather than as a division by zero.
func (c Change) Ratio() float64 {
	if c.Baseline == 0 {
		if c.Current == 0 {
			return 1
		}
		return math.Inf(1)
	}
	return c.Current / c.Baseline
}

func (c Change) String() string {
	return fmt.Sprintf("%s %s: %.0f -> %.0f (%.2fx)", c.Name, c.Metric, c.Baseline, c.Current, c.Ratio())
}

// Comparison is what the baseline check found.
type Comparison struct {
	// Regressions are allocation counts past the threshold, which fail the run.
	Regressions []Change
	// ByteGrowth is bytes-per-op past the threshold, advisory by default.
	ByteGrowth []Change
	// Unrecorded is benchmarks the current run measured and the baseline does
	// not hold; Unmeasured is the reverse, which is how a deleted benchmark
	// stops being noticed.
	Unrecorded []string
	Unmeasured []string
}

// Compare checks a run against a baseline. threshold is the fraction a metric
// may grow before it counts as a regression: 0 means any growth at all.
func Compare(base, current []Sample, threshold float64) Comparison {
	byName := make(map[string]Sample, len(base))
	for _, s := range base {
		byName[s.Name] = s
	}
	seen := make(map[string]bool, len(current))

	var c Comparison
	for _, cur := range current {
		seen[cur.Name] = true
		b, ok := byName[cur.Name]
		if !ok {
			c.Unrecorded = append(c.Unrecorded, cur.Name)
			continue
		}
		if grew(b.Allocs, cur.Allocs, threshold) {
			c.Regressions = append(c.Regressions, Change{cur.Name, "allocs/op", b.Allocs, cur.Allocs})
		}
		if grew(b.Bytes, cur.Bytes, threshold) {
			c.ByteGrowth = append(c.ByteGrowth, Change{cur.Name, "B/op", b.Bytes, cur.Bytes})
		}
	}
	for _, b := range base {
		if !seen[b.Name] {
			c.Unmeasured = append(c.Unmeasured, b.Name)
		}
	}
	sort.Strings(c.Unrecorded)
	sort.Strings(c.Unmeasured)
	return c
}

func grew(base, current, threshold float64) bool {
	if current <= base {
		return false
	}
	return current > base*(1+threshold)
}

// ReadBaseline loads the committed allocation record.
func ReadBaseline(path string) (Baseline, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, fmt.Errorf("bench: cannot read the baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(body, &b); err != nil {
		return Baseline{}, fmt.Errorf("bench: cannot parse %s: %w", path, err)
	}
	if len(b.Samples) == 0 {
		return Baseline{}, fmt.Errorf("bench: %s records no benchmarks", path)
	}
	return b, nil
}

// WriteBaseline records a run as the new baseline. Only the hardware-independent
// figures are kept; ns/op is dropped on the way in by the Sample encoding.
func WriteBaseline(path string, samples []Sample) error {
	b := Baseline{Go: Facts().Go, Note: BaselineNote, Samples: samples}
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: cannot encode the baseline: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("bench: cannot write %s: %w", path, err)
	}
	return nil
}
