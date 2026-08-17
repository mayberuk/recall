package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mayberuk/recall/bench"
)

const defaultBaseline = "bench/baselines/allocs.json"

func baseline(args []string) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	out := fs.String("out", defaultBaseline, "where to write the baseline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	_, cleanup, err := corpora(bench.Sizes)
	if err != nil {
		return err
	}
	defer cleanup()

	samples, _, err := runBenchmarks(root)
	if err != nil {
		return err
	}
	samples = bench.Judgeable(samples)
	path := abs(root, *out)
	if err := bench.WriteBaseline(path, samples); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "benchrun: recorded %d benchmarks in %s\n", len(samples), path)
	return nil
}

// compare is the regression check CI runs. It reads `go test -bench` output
// rather than running the benchmarks itself, so the numbers it judges are the
// ones the run actually printed.
func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	from := fs.String("in", "-", "file holding `go test -bench` output, or - for stdin")
	path := fs.String("baseline", defaultBaseline, "the committed baseline to compare against")
	threshold := fs.Float64("threshold", 0, "fraction a metric may grow before it counts as a regression")
	bytesFatal := fs.Bool("bytes", false, "fail on bytes-per-op growth as well as on allocations")
	requireComplete := fs.Bool("require-complete", false, "fail when a benchmark in the baseline was not measured")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *threshold < 0 {
		return fmt.Errorf("-threshold cannot be negative, got %v", *threshold)
	}

	body, err := readAll(*from)
	if err != nil {
		return err
	}
	current, err := bench.ParseBenchmarks(bytes.NewReader(body))
	if err != nil {
		return err
	}
	if len(current) == 0 {
		return fmt.Errorf("no benchmark lines in %s", describe(*from))
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	base, err := bench.ReadBaseline(abs(root, *path))
	if err != nil {
		return err
	}

	judged := bench.Judgeable(current)
	c := bench.Compare(base.Samples, judged, *threshold)
	for _, name := range c.Unrecorded {
		fmt.Fprintf(os.Stderr, "unrecorded: %s is not in the baseline; re-record it with `benchrun baseline`\n", name)
	}
	for _, name := range c.Unmeasured {
		fmt.Fprintf(os.Stderr, "unmeasured: %s is in the baseline and this run did not measure it\n", name)
	}
	for _, change := range c.ByteGrowth {
		fmt.Fprintf(os.Stderr, "bytes grew: %s\n", change)
	}
	for _, change := range c.Regressions {
		fmt.Fprintf(os.Stderr, "REGRESSION: %s\n", change)
	}

	var failures []string
	if len(c.Regressions) > 0 {
		failures = append(failures, fmt.Sprintf("%d allocation regression(s)", len(c.Regressions)))
	}
	if *bytesFatal && len(c.ByteGrowth) > 0 {
		failures = append(failures, fmt.Sprintf("%d bytes-per-op regression(s)", len(c.ByteGrowth)))
	}
	if *requireComplete && len(c.Unmeasured) > 0 {
		failures = append(failures, fmt.Sprintf("%d baseline benchmark(s) not measured", len(c.Unmeasured)))
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s against %s", joinLines(failures), *path)
	}
	fmt.Fprintf(os.Stderr, "benchrun: %d of %d benchmarks within the baseline; %d carry no allocation to judge\n",
		len(judged), len(current), len(current)-len(judged))
	return nil
}

func readAll(from string) ([]byte, error) {
	if from == "-" {
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("cannot read stdin: %w", err)
		}
		return body, nil
	}
	body, err := os.ReadFile(from)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", from, err)
	}
	return body, nil
}

func describe(from string) string {
	if from == "-" {
		return "stdin"
	}
	return from
}

func abs(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
