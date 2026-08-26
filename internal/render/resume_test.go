package render

import (
	"encoding/json"
	"testing"
)

// TestRenderResumeEmitsExactlyOneCdAndArgvLine pins the contract two separate
// callers (the fzf ctrl-r binding, the Raycast extension) both evaluate:
// `cd <cwd> && <argv>`, one line, so `eval "$(recall resume <id>)"` composes
// from a directory that is never the session's own.
func TestRenderResumeEmitsExactlyOneCdAndArgvLine(t *testing.T) {
	got := string(RenderResume(Resume{
		CWD:  "/home/dev/acme",
		Argv: []string{"claude", "--resume", "5fd86b00"},
	}))
	want := "cd '/home/dev/acme' && claude --resume 5fd86b00\n"
	if got != want {
		t.Errorf("RenderResume() = %q, want %q", got, want)
	}
}

// TestRenderResumeOmitsCdWhenNoCWDWasRecorded is the negative control for the
// case above: a session with nothing recorded must not print a `cd` to
// nowhere, and must not print a leading space where the prefix would have
// gone either.
func TestRenderResumeOmitsCdWhenNoCWDWasRecorded(t *testing.T) {
	got := string(RenderResume(Resume{Argv: []string{"codex", "resume", "c0dec001"}}))
	want := "codex resume c0dec001\n"
	if got != want {
		t.Errorf("RenderResume() = %q, want %q", got, want)
	}
}

// TestRenderResumeSingleQuotesAnEmbeddedQuoteInTheCWD is the reason raw
// interpolation is disallowed: a cwd is an arbitrary filesystem path, and one
// carrying a single quote would otherwise close the shell string early and
// let the rest of the path run as commands.
func TestRenderResumeSingleQuotesAnEmbeddedQuoteInTheCWD(t *testing.T) {
	got := string(RenderResume(Resume{
		CWD:  "/home/dev/o'brien",
		Argv: []string{"claude", "--resume", "abc"},
	}))
	want := "cd '/home/dev/o'\\''brien' && claude --resume abc\n"
	if got != want {
		t.Errorf("RenderResume() = %q, want %q (POSIX single-quote escaping)", got, want)
	}
}

// TestResumeJSONLCarriesTheSameFieldsAsJSON is the machine-format half of the
// contract: --format jsonl must not drop a field --json carries, because a
// caller picks the format it streams rather than the one that answers in
// full.
func TestResumeJSONLCarriesTheSameFieldsAsJSON(t *testing.T) {
	r := Resume{
		Session: "5fd86b00-0000-4000-8000-000000000000",
		Agent:   "codex",
		CWD:     "/workspace/thing",
		Argv:    []string{"codex", "resume", "5fd86b00-0000-4000-8000-000000000000"},
		Notes:   []string{"/workspace/thing no longer exists"},
	}
	line, err := r.JSONL()
	if err != nil {
		t.Fatalf("JSONL: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("JSONL output does not parse: %v\n%s", err, line)
	}
	for _, field := range []string{"session", "agent", "cwd", "argv", "notes"} {
		if _, ok := got[field]; !ok {
			t.Errorf("JSONL is missing %q: %s", field, line)
		}
	}
	if got["session"] != r.Session {
		t.Errorf("session = %v, want %v", got["session"], r.Session)
	}
}

// TestResumeJSONOmitsNotesWhenThereAreNone keeps an ordinary resume (a
// recorded cwd that still exists) from carrying an empty notes array a
// caller would otherwise have to check the length of for no reason.
func TestResumeJSONOmitsNotesWhenThereAreNone(t *testing.T) {
	b, err := JSON(Resume{Session: "s", Agent: "claude-code", CWD: "/x", Argv: []string{"claude", "--resume", "s"}})
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("JSON output does not parse: %v\n%s", err, b)
	}
	if _, ok := got["notes"]; ok {
		t.Errorf("notes present with nothing to say: %s", b)
	}
}
