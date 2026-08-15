package jsonl

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
)

func header(t *testing.T, line string) Header {
	t.Helper()
	return parse(t, line).Header()
}

func TestHeaderShapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Header
	}{
		{
			"an ordinary record",
			`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-08-01T10:00:00.000Z","cwd":"/w","gitBranch":"main","agentId":"a1","promptSource":"typed","isSidechain":false}`,
			Header{Type: "user", UUID: "u1", SessionID: "s1", Timestamp: "2026-08-01T10:00:00.000Z", CWD: "/w", GitBranch: "main", AgentID: "a1", PromptSource: "typed", HasPromptSource: true},
		},
		{
			"fields after a nested object and array",
			`{"message":{"role":"user","content":[{"type":"text","text":"}{"}]},"nested":[1,{"a":"}"}],"type":"user","uuid":"u2"}`,
			Header{Type: "user", UUID: "u2"},
		},
		{
			"a brace inside a string does not end the value",
			`{"note":"a } and a { and a \" quote","type":"assistant","uuid":"u3"}`,
			Header{Type: "assistant", UUID: "u3"},
		},
		{
			"an escaped value is decoded",
			`{"type":"user","uuid":"u4","cwd":"/w/a\"b","gitBranch":"feat/one"}`,
			Header{Type: "user", UUID: "u4", CWD: `/w/a"b`, GitBranch: "feat/one"},
		},
		{
			"whitespace between tokens",
			`{ "type" : "user" , "uuid" : "u5" , "isSidechain" : true }`,
			Header{Type: "user", UUID: "u5", IsSidechain: true},
		},
		{
			"the session_id spelling",
			`{"type":"user","uuid":"u6","session_id":"s6"}`,
			Header{Type: "user", UUID: "u6", SessionID: "s6"},
		},
		{
			"sessionId wins over session_id, whichever comes first",
			`{"session_id":"s7b","type":"user","uuid":"u7","sessionId":"s7a"}`,
			Header{Type: "user", UUID: "u7", SessionID: "s7a"},
		},
		{
			"an empty sessionId falls back to session_id",
			`{"type":"user","uuid":"u8","sessionId":"","session_id":"s8"}`,
			Header{Type: "user", UUID: "u8", SessionID: "s8"},
		},
		{
			"a repeated key keeps the first value, even an empty one",
			`{"type":"user","uuid":"u9","cwd":"","cwd":"/second"}`,
			Header{Type: "user", UUID: "u9", CWD: ""},
		},
		{
			"a repeated isSidechain keeps the first value, even a false one",
			`{"type":"user","uuid":"u10","isSidechain":false,"isSidechain":true}`,
			Header{Type: "user", UUID: "u10", IsSidechain: false},
		},
		{
			"a repeated promptSource keeps the first value",
			`{"type":"user","uuid":"u11","promptSource":"typed","promptSource":"sdk"}`,
			Header{Type: "user", UUID: "u11", PromptSource: "typed", HasPromptSource: true},
		},
		{
			"a field whose type a release changed reads as the accessors read it",
			`{"type":"user","uuid":"u12","cwd":{"path":"/w"},"gitBranch":7,"timestamp":1.5}`,
			Header{Type: "user", UUID: "u12", CWD: `{"path":"/w"}`, GitBranch: "7", Timestamp: "1.5"},
		},
		{
			"null reads as absent",
			`{"type":"user","uuid":"u15","cwd":null,"agentId":null}`,
			Header{Type: "user", UUID: "u15"},
		},
		{
			"an empty promptSource is present but not typed",
			`{"type":"user","uuid":"u13","promptSource":""}`,
			Header{Type: "user", UUID: "u13", HasPromptSource: true},
		},
		{
			"a relocated record",
			`{"type":"relocated","sessionId":"s14","relocatedCwd":"/gone"}`,
			Header{Type: "relocated", SessionID: "s14", RelocatedCWD: "/gone"},
		},
		{"empty object", `{}`, Header{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := header(t, tc.line); got != tc.want {
				t.Errorf("\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// Header is a faster path to what the accessors already answer, so every field
// has to match. The comparison is the assertion, which is why no case here
// hand-writes an expected value.
func agreesWithAccessors(t *testing.T, where string, rec Record) {
	t.Helper()
	h := rec.Header()
	prompt, hasPrompt := rec.PromptSource()
	for _, f := range []struct {
		field     string
		got, want string
	}{
		{"type", h.Type, rec.Type()},
		{"uuid", h.UUID, rec.UUID()},
		{"sessionId", h.SessionID, rec.SessionID()},
		{"timestamp", h.Timestamp, rec.Timestamp()},
		{"cwd", h.CWD, rec.CWD()},
		{"relocatedCwd", h.RelocatedCWD, rec.RelocatedCWD()},
		{"gitBranch", h.GitBranch, rec.GitBranch()},
		{"agentId", h.AgentID, rec.AgentID()},
		{"promptSource", h.PromptSource, prompt},
	} {
		if f.got != f.want {
			t.Fatalf("%s: %s = %q, the accessor says %q", where, f.field, f.got, f.want)
		}
	}
	if h.HasPromptSource != hasPrompt {
		t.Fatalf("%s: promptSource present=%v, the accessor says %v", where, h.HasPromptSource, hasPrompt)
	}
	if h.IsSidechain != rec.IsSidechain() {
		t.Fatalf("%s: isSidechain=%v, the accessor says %v", where, h.IsSidechain, rec.IsSidechain())
	}
}

// isSidechain leads the author rule, and the corpus encodes it as a JSON boolean
// today. The differential over the real store therefore cannot catch a version
// that writes it any other way, so every encoding gjson accepts is pinned here.
func TestHeaderAgreesWithAccessorsOnBooleanEncodings(t *testing.T) {
	values := []string{
		`true`, `false`, `null`,
		`"true"`, `"false"`, `"True"`, `"TRUE"`, `"t"`, `"f"`, `"yes"`, `""`,
		`1`, `0`, `-1`, `2`, `1.5`, `0.0`, `"1"`, `"0"`,
		`{"nested":true}`, `[true]`,
	}
	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			line := `{"type":"user","uuid":"u1","isSidechain":` + v + `,"cwd":"/w"}`
			agreesWithAccessors(t, line, parse(t, line))
		})
	}
}

func TestHeaderAgreesWithAccessorsOnAwkwardRecords(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","cwd":"","cwd":"/second"}`,
		`{"type":"user","uuid":"u2","sessionId":"a","sessionId":"b"}`,
		`{"type":"user","uuid":"u3","sessionId":"","session_id":"alt"}`,
		`{"session_id":"alt","type":"user","uuid":"u4","sessionId":"main"}`,
		`{"type":"user","uuid":"u5","isSidechain":false,"isSidechain":true}`,
		`{"type":"user","uuid":"u6","promptSource":"typed","promptSource":"sdk"}`,
		`{ "type" : "user" , "uuid" : "u7" , "isSidechain" : true }`,
		`{"type":"user","uuid":"u8","cwd":"/w/a\"b\\c","gitBranch":"feat\/one"}`,
		`{"type":"user","uuid":"u9","cwd":"café"}`,
		`{"message":{"content":[{"text":"\"type\":\"decoy\",\"uuid\":\"nope\""}]},"type":"assistant","uuid":"u10"}`,
		`{"type":"user","uuid":"u11","agentId":null,"promptSource":null}`,
		`{"type":"user","uuid":"u12","timestamp":12345}`,
		`{}`,
	}
	for _, line := range lines {
		t.Run(line, func(t *testing.T) {
			agreesWithAccessors(t, line, parse(t, line))
		})
	}
}

func assertHeaderMatchesAccessors(t *testing.T, root string, minRecords int) {
	t.Helper()
	files, records, skipped := 0, 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		files++
		r, err := Open(path)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				skipped++
				continue
			}
			records++
			agreesWithAccessors(t, path, rec)
		}
		return r.Err()
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	// A comparison that compared nothing would otherwise report success, which is
	// the failure shape this whole test exists to rule out.
	if files == 0 {
		t.Fatalf("no transcripts under %s, so nothing was compared", root)
	}
	if records < minRecords {
		t.Fatalf("compared %d records under %s, want at least %d", records, root, minRecords)
	}
	if skipped != 0 {
		t.Fatalf("%d lines under %s did not parse, so they were never compared", skipped, root)
	}
	t.Logf("compared %d records across %d files under %s", records, files, root)
}

