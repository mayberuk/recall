package main

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
)

// TestTurnsMergesTheBudgetLineWithAnIdenticalLimitLine mirrors
// TestFindMergesTheBudgetLineWithAnIdenticalLimitLine for turns's own footer:
// a --budget cap landing on the exact cut --limit already reported for
// "matched turns" must be named on that one line, not restated below it.
func TestTurnsMergesTheBudgetLineWithAnIdenticalLimitLine(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	// "this" matches 8 of the corpus's turns; --limit 3 cuts that to 3, and a
	// --budget too small for any of it to fit forces every retry down to the
	// same one-of-8 answer --limit would have reported.
	out, err := callTurns(t, "this", "--all", "--limit", "3", "--budget", "1")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if want := "── showing 1 of 8 matched turns (--limit, --budget)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not merge --limit and --budget onto one line: want %q, got:\n%s", want, out)
	}
	// The actual regression guard: a fix that appends the merged line above
	// while still also emitting the old standalone line would still pass the
	// assertion above without this one.
	if dup := "── showing 1 of 8 matched turns (--budget)"; hasFooterLine(out, dup) {
		t.Errorf("footer still carries the standalone --budget duplicate alongside the merged line:\n%s", out)
	}
}

// TestTurnsBudgetLineStandsAloneWhenNoLimitReportsTheSameCut: a --budget cap
// that no existing --limit line already describes still gets its own line,
// exactly as before the merge was introduced.
func TestTurnsBudgetLineStandsAloneWhenNoLimitReportsTheSameCut(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	// NeedleConversation matches exactly one turn, well under the default
	// --limit (5 passages), so --limit never caps anything for --budget to
	// coincide with.
	out, err := callTurns(t, fixtures.NeedleConversation, "--all", "--budget", "1")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if want := "── showing 1 of 1 matched turns (--budget)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not name the standalone --budget cap: want %q, got:\n%s", want, out)
	}
	if hasFooterLine(out, "── showing 1 of 1 matched turns (--limit)") {
		t.Errorf("footer names a --limit cap that never fired:\n%s", out)
	}
}

// TestTurnsNamesNoBudgetWithoutBudgetShaping is the negative control: a call
// that never passes --budget must never mention it in the footer, merged or
// standalone.
func TestTurnsNamesNoBudgetWithoutBudgetShaping(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callTurns(t, "this", "--all", "--limit", "3")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if want := "── showing 3 of 8 matched turns (--limit)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not name the --limit cap: want %q, got:\n%s", want, out)
	}
	if strings.Contains(out, "--budget") {
		t.Errorf("a call with no --budget flag named --budget in the footer:\n%s", out)
	}
}

// An existing limit sharing turns's "matched turns" What but disagreeing on
// Shown or Total is covered by
// TestMergeBudgetLimitKeepsSeparateLinesForDifferentNumbers in
// cmd_find_test.go, against the same mergeBudgetLimit both this file and
// cmd_find.go call. It is not reachable through turns's own CLI: within one
// answer, the --limit and --budget entries for "matched turns" are always
// computed from the same already-cut len(shown), so they can never disagree
// in practice.
