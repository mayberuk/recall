package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
)

func callResume(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := callResumeErr(t, args...)
	return out, err
}

func callResumeErr(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut strings.Builder
	err = resume(args, &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestResumeNamesClaudeResumeForAClaudeCodeSession(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callResume(t, fixtures.SessNeedle[:8])
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	want := "cd '" + c.ScratchPath(fixtures.ScratchNormal) + "' && claude --resume " + fixtures.SessNeedle + "\n"
	if out != want {
		t.Errorf("resume = %q, want %q", out, want)
	}
}

func TestResumeFormatJSONCarriesSessionAgentCWDAndArgv(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callResume(t, fixtures.SessNeedle[:8], "--json")
	if err != nil {
		t.Fatalf("resume --json: %v", err)
	}
	got := decodeJSON(t, out)
	if got["session"] != fixtures.SessNeedle {
		t.Errorf("session = %v, want %s", got["session"], fixtures.SessNeedle)
	}
	if got["agent"] != string(schema.AgentClaudeCode) {
		t.Errorf("agent = %v, want %s", got["agent"], schema.AgentClaudeCode)
	}
	if got["cwd"] != c.ScratchPath(fixtures.ScratchNormal) {
		t.Errorf("cwd = %v, want %s", got["cwd"], c.ScratchPath(fixtures.ScratchNormal))
	}
	argv, _ := got["argv"].([]any)
	if len(argv) != 3 || argv[0] != "claude" || argv[1] != "--resume" || argv[2] != fixtures.SessNeedle {
		t.Errorf("argv = %v, want [claude --resume %s]", argv, fixtures.SessNeedle)
	}
}

func TestResumeNamesCodexResumeForACodexSession(t *testing.T) {
	pinCodexSelection(t)
	harnessAt(t)
	codex := fixtures.MaterializeCodex(t)
	threadID := codex.Manifest.Rows[0].ThreadID

	out, err := callResume(t, "--provider", "codex", threadID)
	if err != nil {
		t.Fatalf("resume --provider codex: %v", err)
	}
	if strings.Contains(out, "claude") {
		t.Errorf("a Codex session named the Claude Code resume command:\n%s", out)
	}
	if !strings.Contains(out, "codex resume "+threadID) {
		t.Errorf("resume did not name `codex resume %s`:\n%s", threadID, out)
	}
}

func turn(session, uuid string, origin schema.Agent, cwd string) schema.Turn {
	return schema.Turn{Session: session, UUID: uuid, TS: "2026-01-01T00:00:00Z", Origin: origin, CWD: cwd}
}

func TestResumeViewPicksTheFirstNonEmptyCWDInTheSession(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", "u1", schema.AgentClaudeCode, ""),
		turn("s1", "u2", schema.AgentClaudeCode, "/repo/first"),
		turn("s1", "u3", schema.AgentClaudeCode, "/repo/second"),
	}
	view, err := resumeView(turns, "s1")
	if err != nil {
		t.Fatalf("resumeView: %v", err)
	}
	if view.CWD != "/repo/first" {
		t.Errorf("cwd = %q, want the first non-empty one (/repo/first), not the last (/repo/second)", view.CWD)
	}
}

func TestResumeViewReportsAnAmbiguousPrefixLikeShowDoes(t *testing.T) {
	turns := []schema.Turn{
		turn("aaa11111", "u1", schema.AgentClaudeCode, "/x"),
		turn("aaa22222", "u2", schema.AgentClaudeCode, "/x"),
	}
	_, err := resumeView(turns, "aaa")
	if err == nil {
		t.Fatal("an ambiguous prefix resolved silently")
	}
	if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("code = %s, want %s", got, fperr.ArgError)
	}
	for _, id := range []string{"aaa11111", "aaa22222"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("error does not list candidate %s: %v", id, err)
		}
	}
}

// AgentGemini is registered in schema but has no resumeArgv entry — the gap
// this test exercises.
func TestResumeViewRefusesAnAgentWithNoResumeCommand(t *testing.T) {
	turns := []schema.Turn{turn("s1", "u1", schema.AgentGemini, "/x")}
	_, err := resumeView(turns, "s1")
	if err == nil {
		t.Fatal("an agent with no resume command was accepted")
	}
	if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("code = %s, want %s", got, fperr.ArgError)
	}
	for _, want := range []string{"claude-code", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name the agents that do have a resume command (%s): %v", want, err)
		}
	}
	if want := "recall resume supports claude-code, codex"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not end in the exact supported list %q — gemini may have leaked into it", err.Error(), want)
	}
}

