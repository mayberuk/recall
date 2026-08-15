package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type premiseCheck struct {
	Name     string `json:"name"`
	Holds    bool   `json:"holds"`
	Expected string `json:"expected"`
	Measured string `json:"measured"`
	Note     string `json:"note,omitempty"`
}

type caseResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Asserts string `json:"asserts"`

	HarnessStatus string `json:"harness_status"` // RAN or BLOCKED — never a verdict
	BlockReason   string `json:"block_reason,omitempty"`

	Premises    []premiseCheck    `json:"premises"`
	Invocations []invResult       `json:"invocations"`
	Facts       map[string]string `json:"facts"`
	Timing      *timingRecord     `json:"timing,omitempty"`

	PassRules []string `json:"pass_rules"`
	FailRules []string `json:"fail_rules"`
	Notes     []string `json:"notes,omitempty"`

	extra map[string]any
}

func (c *caseResult) attach(name string, v any) {
	if c.extra == nil {
		c.extra = map[string]any{}
	}
	c.extra[name] = v
}

type timingRecord struct {
	Operation  string  `json:"operation"`
	GateMS     int64   `json:"gate_ms"`
	BaselineMS string  `json:"measured_baseline"`
	RunsMS     []int64 `json:"runs_ms"`
	MedianMS   int64   `json:"median_ms"`
	MinMS      int64   `json:"min_ms"`
	MaxMS      int64   `json:"max_ms"`
	LoadBefore string  `json:"machine_load_before"`
	LoadAfter  string  `json:"machine_load_after"`
}

func (c *caseResult) block(format string, args ...any) {
	c.HarnessStatus = "BLOCKED"
	c.BlockReason = fmt.Sprintf(format, args...)
}

func (c *caseResult) blocked() bool { return c.HarnessStatus == "BLOCKED" }

func (c *caseResult) fact(k, format string, args ...any) {
	if c.Facts == nil {
		c.Facts = map[string]string{}
	}
	c.Facts[k] = fmt.Sprintf(format, args...)
}

func (c *caseResult) premise(name string, holds bool, expected, measured string) bool {
	c.Premises = append(c.Premises, premiseCheck{Name: name, Holds: holds, Expected: expected, Measured: measured})
	return holds
}

// requirePremise blocks the case when a premise it was built on no longer holds. A premise that
// quietly changed meaning is the one way a green verdict becomes false confidence.
func (c *caseResult) requirePremise(name string, holds bool, expected, measured string) bool {
	c.premise(name, holds, expected, measured)
	if !holds {
		c.block("corpus premise %q no longer holds: expected %s, measured %s", name, expected, measured)
	}
	return holds
}

func (c *caseResult) run(in invocation) invResult {
	res := runInvocation(in)
	res.StdoutFile = fmt.Sprintf("%02d-%s.stdout", len(c.Invocations)+1, in.Label)
	res.StderrFile = fmt.Sprintf("%02d-%s.stderr", len(c.Invocations)+1, in.Label)
	c.Invocations = append(c.Invocations, res)
	return res
}

func (c *caseResult) write(root string) error {
	dir := filepath.Join(root, c.ID)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, v := range c.extra {
		if err := writeJSON(filepath.Join(dir, name), v); err != nil {
			return err
		}
	}

	for i, inv := range c.Invocations {
		n := i + 1
		if err := os.WriteFile(filepath.Join(dir, inv.StdoutFile), inv.stdout, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, inv.StderrFile), inv.stderr, 0o644); err != nil {
			return err
		}
		cmdText := renderCmd(inv)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-%s.cmd", n, inv.Label)), []byte(cmdText), 0o644); err != nil {
			return err
		}
		exitText := fmt.Sprintf("%d\n", inv.ExitCode)
		if inv.StartError != "" {
			exitText = fmt.Sprintf("%d\nstart-error: %s\n", inv.ExitCode, inv.StartError)
		}
		if inv.TimedOut {
			exitText += "timed-out: true\n"
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-%s.exit", n, inv.Label)), []byte(exitText), 0o644); err != nil {
			return err
		}
	}

	if err := writeJSON(filepath.Join(dir, "case.json"), c); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "premise.json"), c.Premises); err != nil {
		return err
	}
	if c.Timing != nil {
		if err := writeJSON(filepath.Join(dir, "timing.json"), c.Timing); err != nil {
			return err
		}
	}
	if c.blocked() {
		body := "BLOCKED\n\nreason: " + c.BlockReason + "\n\n" +
			"BLOCKED is not FAIL and is not PASS. The harness could not perform this check.\n" +
			"A wave whose acceptance is BLOCKED is unconverged, exactly like a failing one.\n"
		if err := os.WriteFile(filepath.Join(dir, "BLOCKED"), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "expected.md"), []byte(c.expectedMD()), 0o644)
}