func TestHeaderMatchesAccessorsOnTheFixtures(t *testing.T) {
	c := fixtures.Materialize(t)
	assertHeaderMatchesAccessors(t, c.Root, 40)
}

// The real store is read-only here and is the only place the 24 versions of the
// format actually appear; the synthetic corpus cannot stand in for them.
func TestHeaderMatchesAccessorsOnTheRealCorpus(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory, so the real store cannot be read: %v", err)
	}
	root := filepath.Join(home, ".claude", "projects")
	if _, err := os.Stat(root); err != nil {
		t.Skipf("no session store at %s: %v", root, err)
	}
	assertHeaderMatchesAccessors(t, root, 100000)
}

// Observe and ObserveType are one counting rule reached two ways.
func TestObserveAndObserveTypeAgree(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1"}`,
		`{"type":"quantum-checkpoint","uuid":"u2"}`,
		`{"type":"quantum-checkpoint","uuid":"u3"}`,
		`{"uuid":"u4"}`,
	}
	var viaRecord, viaType Tally
	for _, line := range lines {
		rec := parse(t, line)
		viaRecord.Observe(rec)
		viaType.ObserveType(rec.Header().Type)
	}
	if viaRecord.Lines != viaType.Lines || viaRecord.Untyped != viaType.Untyped {
		t.Errorf("Observe %+v, ObserveType %+v", viaRecord, viaType)
	}
	if viaRecord.Unknown["quantum-checkpoint"] != 2 || viaType.Unknown["quantum-checkpoint"] != 2 {
		t.Errorf("unknown counts: Observe %v, ObserveType %v", viaRecord.Unknown, viaType.Unknown)
	}
	if viaRecord.Untyped != 1 {
		t.Errorf("untyped %d, want 1", viaRecord.Untyped)
	}
}
