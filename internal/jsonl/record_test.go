package jsonl

import (
	"strings"
	"testing"
)

func parse(t *testing.T, raw string) Record {
	t.Helper()
	rec, ok := Parse(Line{Bytes: []byte(raw), Length: len(raw)})
	if !ok {
		t.Fatalf("did not parse: %s", raw)
	}
	return rec
}

func TestParseRejectsNonObjects(t *testing.T) {
	for _, raw := range []string{
		``,
		`   `,
		`{`,
		`[1,2,3]`,
		`"a string"`,
		`{"a":1`,
		`{"type":"user","message":{"role":"user"`,
		`{"type":"user","message":{"role":"user","content":"half a to`,
	} {
		if _, ok := Parse(Line{Bytes: []byte(raw), Length: len(raw)}); ok {
			t.Errorf("accepted %q; a half-written final line must not be read as a record", raw)
		}
		if _, ok := ParseStrict(Line{Bytes: []byte(raw), Length: len(raw)}); ok {
			t.Errorf("ParseStrict accepted %q", raw)
		}
	}
}

// Parse trades interior validation for O(1): it catches every truncation, which
// is the only way a transcript line goes bad, and leaves the rest to doctor.
func TestParseStrictCatchesInteriorCorruption(t *testing.T) {
	const raw = `{"type":"user","uuid":"u1","message":{"role":}}`
	line := Line{Bytes: []byte(raw), Length: len(raw)}

	if _, ok := Parse(line); !ok {
		t.Error("Parse rejected a closed object; it only screens for truncation")
	}
	if _, ok := ParseStrict(line); ok {
		t.Error("ParseStrict accepted interior corruption")
	}
}

func TestParseStrictAcceptsWholeRecords(t *testing.T) {
	const raw = `{"type":"user","uuid":"u1","message":{"role":"user","content":"hi"}}`
	rec, ok := ParseStrict(Line{Bytes: []byte(raw), Length: len(raw)})
	if !ok {
		t.Fatal("ParseStrict rejected a valid record")
	}
	if rec.UUID() != "u1" {
		t.Errorf("UUID = %q, want u1", rec.UUID())
	}
}

func TestAbsentFieldsAreNotErrors(t *testing.T) {
	rec := parse(t, `{"type":"mode","sessionId":"s1","mode":"default"}`)

	if got := rec.CWD(); got != "" {
		t.Errorf("CWD on a record with no cwd = %q, want empty", got)
	}
	if rec.Get("cwd").Exists() {
		t.Error("an absent path must report Exists() == false")
	}
	if rec.IsSidechain() {
		t.Error("absent isSidechain must read as false: 16 of 20 record types have no author")
	}
	if _, ok := rec.PromptSource(); ok {
		t.Error("absent promptSource must report present == false")
	}
	if got := rec.AgentID(); got != "" {
		t.Errorf("AgentID = %q, want empty", got)
	}
}

func TestPresenceIsDistinctFromEmptyString(t *testing.T) {
	present := parse(t, `{"type":"user","promptSource":""}`)
	if v, ok := present.PromptSource(); !ok || v != "" {
		t.Errorf("empty-but-present promptSource = (%q,%v), want (\"\",true)", v, ok)
	}
	absent := parse(t, `{"type":"user"}`)
	if v, ok := absent.PromptSource(); ok || v != "" {
		t.Errorf("absent promptSource = (%q,%v), want (\"\",false)", v, ok)
	}
}

