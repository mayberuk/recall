package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/render"
)

// hasFooterLine reports whether out contains line verbatim as one of its
// footer lines. A substring check alone would let a merged line like
// "(--limit, --budget)" satisfy an assertion meant for the standalone
// "(--budget)" line, since the shorter text is not a substring of the longer
// one here — but pinning to whole lines keeps that true by construction
// rather than by accident of the current wording.
func hasFooterLine(out, line string) bool {
	return slices.Contains(strings.Split(out, "\n"), line)
}

// TestFindMergesTheBudgetLineWithAnIdenticalLimitLine is the regression guard
// for the doubled footer (`recall find recall --all --budget 1` printing
// "showing 1 of 42 sessions" twice, once per flag): a --budget cap that lands
// on the exact cut an explicit --limit already reported must be named on that
// one line, not restated on a line of its own directly under it.
func TestFindMergesTheBudgetLineWithAnIdenticalLimitLine(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	// "this" matches 8 of the corpus's 13 sessions; --limit 3 cuts that to 3,
	// and a --budget too small for any of it to fit forces every fallback
	// attempt down to the same one-of-8 answer --limit would have reported.
	out, _, err := callFind(t, "this", "--all", "--limit", "3", "--budget", "1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if want := "── showing 1 of 8 sessions (--limit, --budget)"; !hasFooterLine(out, want) {
		t.Errorf("footer does not merge --limit and --budget onto one line: want %q, got:\n%s", want, out)
	}
	// The actual regression guard: a change that appends the merged line above
	// while still also emitting the old standalone line would still pass the
	// assertion above without this one.
	if dup := "── showing 1 of 8 sessions (--budget)"; hasFooterLine(out, dup) {
		t.Errorf("footer still carries the standalone --budget duplicate alongside the merged line:\n%s", out)
	}
}

// TestFindBudgetLineStandsAloneWhenNoLimitReportsTheSameCut: a --budget cap
// that no existing limit already describes still gets its own line, exactly
// as before the merge was introduced.
func TestFindBudgetLineStandsAloneWhenNoLimitReportsTheSameCut(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	// NeedleConversation matches exactly one session, well under the default
	// --limit, so --limit never caps anything for --budget to coincide with.
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

// TestFindNamesNoBudgetWithoutBudgetShaping is the negative control: a call
// that never passes --budget must never mention it in the footer, merged or
// standalone.
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

// TestMergeBudgetLimitKeepsSeparateLinesForDifferentNumbers exercises
// mergeBudgetLimit directly rather than through find or turns: within one
// answer, both callers always compute their --limit and --budget entries for
// the same What from the same already-cut count, so the CLI itself can never
// produce two entries that share a What but disagree on Shown or Total. This
// proves the merge stays conservative — a real numeric disagreement, were one
// ever to reach it, still prints as two lines rather than being folded into
// one that would misstate either fact.
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
