package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mayberuk/recall/bench"
	"github.com/mayberuk/recall/bench/turns"
	"github.com/mayberuk/recall/internal/schema"
)

func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	out := fs.String("out", filepath.Join("bench", "RESULTS.md"), "where to write the report")
	gateSize := fs.String("gate-size", string(bench.SizeMedium), "corpus size the gates are enforced against")
	runs := fs.Int("runs", 3, "timed runs per scenario; the median is reported")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	generated, cleanup, err := corpora(bench.Sizes)
	if err != nil {
		return err
	}
	defer cleanup()

	facts, err := describeCorpora(generated)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "benchrun: running the micro benchmarks")
	micro, _, err := runBenchmarks(root)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "benchrun: timing the scenarios")
	scenarios, err := measureScenarios(root, generated, *runs)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "benchrun: measuring the gates")
	gates, err := measureGates(bench.Size(*gateSize))
	if err != nil {
		return err
	}

	r := bench.Report{
		Machine:   bench.Facts(),
		Measured:  time.Now(),
		Corpora:   facts,
		Micro:     micro,
		Scenarios: scenarios,
		Gates:     gates,
	}
	if err := r.Validate(); err != nil {
		return err
	}
	path := *out
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if err := os.WriteFile(path, []byte(r.Markdown()), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "benchrun: wrote %s\n", path)

	// The report is written before the verdict so a breach leaves the evidence
	// on disk, and it still fails the run: a gate breach is a failure.
	return reportGates(gates)
}

// describeCorpora records what each measured corpus holds, so the report states
// the shape of the thing it measured instead of leaving a reader to assume it
// resembles a session store.
func describeCorpora(generated map[bench.Size]bench.Generated) ([]bench.CorpusFacts, error) {
	var out []bench.CorpusFacts
	for _, size := range bench.Sizes {
		g, ok := generated[size]
		if !ok {
			return nil, fmt.Errorf("the %s corpus was not generated", size)
		}
		files, err := bench.Files(g.Root)
		if err != nil {
			return nil, err
		}
		var onDisk int64
		for _, path := range files {
			st, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("cannot size %s: %w", path, err)
			}
			onDisk += st.Size()
		}
		corpus, err := turns.Load(size)
		if err != nil {
			return nil, err
		}
		facts := bench.CorpusFacts{Size: size, Files: len(files), DiskBytes: onDisk, Turns: len(corpus)}
		for _, tier := range []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult} {
			t := bench.TierFacts{Tier: tier}
			for i := range corpus {
				if corpus[i].Tier == tier {
					t.Turns++
					t.Bytes += int64(len(corpus[i].Text))
				}
			}
			facts.Tiers = append(facts.Tiers, t)
		}
		out = append(out, facts)
	}
	return out, nil
}

// measureScenarios times the built binary against every corpus size. The
// archive is built once per size before the timed runs, because the first
// invocation of all pays for the cold build and would otherwise be reported as
// the cost of whichever scenario happened to run first.
func measureScenarios(root string, generated map[bench.Size]bench.Generated, runs int) ([]bench.Measurement, error) {
	binary, cleanup, err := buildRecall(root)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	var out []bench.Measurement
	for _, size := range bench.Sizes {
		g, ok := generated[size]
		if !ok {
			return nil, fmt.Errorf("the %s corpus was not generated", size)
		}
		archiveDir, err := os.MkdirTemp(os.TempDir(), "recall-benchscen-")
		if err != nil {
			return nil, fmt.Errorf("cannot create an archive directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(archiveDir) }()
		env := g.Env(archiveDir)

		scenarios, err := bench.Scenarios(g)
		if err != nil {
			return nil, err
		}
		if _, err := scenarios[0].Run(binary, env); err != nil {
			return nil, fmt.Errorf("building the %s archive: %w", size, err)
		}
		for _, s := range scenarios {
			m, err := s.Median(binary, env, runs)
			if err != nil {
				return nil, err
			}
			m.Size = size
			out = append(out, m)
		}
	}
	return out, nil
}
