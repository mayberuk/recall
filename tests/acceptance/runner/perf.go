package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// Speed is a correctness property here: the design has no index because linear scanning measured
// fast, so an implementation that is materially slower makes the architecture wrong even while
// every unit test still passes. Gates and baselines are pinned in bench.Gate values, not here.

const perfRuns = 3

// timed measures a gate on a run expected to find something.
func (h *harness) timed(c *caseResult, op, baseline string, gateMS int64, runs []invocation) {
	h.timedExit(c, op, baseline, gateMS, 0, runs)
}

// timedExit measures a gate on a run expected to end on okExit. The startup
// gate times a query that deliberately matches nothing, and "nothing matched"
// is exit 1: demanding 0 there would fail the case for the tool behaving as its
// own contract says it must.
func (h *harness) timedExit(c *caseResult, op, baseline string, gateMS int64, okExit int, runs []invocation) {
	loadBefore := loadAverage()
	var ms []int64
	for _, in := range runs {
		res := c.run(in)
		ms = append(ms, res.DurationMS)
	}
	lo, hi := ms[0], ms[0]
	for _, v := range ms {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	c.Timing = &timingRecord{
		Operation:  op,
		GateMS:     gateMS,
		BaselineMS: baseline,
		RunsMS:     ms,
		MedianMS:   medianMS(ms),
		MinMS:      lo,
		MaxMS:      hi,
		LoadBefore: loadBefore,
		LoadAfter:  loadAverage(),
	}
	c.PassRules = []string{
		fmt.Sprintf("`timing.json` median_ms (%d) is at or below the gate of %d ms", c.Timing.MedianMS, gateMS),
		fmt.Sprintf("every run exited %d — a fast failure is not a fast success", okExit),
	}
	c.FailRules = []string{
		fmt.Sprintf("median_ms is above %d ms", gateMS),
		fmt.Sprintf("any run exited other than %d, timed out, or produced a start-error", okExit),
	}
	c.Notes = append(c.Notes,
		"A breach is a FAIL, not a warning. The remedy is never \"add an index\" — that reverses a ratified decision and is an escalation, not a fix.",
		"Wall-clock on a warm page cache, three runs, median, exactly as the contract specifies. The corpus was read in full by the harness before any measurement, so the cache is warm.",
		"Load average is recorded either side of the runs. This harness may be running while other portions build, and a wide min-to-max spread with a high load average is worth saying out loud in your report — but the verdict is still the median against the gate.")
}

func loadAverage() string {
	out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		return "unknown"
	}
	return strings.Trim(strings.TrimSpace(string(out)), "{} ")
}

// coldRun measures a first-ever run in a sandbox that has no archive yet.
func (h *harness) coldRun(label string) invocation {
	sb, err := newSandbox(h.tmpRoot, label, h.corpusRoot)
	if err != nil {
		return invocation{Label: label, Argv: []string{h.binary}, Dir: h.repoRoot}
	}
	return invocation{
		Label: label, Argv: []string{h.binary, "find", h.sel.A1Query, "--all"},
		Dir: h.repoRoot, Env: sb.env(),
	}
}

func (h *harness) p1(c *caseResult) {
	c.ID = "p1-cold-strip"
	c.Title = "cold full strip of the corpus"
	c.Asserts = "cold full strip, 1.29 GB — gate 4 s (measured baseline 1.31 s)"
	if !h.gate(c, "find") {
		return
	}
	c.fact("corpus", "%d files, %d records under %s", h.facts.FileCount, h.facts.LineCount, h.corpusRoot)
	c.fact("cold means", "a throwaway HOME with no archive yet; each measured run gets its own")

	runInvocation(h.coldRun("p1-cache-warm"))
	var runs []invocation
	for i := 0; i < perfRuns; i++ {
		runs = append(runs, h.coldRun(fmt.Sprintf("cold-strip-%d", i+1)))
	}
	h.timed(c, "cold full strip of the whole corpus", "1.31 s", 4000, runs)
	c.Notes = append(c.Notes,
		"The archive-building command is not pinned by the contract. The harness uses the first `find --all` in an empty archive, because the design builds freshness at query time.")
}

