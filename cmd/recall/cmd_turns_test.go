package main

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
)

func TestTurnsMergesTheBudgetLineWithAnIdenticalLimitLine(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callTurns(t, "this", "--all", "--limit", "3", "--budget", "1")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if want := "── showing 1 of 8 matched turns (--limit, --budget)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not merge --limit and --budget onto one line: want %q, got:\n%s", want, out)
	}
	// Guards against a fix that appends the merged line while still also
	// emitting the old standalone --budget line; the assertion above alone
	// would still pass that regression.
	if dup := "── showing 1 of 8 matched turns (--budget)"; hasFooterLine(out, dup) {
		t.Errorf("footer still carries the standalone --budget duplicate alongside the merged line:\n%s", out)
	}
}

func TestTurnsBudgetLineStandsAloneWhenNoLimitReportsTheSameCut(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

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
