package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
)

// `recall find --help` used to exit 1 with "flag: help requested". It is the
// one place a caller looks before its first invocation, and the top-level help
// sends every caller to it.
func TestVerbHelpWorksAndListsEveryFlag(t *testing.T) {
	for _, verb := range []string{"find", "show", "when", "turns", "doctor", "guide"} {
		t.Run(verb, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run([]string{verb, "--help"}, &out, &errOut); code != 0 {
				t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
			}
			help := out.String()
			for _, name := range flagNames(verb) {
				if !strings.Contains(help, strings.TrimPrefix(name, "--")) {
					t.Errorf("help does not mention %s:\n%s", name, help)
				}
			}
			if errOut.Len() != 0 {
				t.Errorf("help wrote to stderr: %q", errOut.String())
			}
		})
	}
}

// A wrong flag used to print "flag provided but not defined" and nothing else,
// which tells a caller its move was wrong and gives it no other one.
func TestAWrongFlagPrintsTheValidOnes(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"find", "wallet", "--bogusflag"}, &out, &errOut)

	if want := 2; code != want {
		t.Errorf("exit %d, want %d for a usage error", code, want)
	}
	stderr := errOut.String()
	for _, want := range []string{"bogusflag", "--results", "--since", "--all-terms", "recall find --help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr is missing %q:\n%s", want, stderr)
		}
	}
	if !strings.HasSuffix(stderr, "ERROR_CODE=arg_error\n") {
		t.Errorf("stderr must still end in the parseable code line:\n%s", stderr)
	}
}

// grep's convention, which every caller already knows: nothing matched is 1,
// and the report of what was searched is still on stdout.
func TestAnEmptySearchExitsOneAndStaysSilentOnStderr(t *testing.T) {
	harness(t)
	var out, errOut bytes.Buffer
	// The registry writes a verb's own output to the process streams, so the
	// exit code and the report are checked through different doors.
	code := report(&errOut, find([]string{"zzzznothingcarriesthis", "--all"}, &out, &errOut))

	if want := 1; code != want {
		t.Fatalf("exit %d, want %d", code, want)
	}
	if errOut.Len() != 0 {
		t.Errorf("an empty result wrote to stderr: %q", errOut.String())
	}
	if !strings.Contains(out.String(), "── ") {
		t.Errorf("an empty result dropped the coverage line:\n%s", out.String())
	}
}

func TestAHitExitsZero(t *testing.T) {
	harness(t)
	var out, errOut bytes.Buffer
	if code := report(&errOut, find([]string{fixtures.NeedleConversation, "--all"}, &out, &errOut)); code != 0 {
		t.Fatalf("exit %d on a query that hits, want 0\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
}

// The top-level help sends callers to `recall guide`, and the three things that
// decided the observed failure have to be reachable from one of the two.
func TestTheHelpAndGuideStateWhatDecidedTheObservedFailure(t *testing.T) {
	var top bytes.Buffer
	printUsage(&top)

	var guideOut bytes.Buffer
	if err := guide(nil, &guideOut); err != nil {
		t.Fatalf("guide: %v", err)
	}
	both := top.String() + guideOut.String()

	for _, want := range []string{
		"--results",         // tool output is not searched by default
		"--all-terms",       // terms are ANDed, and what to do about it
		"prefix",            // a short session id resolves
		"--include-self",    // the caller's own session is excluded
		"1 nothing matched", // the exit code, in the top-level help
		"matched nothing",   // and in the guide, where it is spelled out
	} {
		if !strings.Contains(both, want) {
			t.Errorf("neither the help nor the guide mentions %q", want)
		}
	}
	if !strings.Contains(top.String(), "recall guide") {
		t.Errorf("the top-level help does not point at the guide:\n%s", top.String())
	}
}

func TestGuideFitsInOneRead(t *testing.T) {
	var out bytes.Buffer
	if err := guide(nil, &out); err != nil {
		t.Fatalf("guide: %v", err)
	}
	if lines := strings.Count(out.String(), "\n"); lines > 90 {
		t.Errorf("guide is %d lines; it is meant to be read once and kept in context", lines)
	}
	if out.Len() > 4096 {
		t.Errorf("guide is %d bytes, about %d tokens", out.Len(), out.Len()/4)
	}
}

// find, then choosing, then show is three round trips for one question. This
// verb answers it in one, and stamps each passage so it can be cited and
// reached again.
func TestTurnsReturnsCitablePassagesInOneCall(t *testing.T) {
	harness(t)
	var out, errOut bytes.Buffer
	if code := report(&errOut, turns([]string{fixtures.NeedleConversation, "--all"}, &out)); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, fixtures.NeedleConversation) {
		t.Errorf("turns returned no passage carrying the query:\n%s", body)
	}
	cite := citation(t, body)
	session, uuid, ok := strings.Cut(cite, ":")
	if !ok {
		t.Fatalf("citation %q is not <session>:<uuid>", cite)
	}

	var shown, showErr bytes.Buffer
	if code := report(&showErr, show([]string{session, "--turn", uuid}, &shown)); code != 0 {
		t.Fatalf("the citation turns printed does not resolve: exit %d\n%s", code, showErr.String())
	}
	if !strings.Contains(shown.String(), fixtures.NeedleConversation) {
		t.Errorf("show --turn on the printed citation returned a different turn:\n%s", shown.String())
	}
}

// citation is the first `<session>:<uuid>` stamp in turns output.
func citation(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		field, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
		if s, u, ok := strings.Cut(field, ":"); ok && len(s) >= 8 && len(u) >= 8 {
			return field
		}
	}
	t.Fatalf("no <session>:<uuid> stamp in:\n%s", body)
	return ""
}