func (h *harness) p2(c *caseResult) {
	c.ID = "p2-incremental"
	c.Title = "incremental update with nothing changed"
	c.Asserts = "incremental update, nothing changed — gate 1.5 s (measured baseline 0.4 s)"
	if !h.gate(c, "find") {
		return
	}
	var runs []invocation
	for i := 0; i < perfRuns; i++ {
		runs = append(runs, invocation{
			Label: fmt.Sprintf("incremental-%d", i+1),
			Argv:  []string{h.binary, "find", h.sel.A1Query, "--all"},
			Dir:   h.repoRoot, Env: h.env(),
		})
	}
	h.timed(c, "repeat run over an archive that is already current", "0.4 s", 1500, runs)
	c.Notes = append(c.Notes,
		"Same argv as p1 but against the warm sandbox whose archive is already built, so what is being measured is the freshness check and nothing else.",
		"The contract's gate table treats \"incremental update\" and \"`find`\" as separate operations, but the pinned CLI surface has no separate update command. If the two are the same invocation in this build, p3's tighter 250 ms gate is the binding one. See logs/escalations/acceptance-2.md.")
}

func (h *harness) p3(c *caseResult) {
	c.ID = "p3-find-conversation"
	c.Title = "find over the conversation tier"
	c.Asserts = "find over the conversation tier — gate 250 ms (measured baseline ~35 ms)"
	if !h.gate(c, "find") {
		return
	}
	var runs []invocation
	for i := 0; i < perfRuns; i++ {
		runs = append(runs, invocation{
			Label: fmt.Sprintf("find-%d", i+1),
			Argv:  []string{h.binary, "find", h.sel.A1Query},
			Dir:   h.sel.A1Cwd, Env: h.env(),
		})
	}
	h.timed(c, "repo-scoped find over the conversation tier, warm archive", "~35 ms", 250, runs)
}

func (h *harness) p4(c *caseResult) {
	c.ID = "p4-find-results"
	c.Title = "find across all tiers"
	c.Asserts = "find --results (all tiers) — gate 1.2 s (measured baseline ~355 ms)"
	if !h.gate(c, "find") {
		return
	}
	var runs []invocation
	for i := 0; i < perfRuns; i++ {
		runs = append(runs, invocation{
			Label: fmt.Sprintf("find-results-%d", i+1),
			Argv:  []string{h.binary, "find", h.sel.A1Query, "--all", "--results"},
			Dir:   h.repoRoot, Env: h.env(),
		})
	}
	h.timed(c, "machine-wide find including the tool-result tier, warm archive", "~355 ms", 1200, runs)
}

func (h *harness) p5(c *caseResult) {
	c.ID = "p5-startup"
	c.Title = "binary start-to-exit on a trivial query"
	c.Asserts = "binary start-to-exit, trivial query — gate 100 ms"
	if !h.gate(c, "find") {
		return
	}
	qf := h.facts.Queries[h.sentinel]
	absent := qf.ConvTotal + qf.ResultTotal
	// A sentinel with hits anywhere sends `find` down the hit-elsewhere path, which skips the
	// terms-present-nearby survey entirely — the survey is most of what this gate measures. The
	// premise breaking therefore makes the run look faster, not slower, so it must block.
	if !c.requirePremise("the sentinel query matches nothing anywhere in the corpus",
		absent == 0,
		"0 hits in any tier, corpus-wide, so `find` takes the miss path this gate exists to time",
		fmt.Sprintf("%d hit(s): %d conversation-tier, %d tool-result-tier", absent, qf.ConvTotal, qf.ResultTotal)) {
		c.BlockReason += ". A sentinel with hits sends find down the cheaper hit-elsewhere path, so timing it would report a green for an operation this gate never measured"
		return
	}
	c.fact("sentinel query", "128 bits of randomness generated for this run, verified absent from all %d corpus files before any timing", h.facts.FileCount)
	c.fact("why it is generated and not fixed", "a hardcoded sentinel ends up in the corpus. This harness names its own token in prose, Claude Code writes that prose to a transcript, and the token then has hits — the same self-contamination that already moved the a1 and a6 premises. The literal token for this run is in each invocation's .cmd file; the next run uses a different one")
	c.fact("what the miss path costs", "a miss runs the terms-present-nearby survey; a query with hits elsewhere skips it. Those are different operations and only the first is what this gate names")

	var runs []invocation
	for i := 0; i < perfRuns; i++ {
		runs = append(runs, invocation{
			Label: fmt.Sprintf("trivial-%d", i+1),
			Argv:  []string{h.binary, "find", h.sentinel},
			Dir:   h.sel.A1Cwd, Env: h.env(),
		})
	}
	h.timedExit(c, "process start to exit on a query with nothing to render", "—", 100, 1, runs)
	c.Notes = append(c.Notes,
		"The contract gives no baseline for this one, only the 100 ms gate. A repo-scoped miss is the cheapest real invocation the pinned surface offers, so it is what start-to-exit is measured on.",
		"A miss triggers the wider probe of a5 and the terms-present-nearby survey, which is real work — if this gate breaks while p3 passes, that survey is the place to look before the process start is.")
}
