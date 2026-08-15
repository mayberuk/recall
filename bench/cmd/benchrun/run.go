package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mayberuk/recall/bench"
)

// measuredPackages hold the micro benchmarks. internal/rank's benchmark lives
// in ./bench instead, because it measures the scan→rank boundary rather than
// rank alone; everything else measures itself.
var measuredPackages = []string{"./internal/scan/", "./internal/strip/", "./internal/archive/", "./bench/"}

// packages prints what `make bench-micro` should run, so the list lives in one
// place: a Makefile with its own copy is a Makefile that measures a different
// set from the one the baseline was recorded over.
func packages() error {
	for i, pkg := range measuredPackages {
		if i > 0 {
			fmt.Print(" ")
		}
		fmt.Print(pkg)
	}
	fmt.Println()
	return nil
}

// corpora generates every size once and hands the directory to the child `go
// test` processes through the environment, so five processes share one 50 MB
// corpus instead of writing five.
func corpora(sizes []bench.Size) (map[bench.Size]bench.Generated, func(), error) {
	root, err := os.MkdirTemp(os.TempDir(), "recall-benchrun-")
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create a corpus directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	if err := os.Setenv(bench.CorpusRootEnv, root); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("cannot publish the corpus directory: %w", err)
	}

	out := make(map[bench.Size]bench.Generated, len(sizes))
	for _, size := range sizes {
		fmt.Fprintf(os.Stderr, "benchrun: generating the %s corpus\n", size)
		g, err := bench.Corpus(size)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		out[size] = g
	}
	return out, cleanup, nil
}

// runBenchmarks runs the micro benchmarks and returns what they measured. The
// raw output is returned too: `benchrun compare` reads the same text from a
// file, and a parser fed by two different producers is a parser that drifts.
func runBenchmarks(root string) ([]bench.Sample, string, error) {
	args := append([]string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-count", "1"}, measuredPackages...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(&out, os.Stderr)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, out.String(), fmt.Errorf("go test -bench: %w", err)
	}
	samples, err := bench.ParseBenchmarks(bytes.NewReader(out.Bytes()))
	if err != nil {
		return nil, out.String(), err
	}
	if len(samples) == 0 {
		return nil, out.String(), fmt.Errorf("go test -bench reported no benchmarks")
	}
	return samples, out.String(), nil
}

// buildRecall compiles the CLI the scenarios time. A scenario is only worth
// timing as a process: the working directory decides the scope of a search, and
// an in-process call cannot have one.
func buildRecall(root string) (string, func(), error) {
	dir, err := os.MkdirTemp(os.TempDir(), "recall-benchbin-")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create a build directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	binary := filepath.Join(dir, "recall")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/recall")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("go build ./cmd/recall: %w\n%s", err, out)
	}
	return binary, cleanup, nil
}

// repoRoot is the checkout benchrun measures. Every path it uses is relative to
// this, so running it from anywhere else would measure another tree or none.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot read the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above the working directory; run benchrun inside the repo")
		}
		dir = parent
	}
}