func renderCmd(inv invResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cwd: %s\n", inv.Dir)
	keys := make([]string, 0, len(inv.EnvDelta))
	for k := range inv.EnvDelta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "env: %s=%s\n", k, inv.EnvDelta[k])
	}
	fmt.Fprintf(&b, "argv: %s\n", strings.Join(inv.Argv, " "))
	fmt.Fprintf(&b, "exit: %d\n", inv.ExitCode)
	fmt.Fprintf(&b, "wall_ms: %d\n", inv.DurationMS)
	fmt.Fprintf(&b, "stdout_bytes: %d\n", inv.StdoutBytes)
	fmt.Fprintf(&b, "stderr_bytes: %d\n", inv.StderrBytes)
	return b.String()
}

func (c *caseResult) expectedMD() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", c.ID, c.Title)

	b.WriteString("## Read this first\n\n")
	b.WriteString("Rule one: if a file named `BLOCKED` exists in this directory, the verdict is **BLOCKED**.\n")
	b.WriteString("Stop there. Do not read it as FAIL and do not read it as PASS — the harness could not\n")
	b.WriteString("perform the check at all, and a BLOCKED case leaves the wave unconverged just as a\n")
	b.WriteString("failing one does.\n\n")
	b.WriteString("Otherwise rule exactly one of **PASS** or **FAIL** from the rules below. \"Mostly passed\"\n")
	b.WriteString("is a malformed verdict. Judge only from the files in this directory — do not run the\n")
	b.WriteString("binary, do not read the product source.\n\n")

	if c.blocked() {
		fmt.Fprintf(&b, "**This case is BLOCKED.** Reason: %s\n\n", c.BlockReason)
	}

	b.WriteString("## What the contract asks of this case\n\n")
	fmt.Fprintf(&b, "> %s\n\n", c.Asserts)
	b.WriteString("(`docs/orchestration.md` §Acceptance harness. Every expected value below comes from that\n")
	b.WriteString("contract or from a read-only measurement of the corpus, never from the tool's own output.)\n\n")

	if len(c.Premises) > 0 {
		b.WriteString("## Corpus premises, re-verified at run time\n\n")
		b.WriteString("| premise | holds | expected | measured |\n|---|---|---|---|\n")
		for _, p := range c.Premises {
			fmt.Fprintf(&b, "| %s | %t | %s | %s |\n", p.Name, p.Holds, md(p.Expected), md(p.Measured))
		}
		b.WriteString("\n")
	}

	if len(c.Facts) > 0 {
		b.WriteString("## Measured facts this case's expectations rest on\n\n")
		keys := make([]string, 0, len(c.Facts))
		for k := range c.Facts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- **%s**: %s\n", k, c.Facts[k])
		}
		b.WriteString("\n")
	}

	if len(c.Invocations) > 0 {
		b.WriteString("## Evidence files\n\n")
		b.WriteString("| # | label | argv | exit | stdout bytes | wall ms |\n|---|---|---|---|---|---|\n")
		for i, inv := range c.Invocations {
			fmt.Fprintf(&b, "| %02d | %s | `%s` | %d | %d | %d |\n",
				i+1, inv.Label, md(strings.Join(inv.Argv, " ")), inv.ExitCode, inv.StdoutBytes, inv.DurationMS)
		}
		b.WriteString("\nEach numbered invocation has four files: `NN-label.cmd` (cwd, environment overrides,\n")
		b.WriteString("argv), `NN-label.stdout`, `NN-label.stderr`, `NN-label.exit` (exit code, plus\n")
		b.WriteString("`start-error:` or `timed-out:` when either happened).\n\n")
	}

	if c.Timing != nil {
		t := c.Timing
		b.WriteString("## Timing\n\n")
		fmt.Fprintf(&b, "Operation: %s. Gate: **%d ms** (fails above). Design baseline: %s.\n",
			t.Operation, t.GateMS, t.BaselineMS)
		fmt.Fprintf(&b, "Runs: %v ms. **Median: %d ms.** Min %d, max %d.\n", t.RunsMS, t.MedianMS, t.MinMS, t.MaxMS)
		fmt.Fprintf(&b, "Machine load average before: %s — after: %s.\n\n", t.LoadBefore, t.LoadAfter)
		b.WriteString("The gate applies to the median, and the gate figure is the contract's, not the harness's\n")
		b.WriteString("opinion. A breach is a FAIL, not a warning, and the remedy is never \"add an index\" —\n")
		b.WriteString("that reverses a ratified decision. Min, max and load average are recorded so a wide\n")
		b.WriteString("spread on a busy machine can be reported as such; they do not soften the verdict.\n\n")
	}

	b.WriteString("## Verdict rules\n\n")
	b.WriteString("PASS requires **all** of:\n\n")
	for _, r := range c.PassRules {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	b.WriteString("\nFAIL if **any** of:\n\n")
	for _, r := range c.FailRules {
		fmt.Fprintf(&b, "- %s\n", r)
	}
	b.WriteString("\n")

	if len(c.Notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, n := range c.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func md(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