// The same session reported 112 turns without --results and 245 with it, which
// reads as the tool disagreeing with itself rather than as two questions.
func TestShowSaysWhichTiersItsTurnCountIsOver(t *testing.T) {
	harness(t)
	var plain, withResults bytes.Buffer
	if err := show([]string{fixtures.SessHugeResult}, &plain); err != nil {
		t.Fatalf("show: %v", err)
	}
	if err := show([]string{fixtures.SessHugeResult, "--results"}, &withResults); err != nil {
		t.Fatalf("show --results: %v", err)
	}
	for _, out := range []string{plain.String(), withResults.String()} {
		if !strings.Contains(out, "turns (conversation") {
			t.Errorf("the turn count does not name the tiers it counted:\n%s", out)
		}
	}
	if !strings.Contains(withResults.String(), "result)") {
		t.Errorf("--results did not widen the stated tiers:\n%s", withResults.String())
	}
}

// `turns` prints a follow-up command for a truncated passage, and that command
// has to be one `show` accepts. It advertised --chars before show had it.
func TestEveryFlagShowsOwnOutputAdvertisesExists(t *testing.T) {
	defined := map[string]bool{}
	for _, name := range flagNames("show") {
		defined[name] = true
	}
	for _, name := range []string{"--chars", "--turn", "--around", "--results", "--full", "--budget"} {
		if !defined[name] {
			t.Errorf("show does not accept %s, which recall's own output tells callers to use", name)
		}
	}
}

// A passage is what a caller quotes onward, so the marks a snippet uses would
// corrupt a table cell, an identifier or a URL inside it.
func TestAQuotedPassageCarriesNoHighlightMarks(t *testing.T) {
	harness(t)
	var out, errOut bytes.Buffer
	if code := report(&errOut, turns([]string{fixtures.NeedleConversation, "--all"}, &out)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), render.MarkOpen) || strings.Contains(out.String(), render.MarkClose) {
		t.Errorf("a passage carries highlight marks, so quoting it verbatim corrupts what it quotes:\n%s", out.String())
	}
}

// A cut that does not say so is the failure the byte cap exists to prevent.
func TestShowDeclaresATurnItCut(t *testing.T) {
	harness(t)
	var out bytes.Buffer
	if err := show([]string{fixtures.SessHugeResult, "--results", "--chars", "80"}, &out); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out.String(), "--chars 0 for all of it") {
		t.Errorf("--chars cut a turn without saying so:\n%s", out.String())
	}
}

func repoTurn(session, repo, text string) schema.Turn {
	return schema.Turn{
		Session: session, UUID: session + "-1", TS: "2026-08-01T10:00:00Z",
		Tier: schema.TierConversation, Author: schema.AuthorAssistant, Repo: repo, Text: text,
	}
}

// The wider probe fires only on a bare zero, and a relaxed query no longer
// returns zero — it returns weak local partial matches, which look answered.
// Reporting nothing found when the thing is present in another checkout is the
// failure this whole tool exists to invert.
func TestARelaxedRepoScopedAnswerSaysWhenTheMachineCarriesMore(t *testing.T) {
	c := &corpus{
		tiers: []schema.Tier{schema.TierConversation},
		turns: []schema.Turn{
			repoTurn("a", "example/repo-a", "the wallet migration thread"),
			repoTurn("b", "example/repo-b", "the wallet and zebra decision, together"),
		},
	}
	f := newSearchFlags()
	f.Repo = "example/repo-a"
	if err := f.buildFilter(now); err != nil {
		t.Fatalf("buildFilter: %v", err)
	}

	s := c.search("wallet zebra", f, rank.Concentration)
	if len(s.ranked.Sessions) == 0 {
		t.Fatal("the repo-scoped search returned nothing, so this is the old bare-zero path, not the one under test")
	}
	if !s.scan.Match.Relaxed() {
		t.Fatal("the repo-scoped search was not relaxed, so there is nothing to warn about")
	}

	notes := s.notes
	if len(notes) != 1 {
		t.Fatalf("notes %v, want one saying the machine carries more of the query", notes)
	}
	for _, want := range []string{"2 of the 2", "against 1 here", "--all"} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("the note is missing %q: %s", want, notes[0])
		}
	}
}

func TestNoElsewhereNoteWhenThisRepoAnswersAsWellAsAnyOther(t *testing.T) {
	c := &corpus{
		tiers: []schema.Tier{schema.TierConversation},
		turns: []schema.Turn{
			repoTurn("a", "example/repo-a", "the wallet migration thread"),
			repoTurn("b", "example/repo-b", "the wallet thread again"),
		},
	}
	f := newSearchFlags()
	f.Repo = "example/repo-a"
	if err := f.buildFilter(now); err != nil {
		t.Fatalf("buildFilter: %v", err)
	}
	s := c.search("wallet zebra", f, rank.Concentration)
	if notes := s.notes; len(notes) != 0 {
		t.Errorf("notes %v, want none — no other repo carries more of the query", notes)
	}
}

// --brief prints no hit lines, so a cap on them is noise; a cap on sessions is
// the narrowing the footer exists to declare.
func TestBriefStillDeclaresTheSessionsItCut(t *testing.T) {
	harness(t)
	var out, errOut bytes.Buffer
	if code := report(&errOut, find([]string{"the", "--all", "--brief", "--limit", "1"}, &out, &errOut)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, "sessions (--limit)") {
		t.Errorf("--brief cut the session list without declaring it:\n%s", body)
	}
	if strings.Contains(body, "(--hits)") {
		t.Errorf("--brief declared a cap on hit lines it does not print:\n%s", body)
	}
}