// stdout stays one eval-safe line so `eval "$(recall resume <id>)"` never
// runs a second command; the note goes to stderr instead.
func TestResumeStatesAMissingDirectoryOnStderrNotStdout(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, errOut, err := callResumeErr(t, fixtures.SessRelocated[:8])
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	gone := c.ScratchPath(fixtures.ScratchGone)
	if !strings.Contains(errOut, gone) {
		t.Errorf("stderr %q does not name the missing directory %s", errOut, gone)
	}
	if !strings.Contains(errOut, "no longer exists") {
		t.Errorf("stderr %q does not say the directory no longer exists", errOut)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("stdout has %d newlines, want exactly 1 (a caller evals stdout, and a second line is a second command)", n)
	}
}

func TestResumeStatesNoRecordedDirectoryOnStderrNotStdout(t *testing.T) {
	pinCodexSelection(t)
	harnessAt(t)
	codex := fixtures.MaterializeCodex(t)
	var threadID string
	for _, row := range codex.Manifest.Rows {
		if row.Quirk == fixtures.CodexQuirkMissingSessionMeta {
			threadID = row.ThreadID
		}
	}
	if threadID == "" {
		t.Fatal("no CodexQuirkMissingSessionMeta row in the generated Codex corpus")
	}

	out, errOut, err := callResumeErr(t, "--provider", "codex", threadID)
	if err != nil {
		t.Fatalf("resume --provider codex: %v", err)
	}
	if !strings.Contains(errOut, "no working directory was recorded") {
		t.Errorf("stderr %q does not say no directory was recorded", errOut)
	}
	if strings.Contains(errOut, "no longer exists") {
		t.Errorf("stderr %q reads like the missing-directory case, not the unrecorded one", errOut)
	}
	if n := strings.Count(out, "\n"); n != 1 {
		t.Errorf("stdout has %d newlines, want exactly 1", n)
	}
	if strings.HasPrefix(out, "cd ") {
		t.Errorf("stdout %q carries a cd prefix despite no cwd being recorded", out)
	}
}

func TestResumeIsSilentOnStderrWhenTheCWDExists(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	_, errOut, err := callResumeErr(t, fixtures.SessNeedle[:8])
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty for a session whose recorded cwd still exists", errOut)
	}
}

// A vanished cwd is a note, not an error — the caller may still want to
// resume from elsewhere.
func TestResumeViewNotesAMissingDirectoryWithoutFailing(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "does-not-exist")
	turns := []schema.Turn{turn("s1", "u1", schema.AgentClaudeCode, gone)}
	view, err := resumeView(turns, "s1")
	if err != nil {
		t.Fatalf("a session with a vanished cwd was refused instead of noted: %v", err)
	}
	if view.CWD != gone {
		t.Errorf("cwd = %q, want %q — the argv line must still name it", view.CWD, gone)
	}
	if len(view.Argv) == 0 {
		t.Error("no argv was emitted despite the missing directory being a note, not an error")
	}
	found := false
	for _, n := range view.Notes {
		if strings.Contains(n, gone) {
			found = true
		}
	}
	if !found {
		t.Errorf("notes %v do not name the missing directory %s", view.Notes, gone)
	}
}

func TestResumeViewNotesAreDistinctBetweenMissingAndUnrecordedCWD(t *testing.T) {
	existing := t.TempDir()
	gone := filepath.Join(existing, "gone")

	present, err := resumeView([]schema.Turn{turn("s1", "u1", schema.AgentClaudeCode, existing)}, "s1")
	if err != nil {
		t.Fatalf("resumeView (existing cwd): %v", err)
	}
	if len(present.Notes) != 0 {
		t.Errorf("a cwd that exists carried a note: %v", present.Notes)
	}

	missing, err := resumeView([]schema.Turn{turn("s1", "u1", schema.AgentClaudeCode, gone)}, "s1")
	if err != nil {
		t.Fatalf("resumeView (missing cwd): %v", err)
	}
	none, err := resumeView([]schema.Turn{turn("s1", "u1", schema.AgentClaudeCode, "")}, "s1")
	if err != nil {
		t.Fatalf("resumeView (no recorded cwd): %v", err)
	}

	if none.CWD != "" {
		t.Fatalf("a session with no recorded cwd carried one: %q", none.CWD)
	}
	if len(missing.Notes) != 1 || len(none.Notes) != 1 {
		t.Fatalf("missing.Notes=%v none.Notes=%v, want exactly one note each", missing.Notes, none.Notes)
	}
	if missing.Notes[0] == none.Notes[0] {
		t.Errorf("a vanished directory and no directory recorded produced the same note: %q", missing.Notes[0])
	}

	if got := string(render.RenderResume(none)); strings.Contains(got, "cd ") {
		t.Errorf("no cwd was recorded but the human line still carries a cd prefix: %q", got)
	}
}
