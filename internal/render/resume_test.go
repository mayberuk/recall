package render

import (
	"encoding/json"
	"testing"
)

// Pins the one-line `cd <cwd> && <argv>` shape external callers (fzf,
// Raycast) eval directly.
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

func TestRenderResumeOmitsCdWhenNoCWDWasRecorded(t *testing.T) {
	got := string(RenderResume(Resume{Argv: []string{"codex", "resume", "c0dec001"}}))
	want := "codex resume c0dec001\n"
	if got != want {
		t.Errorf("RenderResume() = %q, want %q", got, want)
	}
}

// Unquoted, a single quote in cwd would close the shell string early and let
// the rest run as commands.
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
