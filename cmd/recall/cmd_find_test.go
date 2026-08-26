package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/render"
)

// hasFooterLine pins to a whole line, so a merged footer cannot satisfy an
// assertion written for the standalone one.
func hasFooterLine(out, line string) bool {
	return slices.Contains(strings.Split(out, "\n"), line)
}

func TestFindMergesTheBudgetLineWithAnIdenticalLimitLine(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, "this", "--all", "--limit", "3", "--budget", "1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if want := "── showing 1 of 8 sessions (--limit, --budget)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not merge --limit and --budget onto one line: want %q, got:\n%s", want, out)
	}
	// Guards against a fix that leaves the old standalone --budget line in place too.
	if dup := "── showing 1 of 8 sessions (--budget)"; hasFooterLine(out, dup) {
		t.Errorf("footer still carries the standalone --budget duplicate alongside the merged line:\n%s", out)
	}
}

func TestFindBudgetLineStandsAloneWhenNoLimitReportsTheSameCut(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--budget", "1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if want := "── showing 1 of 1 sessions (--budget)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not name the standalone --budget cap: want %q, got:\n%s", want, out)
	}
	if hasFooterLine(out, "── showing 1 of 1 sessions (--limit)") {
		t.Errorf("footer names a --limit cap that never fired:\n%s", out)
	}
}

func TestFindNamesNoBudgetWithoutBudgetShaping(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, "this", "--all", "--limit", "3")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if want := "── showing 3 of 8 sessions (--limit)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not name the --limit cap: want %q, got:\n%s", want, out)
	}
	if strings.Contains(out, "--budget") {
		t.Errorf("a call with no --budget flag named --budget in the footer:\n%s", out)
	}
}

// Exercises mergeBudgetLimit directly on an input find/turns can't construct.
func TestMergeBudgetLimitKeepsSeparateLinesForDifferentNumbers(t *testing.T) {
	limits := []render.Limit{{Flag: "--limit", What: "sessions", Shown: 3, Total: 8}}
	limits = mergeBudgetLimit(limits, "sessions", 5, 8)

	got := render.Coverage{Limits: limits}.Lines()
	for _, want := range []string{
		"── showing 3 of 8 sessions (--limit)",
		"── showing 5 of 8 sessions (--budget)",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("expected a separate line %q, got:\n%s", want, strings.Join(got, "\n"))
		}
	}
	if slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, "--limit, --budget") }) {
		t.Errorf("different-numbers case was merged onto one line:\n%s", strings.Join(got, "\n"))
	}
}
