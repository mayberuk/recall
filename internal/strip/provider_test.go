package strip_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// var _ archive.Provider = strip.ClaudeCode() is the durable guard: nothing in
// the tree failed when this property broke before a real provider existed, so
// this compile-time assertion is what makes a future drift in either method
// set a build failure instead of a runtime surprise deep in a corpus walk.
// It must live in an external test package: internal/archive's own tests
// already import internal/strip, so internal/strip importing internal/archive
// from any file compiled into package strip (this one is package strip_test)
// is a real import cycle, not just an unwanted coupling.
var _ archive.Provider = strip.ClaudeCode()

func TestClaudeCodeAgent(t *testing.T) {
	if got := strip.ClaudeCode().Agent(); got != schema.AgentClaudeCode {
		t.Errorf("Agent() = %q, want %q", got, schema.AgentClaudeCode)
	}
}

// clearProjectsEnv isolates Root from whatever environment the test binary
// itself runs under, so each case starts from exactly the variables it sets.
func clearProjectsEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_PROJECTS_DIR", "")
}

func TestRootPrefersClaudeProjectsDir(t *testing.T) {
	clearProjectsEnv(t)
	t.Setenv("CLAUDE_PROJECTS_DIR", "/custom/projects")

	got, err := strip.ClaudeCode().Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if want := "/custom/projects"; got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestRootFallsBackToHomeClaudeProjects(t *testing.T) {
	clearProjectsEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := strip.ClaudeCode().Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if want := filepath.Join(home, ".claude", "projects"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

// TestRootFailsWithoutAHomeDirectory mirrors archive.DefaultRoot's own
// failure path: os.UserHomeDir on unix reads only $HOME, so clearing it is
// the deterministic way to make that lookup fail.
func TestRootFailsWithoutAHomeDirectory(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Skip("HOME is already unset in this environment")
	}
	clearProjectsEnv(t)
	t.Setenv("HOME", "")

	if _, err := strip.ClaudeCode().Root(); err == nil {
		t.Error("Root succeeded with no CLAUDE_PROJECTS_DIR or HOME")
	}
}

// TestRootAgreesWithArchiveDefaultRoot pins that the provider's Root and
// archive.DefaultRoot are one rule stated twice: a fix to one that forgets the
// other silently moves the corpus root out from under the archive.
func TestRootAgreesWithArchiveDefaultRoot(t *testing.T) {
	clearProjectsEnv(t)
	t.Setenv("CLAUDE_PROJECTS_DIR", "/custom/projects")

	providerRoot, err := strip.ClaudeCode().Root()
	if err != nil {
		t.Fatalf("provider Root: %v", err)
	}
	archiveRoot, err := archive.DefaultRoot()
	if err != nil {
		t.Fatalf("archive.DefaultRoot: %v", err)
	}
	if providerRoot != archiveRoot {
		t.Errorf("with CLAUDE_PROJECTS_DIR set: provider Root() = %q, archive.DefaultRoot() = %q", providerRoot, archiveRoot)
	}

	clearProjectsEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	providerRoot, err = strip.ClaudeCode().Root()
	if err != nil {
		t.Fatalf("provider Root: %v", err)
	}
	archiveRoot, err = archive.DefaultRoot()
	if err != nil {
		t.Fatalf("archive.DefaultRoot: %v", err)
	}
	if providerRoot != archiveRoot {
		t.Errorf("with CLAUDE_PROJECTS_DIR unset: provider Root() = %q, archive.DefaultRoot() = %q", providerRoot, archiveRoot)
	}
}

func TestClaudeCodeReturnsAFreshProviderEveryCall(t *testing.T) {
	a, b := strip.ClaudeCode(), strip.ClaudeCode()
	if a == b {
		t.Error("ClaudeCode() returned the same provider twice, want a fresh one each call")
	}
}

func TestIsTranscriptExcludesNonJSONLFiles(t *testing.T) {
	p := strip.ClaudeCode()
	cases := map[string]bool{
		"a.jsonl":         true,
		"sub/dir/b.jsonl": true,
		"a.jsonl.bak":     false,
		"README.md":       false,
		"no-extension":    false,
		"":                false,
		"notesjsonl":      false,
		"a.JSONL":         false,
	}
	for rel, want := range cases {
		if got := p.IsTranscript(rel); got != want {
			t.Errorf("IsTranscript(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestNeedsHeadIsFalse(t *testing.T) {
	if strip.ClaudeCode().NeedsHead() {
		t.Error("NeedsHead() = true, want false: Claude Code keys sessions by path, not by content")
	}
}

// TestDecodersFromTheSameProviderShareOneObservation pins the shared-stripper
// design: a Decoder carries no state of its own, so two files decoded through
// two separate Decoder calls must still accumulate into the one Observation
// the provider reports.
func TestDecodersFromTheSameProviderShareOneObservation(t *testing.T) {
	recA := parseRecord(t, `{"type":"user","uuid":"u1","promptSource":"typed","message":{"role":"user","content":"typed by a human"}}`)
	recB := parseRecord(t, `{"type":"assistant","uuid":"u2","message":{"role":"assistant","content":[{"type":"text","text":"a reply"}]}}`)

	p := strip.ClaudeCode()
	p.Decoder("a.jsonl").Turns(recA)
	p.Decoder("b.jsonl").Turns(recB)

	want := strip.New()
	want.Strip(recA)
	want.Strip(recB)

	if got, wantObs := p.Observation(), want.Observation(); !reflect.DeepEqual(got, wantObs) {
		t.Errorf("Observation() = %#v across two Decoder calls, want %#v as if one Stripper saw both records", got, wantObs)
	}
	if lines := p.Observation().Tally.Lines; lines != 2 {
		t.Errorf("Tally.Lines = %d after decoding through two Decoder calls, want 2", lines)
	}
}

func parseRecord(t *testing.T, line string) jsonl.Record {
	t.Helper()
	rec, ok := jsonl.Parse(jsonl.Line{Bytes: []byte(line), Length: len(line)})
	if !ok {
		t.Fatalf("test line did not parse: %s", line)
	}
	return rec
}

// TestObservationMatchesStripperOverTheFixtureCorpus proves Observation is
// wired to what the provider actually decoded, not the zero value: feeding
// the same corpus through the provider and through a bare Stripper must leave
// both with the same, non-zero, observation.
func TestObservationMatchesStripperOverTheFixtureCorpus(t *testing.T) {
	c := fixtures.Materialize(t)

	p := strip.ClaudeCode()
	s := strip.New()
	var rels []string
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(c.Root, path)
		if err != nil {
			return err
		}
		if p.IsTranscript(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	sort.Strings(rels)
	if len(rels) == 0 {
		t.Fatal("fixture corpus holds no transcript files; this test would pass vacuously")
	}

	for _, rel := range rels {
		r, err := jsonl.Open(c.Path(rel))
		if err != nil {
			t.Fatalf("open %s: %v", rel, err)
		}
		dec := p.Decoder(rel)
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				continue
			}
			dec.Turns(rec)
			s.Strip(rec)
		}
		_ = r.Close()
	}

	got, want := p.Observation(), s.Observation()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Observation() = %#v, want %#v to match an equivalently fed Stripper", got, want)
	}
	if got.Tally.Lines == 0 {
		t.Fatal("Observation().Tally.Lines is 0 over a non-empty corpus")
	}
	if got.Typed != c.Manifest.TypedTurnRecords {
		t.Errorf("Observation().Typed = %d, want %d from the corpus manifest", got.Typed, c.Manifest.TypedTurnRecords)
	}
	if got.Typed == 0 {
		t.Fatal("Observation().Typed is 0 over a corpus the manifest says carries typed records")
	}
}