func TestSessionIDFallsBackToSnakeCase(t *testing.T) {
	tests := []struct {
		name, raw, want string
	}{
		{"camel only", `{"sessionId":"camel"}`, "camel"},
		{"snake only", `{"session_id":"snake"}`, "snake"},
		{"both agree", `{"sessionId":"camel","session_id":"camel"}`, "camel"},
		{"camel wins", `{"sessionId":"camel","session_id":"snake"}`, "camel"},
		{"empty camel falls back", `{"sessionId":"","session_id":"snake"}`, "snake"},
		{"neither", `{"type":"mode"}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parse(t, tc.raw).SessionID(); got != tc.want {
				t.Errorf("SessionID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnvelopeAccessors(t *testing.T) {
	raw := `{"type":"assistant","uuid":"u-1","timestamp":"2026-08-01T10:00:00.000Z",` +
		`"cwd":"/tmp/repo","gitBranch":"main","version":"2.1.231","isSidechain":true,` +
		`"agentId":"a1","sessionId":"s-1","message":{"role":"assistant","content":[` +
		`{"type":"thinking","thinking":"reasoning text","signature":"AAAA"},` +
		`{"type":"text","text":"prose"}]}}`
	rec := parse(t, raw)

	for _, c := range []struct{ name, got, want string }{
		{"Type", rec.Type(), "assistant"},
		{"UUID", rec.UUID(), "u-1"},
		{"Timestamp", rec.Timestamp(), "2026-08-01T10:00:00.000Z"},
		{"CWD", rec.CWD(), "/tmp/repo"},
		{"GitBranch", rec.GitBranch(), "main"},
		{"Version", rec.Version(), "2.1.231"},
		{"AgentID", rec.AgentID(), "a1"},
		{"SessionID", rec.SessionID(), "s-1"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if !rec.IsSidechain() {
		t.Error("IsSidechain = false, want true")
	}

	blocks := rec.Message().Get("content").Array()
	if len(blocks) != 2 {
		t.Fatalf("got %d content blocks, want 2", len(blocks))
	}
	if got := blocks[0].Get("thinking").String(); got != "reasoning text" {
		t.Errorf("thinking text = %q, want %q", got, "reasoning text")
	}
	if got := blocks[1].Get("text").String(); got != "prose" {
		t.Errorf("assistant text = %q, want %q", got, "prose")
	}
}

func TestRelocatedCWDIsSeparateFromCWD(t *testing.T) {
	rec := parse(t, `{"type":"relocated","sessionId":"s","relocatedCwd":"/gone/checkout"}`)
	if got := rec.CWD(); got != "" {
		t.Errorf("CWD = %q, want empty: a relocated record carries no cwd", got)
	}
	if got := rec.RelocatedCWD(); got != "/gone/checkout" {
		t.Errorf("RelocatedCWD = %q, want /gone/checkout", got)
	}
}

func TestValuesOutliveTheReaderBuffer(t *testing.T) {
	body := `{"type":"user","message":{"role":"user","content":"first"}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 300<<10) + `"}}` + "\n"
	r := NewReader("mem", strings.NewReader(body), 0)

	if !r.Next() {
		t.Fatal("no first line")
	}
	rec, ok := r.Record()
	if !ok {
		t.Fatal("first line did not parse")
	}
	held := rec.Get("message.content").String()

	if !r.Next() {
		t.Fatal("no second line")
	}
	if held != "first" {
		t.Errorf("extracted value became %q after the buffer was reused, want %q", held, "first")
	}
}

func TestKnownTypeCoversTheMeasuredCatalog(t *testing.T) {
	for _, typ := range []string{
		"assistant", "user", "attachment", "system", "last-prompt", "mode",
		"permission-mode", "ai-title", "custom-title", "queue-operation", "pr-link",
		"file-history-snapshot", "file-history-delta", "agent-name", "bridge-session",
		"worktree-state", "relocated", "frame-link", "started", "result",
	} {
		if !KnownType(typ) {
			t.Errorf("%q is one of the twenty measured record types but reads as unknown", typ)
		}
	}
	for _, typ := range []string{"", "quantum-checkpoint", "Assistant", "user "} {
		if KnownType(typ) {
			t.Errorf("%q must not read as known", typ)
		}
	}
}

// An unrecognised record type must be countable. Ignoring it silently is how a
// false negative enters through the back door across 24 versions.
func TestTallyCountsUnknownTypes(t *testing.T) {
	var tally Tally
	lines := []string{
		`{"type":"user","uuid":"1"}`,
		`{"type":"quantum-checkpoint","sessionId":"s"}`,
		`{"type":"quantum-checkpoint","sessionId":"s"}`,
		`{"type":"holo-summary","sessionId":"s"}`,
		`{"sessionId":"s"}`,
		`{"type":"assistant","uuid":"2"}`,
		`{"truncated":`,
	}
	for _, raw := range lines {
		rec, ok := Parse(Line{Bytes: []byte(raw), Length: len(raw)})
		if !ok {
			tally.ObserveMalformed()
			continue
		}
		tally.Observe(rec)
	}

	if tally.Lines != len(lines) {
		t.Errorf("Lines = %d, want %d", tally.Lines, len(lines))
	}
	if tally.Malformed != 1 {
		t.Errorf("Malformed = %d, want 1", tally.Malformed)
	}
	if tally.Untyped != 1 {
		t.Errorf("Untyped = %d, want 1", tally.Untyped)
	}
	if tally.UnknownTotal() != 3 {
		t.Errorf("UnknownTotal = %d, want 3", tally.UnknownTotal())
	}
	want := []TypeCount{{"quantum-checkpoint", 2}, {"holo-summary", 1}}
	got := tally.UnknownCounts()
	if len(got) != len(want) {
		t.Fatalf("UnknownCounts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("UnknownCounts[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTallyMerge(t *testing.T) {
	a := Tally{Lines: 2, Malformed: 1, Untyped: 1, Unknown: map[string]int{"x": 1}}
	b := Tally{Lines: 3, Malformed: 0, Untyped: 2, Unknown: map[string]int{"x": 2, "y": 5}}
	a.Merge(b)

	if a.Lines != 5 || a.Malformed != 1 || a.Untyped != 3 {
		t.Errorf("merged totals = %+v", a)
	}
	if a.Unknown["x"] != 3 || a.Unknown["y"] != 5 {
		t.Errorf("merged unknown map = %v, want x=3 y=5", a.Unknown)
	}
	if a.UnknownTotal() != 8 {
		t.Errorf("UnknownTotal = %d, want 8", a.UnknownTotal())
	}
	if b.Unknown["x"] != 2 {
		t.Error("Merge mutated its argument")
	}
}

// TestValueAccessorsReadEveryJSONShapeItCanHold exercises the Value methods
// no other test reaches directly: a number, an absent number, an array, an
// object, and the raw text of a nested value.
func TestValueAccessorsReadEveryJSONShapeItCanHold(t *testing.T) {
	rec := parse(t, `{"type":"user","retries":3,"tags":["a","b"],"message":{"role":"user"}}`)

	if got := rec.Get("retries").Int(); got != 3 {
		t.Errorf("Int() = %d, want 3", got)
	}
	if got := rec.Get("missing").Int(); got != 0 {
		t.Errorf("Int() on an absent field = %d, want 0", got)
	}
	if !rec.Get("tags").IsArray() {
		t.Error("tags is a JSON array but IsArray() said no")
	}
	if rec.Get("message").IsArray() {
		t.Error("message is a JSON object, not an array")
	}
	if !rec.Get("message").IsObject() {
		t.Error("message is a JSON object but IsObject() said no")
	}
	if rec.Get("tags").IsObject() {
		t.Error("tags is a JSON array, not an object")
	}
	if got := rec.Get("message").Raw(); got != `{"role":"user"}` {
		t.Errorf("Raw() = %q, want the value's original JSON text", got)
	}
}

// TestValueStrReportsPresenceSeparatelyFromEmptyString is Value.Str's own
// contract, the same distinction Record.PromptSource relies on it for.
func TestValueStrReportsPresenceSeparatelyFromEmptyString(t *testing.T) {
	rec := parse(t, `{"type":"user","message":{"role":"user","promptSource":""}}`)
	if v, ok := rec.Message().Str("promptSource"); !ok || v != "" {
		t.Errorf("Str() on an empty-but-present field = (%q,%v), want (\"\",true)", v, ok)
	}
	if v, ok := rec.Message().Str("missing"); ok || v != "" {
		t.Errorf("Str() on an absent field = (%q,%v), want (\"\",false)", v, ok)
	}
}

// TestRecordRawIsTheOriginalBytes is what doctor's checksum machinery reads:
// the record's exact bytes as they sat in the transcript, not a re-encoding.
func TestRecordRawIsTheOriginalBytes(t *testing.T) {
	const raw = `{"type":"user","uuid":"u1"}`
	rec := parse(t, raw)
	if got := string(rec.Raw()); got != raw {
		t.Errorf("Raw() = %q, want %q", got, raw)
	}
}

func TestMergeIntoZeroTally(t *testing.T) {
	var dst Tally
	dst.Merge(Tally{Lines: 1, Unknown: map[string]int{"z": 4}})
	if dst.Unknown["z"] != 4 {
		t.Errorf("merging into a zero tally lost the counts: %v", dst.Unknown)
	}
}
