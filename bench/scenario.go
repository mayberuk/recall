package bench

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/mayberuk/recall/internal/corpusgen"
)

// Scenario is one invocation of the built binary, end to end. The flags are
// the ones that change how much work a verb does — how many tiers it reads,
// how much it renders, how much it filters — because those are what a caller
// choosing between them is choosing between.
type Scenario struct {
	Name string
	Args []string

	// Dir is the checkout the process runs in. Scope is decided by the working
	// directory, so a scenario that stood anywhere else would measure a search
	// of a different size.
	Dir string
}

// Measurement is one scenario's cost.
type Measurement struct {
	Name      string
	Size      Size
	Wall      time.Duration
	OutBytes  int
	PeakBytes int64
}

// The corpus epoch is fixed by the generator, so an absolute date narrows the
// same way on any machine and in any year; a relative age would drift past the
// whole corpus and start measuring a filter that rejects everything.
const sinceDate = "2025-12-01"

// Scenarios is the invocation set, in reporting order.
func Scenarios(g Generated) ([]Scenario, error) {
	single, err := g.Plant(corpusgen.KindSingleSession)
	if err != nil {
		return nil, err
	}
	result, err := g.Plant(corpusgen.KindResultOnly)
	if err != nil {
		return nil, err
	}
	at := single.Cwd
	term := single.Term

	find := func(name string, args ...string) Scenario {
		return Scenario{Name: "find " + name, Args: append([]string{"find", term}, args...), Dir: at}
	}
	return []Scenario{
		find("bare"),
		find("--all", "--all"),
		{Name: "find --results", Args: []string{"find", result.Term, "--results"}, Dir: at},
		find("--tools", "--tools"),
		find("--brief", "--brief"),
		find("--json", "--json"),
		find("--format jsonl", "--format", "jsonl"),
		find("--ids", "--ids"),
		find("--all-terms", "--all-terms"),
		find("--not", "--not", commonA),
		find("--since", "--since", sinceDate),
		find("--author", "--author", "assistant"),
		find("--repo", "--repo", "repo01"),
		find("--limit", "--limit", "25"),
		{Name: "turns bare", Args: []string{"turns", term}, Dir: at},
		{Name: "turns --budget", Args: []string{"turns", term, "--budget", "500"}, Dir: at},
		{Name: "turns --brief", Args: []string{"turns", term, "--brief"}, Dir: at},
		{Name: "show bare", Args: []string{"show", single.Session}, Dir: at},
		{Name: "show --chars", Args: []string{"show", single.Session, "--chars", "200"}, Dir: at},
		{Name: "when", Args: []string{"when", term}, Dir: at},
		{Name: "doctor", Args: []string{"doctor"}, Dir: at},
		{Name: "guide", Args: []string{"guide"}, Dir: at},
	}, nil
}

// Env is the environment a scenario runs in. Every location recall reads is
// derived from HOME, so emptying the overrides — an empty variable reads as
// unset — is what proves a measurement came from the generated corpus and not
// from the operator's own session store.
func (g Generated) Env(archiveDir string) []string {
	return append(os.Environ(),
		"HOME="+g.Home,
		"RECALL_HOME="+archiveDir,
		"XDG_DATA_HOME=",
		"CLAUDE_PROJECTS_DIR=",
		"CLAUDE_CODE_SESSION_ID=",
		"NO_COLOR=1",
	)
}

// Run times one invocation. Output is counted rather than kept: what a caller
// pays for an answer is its size, and holding megabytes of it would distort the
// measurement of the next one.
func (s Scenario) Run(binary string, env []string) (Measurement, error) {
	cmd := exec.Command(binary, s.Args...)
	cmd.Dir = s.Dir
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	start := time.Now()
	err := cmd.Run()
	took := time.Since(start)

	// Exit 1 is a search that found nothing, which is a legitimate answer and a
	// measurable one; anything else means the scenario did not run and the
	// report would otherwise carry a number for work that never happened.
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return Measurement{}, fmt.Errorf("bench: scenario %q (%v) failed: %v: %s",
				s.Name, s.Args, err, bytes.TrimSpace(errOut.Bytes()))
		}
	}
	return Measurement{
		Name:      s.Name,
		Wall:      took,
		OutBytes:  out.Len(),
		PeakBytes: peakBytes(cmd.ProcessState),
	}, nil
}

// peakBytes is the process's high-water resident set. Linux reports it in
// kilobytes and darwin in bytes; the ratio between two scenarios is the point,
// so the unit is normalised rather than left to the reader.
func peakBytes(st *os.ProcessState) int64 {
	ru, ok := st.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "linux" {
		return int64(ru.Maxrss) << 10
	}
	return int64(ru.Maxrss)
}

// Median runs a scenario several times and keeps the middle wall clock. One
// timing of a process includes whatever the machine was doing at that instant;
// the median of an odd number of runs does not.
func (s Scenario) Median(binary string, env []string, runs int) (Measurement, error) {
	if runs < 1 {
		return Measurement{}, fmt.Errorf("bench: scenario %q needs at least one run, got %d", s.Name, runs)
	}
	measured := make([]Measurement, 0, runs)
	for range runs {
		m, err := s.Run(binary, env)
		if err != nil {
			return Measurement{}, err
		}
		measured = append(measured, m)
	}
	sort.Slice(measured, func(i, j int) bool { return measured[i].Wall < measured[j].Wall })
	return measured[len(measured)/2], nil
}
