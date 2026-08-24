package strip_test

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// The compile-time guard for the second provider, beside ClaudeCode's, and for
// the same reason: a drift in either method set has to be a build failure
// rather than a runtime surprise deep in a corpus walk.
var _ archive.Provider = strip.Codex()

// codexRow is the manifest row for one quirk. Every expected turn count in
// this file comes from a row, never from what the decoder produced.
func codexRow(t *testing.T, c fixtures.CodexCorpus, quirk fixtures.CodexQuirk) fixtures.CodexRow {
	t.Helper()
	for _, row := range c.Manifest.Rows {
		if row.Quirk == quirk {
			return row
		}
	}
	t.Fatalf("the codex manifest holds no %q row", quirk)
	return fixtures.CodexRow{}
}

// codexRel names a manifest row the way the archive's walk does. The manifest
// is relative to CODEX_HOME; the provider's Root is the sessions directory
// under it.
func codexRel(t *testing.T, c fixtures.CodexCorpus, row fixtures.CodexRow) string {
	t.Helper()
	root, err := strip.Codex().Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	rel, err := filepath.Rel(root, c.Path(row.File))
	if err != nil {
		t.Fatalf("relative path of %s under %s: %v", row.File, root, err)
	}
	return filepath.ToSlash(rel)
}

// codexLines reads a rollout into copied lines, so a test can feed the same
// records to more than one decoder, in an order of its own, and read fields
// back out of them afterwards.
func codexLines(t *testing.T, path string) []jsonl.Line {
	t.Helper()
	r, err := jsonl.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = r.Close() }()

	var out []jsonl.Line
	for r.Next() {
		l := r.Line()
		out = append(out, jsonl.Line{Offset: l.Offset, Length: l.Length, Bytes: append([]byte(nil), l.Bytes...)})
	}
	if err := r.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no lines", path)
	}
	return out
}

func codexRecord(t *testing.T, line jsonl.Line) jsonl.Record {
	t.Helper()
	rec, ok := jsonl.Parse(line)
	if !ok {
		t.Fatalf("rollout line at offset %d did not parse: %s", line.Offset, line.Bytes)
	}
	return rec
}

// decodeCodex feeds lines to one decoder and collects the turns. It also holds
// the archive's own precondition: a record's turns are numbered from zero and
// deduped whole, and this decoder promises at most one per record.
func decodeCodex(t *testing.T, dec archive.Decoder, lines []jsonl.Line) []schema.Turn {
	t.Helper()
	var out []schema.Turn
	for _, line := range lines {
		turns, ok := dec.Turns(codexRecord(t, line))
		if !ok {
			if len(turns) != 0 {
				t.Errorf("record at offset %d reported no turns but returned %d", line.Offset, len(turns))
			}
			continue
		}
		if len(turns) != 1 {
			t.Errorf("record at offset %d yielded %d turns, want exactly one", line.Offset, len(turns))
		}
		out = append(out, turns...)
	}
	return out
}

func decodeCodexRow(t *testing.T, p *strip.CodexProvider, c fixtures.CodexCorpus, row fixtures.CodexRow) []schema.Turn {
	t.Helper()
	return decodeCodex(t, p.Decoder(codexRel(t, c, row)), codexLines(t, c.Path(row.File)))
}

// findCodexLine returns the first line the predicate accepts, failing rather
// than returning a zero value: a fixture that stopped carrying the record a
// test is about would otherwise make that test pass vacuously.
func findCodexLine(t *testing.T, lines []jsonl.Line, what string, pred func(jsonl.Record) bool) jsonl.Line {
	t.Helper()
	for _, line := range lines {
		if pred(codexRecord(t, line)) {
			return line
		}
	}
	t.Fatalf("the rollout holds no %s record", what)
	return jsonl.Line{}
}

func codexTypeIs(typ, payloadType string) func(jsonl.Record) bool {
	return func(rec jsonl.Record) bool {
		t, payload, ok := rec.CodexEnvelope()
		if !ok || t != typ {
			return false
		}
		return payloadType == "" || payload.Get("type").String() == payloadType
	}
}

func codexMessageRole(role string) func(jsonl.Record) bool {
	return func(rec jsonl.Record) bool {
		return codexTypeIs("response_item", "message")(rec) && rec.Get("payload.role").String() == role
	}
}

// codexWantUUID restates the documented key rule rather than reading one back
// out of the decoder: the record's position in the file, then sixteen hex
// digits of FNV-1a over its raw bytes.
func codexWantUUID(line jsonl.Line) string {
	h := fnv.New64a()
	_, _ = h.Write(line.Bytes)
	return fmt.Sprintf("%d-%016x", line.Offset, h.Sum64())
}

// codexUUIDPosition is the position half of a synthesised uuid. It is what
// says which record a turn was made from when two records carry the same text.
func codexUUIDPosition(t *testing.T, uuid string) int64 {
	t.Helper()
	pos, _, ok := strings.Cut(uuid, "-")
	if !ok {
		t.Fatalf("uuid %q is not <position>-<hash>", uuid)
	}
	n, err := strconv.ParseInt(pos, 10, 64)
	if err != nil {
		t.Fatalf("uuid %q carries a non-numeric position: %v", uuid, err)
	}
	return n
}

func codexLineOf(offset int64, body string) jsonl.Line {
	return jsonl.Line{Offset: offset, Length: len(body), Bytes: []byte(body)}
}

func codexMessageJSON(ts, role, text string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":`+
		`{"type":"message","role":%q,"content":[{"type":"input_text","text":%q}]}}`, ts, role, text)
}

func codexTextsOf(turns []schema.Turn) []string {
	out := make([]string, len(turns))
	for i, turn := range turns {
		out[i] = turn.Text
	}
	return out
}

func countCodexText(turns []schema.Turn, text string) int {
	n := 0
	for _, turn := range turns {
		if turn.Text == text {
			n++
		}
	}
	return n
}

func clearCodexEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CODEX_HOME", "")
}

func TestCodexAgent(t *testing.T) {
	if got := strip.Codex().Agent(); got != schema.AgentCodex {
		t.Errorf("Agent() = %q, want %q", got, schema.AgentCodex)
	}
}

func TestCodexReturnsAFreshProviderEveryCall(t *testing.T) {
	if a, b := strip.Codex(), strip.Codex(); a == b {
		t.Error("Codex() returned the same provider twice, want a fresh one each call")
	}
}

func TestCodexNeedsHeadIsTrue(t *testing.T) {
	if !strip.Codex().NeedsHead() {
		t.Error("NeedsHead() = false, want true: a rollout's thread id, cwd and branch live in session_meta, not in its path")
	}
}

func TestCodexRootPrefersCodexHome(t *testing.T) {
	clearCodexEnv(t)
	t.Setenv("CODEX_HOME", "/custom/codex")

	got, err := strip.Codex().Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if want := filepath.Join("/custom/codex", "sessions"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestCodexRootFallsBackToHomeDotCodexSessions(t *testing.T) {
	clearCodexEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := strip.Codex().Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if want := filepath.Join(home, ".codex", "sessions"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestCodexRootFailsWithoutAHomeDirectory(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Skip("HOME is already unset in this environment")
	}
	clearCodexEnv(t)
	t.Setenv("HOME", "")

	if _, err := strip.Codex().Root(); err == nil {
		t.Error("Root succeeded with no CODEX_HOME or HOME")
	}
}

func TestCodexIsTranscriptOnlyAcceptsPlainRollouts(t *testing.T) {
	p := strip.Codex()
	cases := map[string]bool{
		"2026/06/01/rollout-2026-06-01T09-00-00-c0dec001-0000-4000-8000-000000000001.jsonl": true,
		"rollout-x.jsonl":        true,
		"rollout-x.jsonl.zst":    false,
		"rollout-x.jsonl.bak":    false,
		"rollout-x":              false,
		"history.jsonl":          false,
		"session_index.jsonl":    false,
		"2026/06/01/notes.md":    false,
		"2026/06/01/config.toml": false,
		"":                       false,
	}
	rels := make([]string, 0, len(cases))
	for rel := range cases {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if got := p.IsTranscript(rel); got != cases[rel] {
			t.Errorf("IsTranscript(%q) = %v, want %v", rel, got, cases[rel])
		}
	}
	if got := p.Observation().Compressed; got != 1 {
		t.Errorf("Observation().Compressed = %d after one rollout-*.jsonl.zst, want 1", got)
	}
}

// TestCodexRowsDecodeToTheirManifestTurnCounts is the manifest cross-check:
// every quirk row, decoded whole, against the count fixtures states by hand
// for it. It also pins the fields the archive stamps for itself — Origin is
// the store's own agent and Repo is the resolver's, so a decoder that writes
// either is claiming an answer it does not have.
func TestCodexRowsDecodeToTheirManifestTurnCounts(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	if len(c.Manifest.Rows) == 0 {
		t.Fatal("the codex manifest holds no rows; every count here would be vacuous")
	}

	for _, row := range c.Manifest.Rows {
		t.Run(string(row.Quirk), func(t *testing.T) {
			if row.Opaque {
				t.Skip("counted by IsTranscript and never decoded; see TestZstdRolloutIsCountedAndNeverWalked")
			}
			if row.ThreadID == "" {
				t.Fatal("the manifest row states no thread id; the session assertion below would be vacuous")
			}
			turns := decodeCodexRow(t, strip.Codex(), c, row)
			if len(turns) != row.ExpectedTurns {
				t.Fatalf("decoded %d turns, want %d from the manifest; texts: %q",
					len(turns), row.ExpectedTurns, codexTextsOf(turns))
			}
			for i, turn := range turns {
				if turn.Session != row.ThreadID {
					t.Errorf("turn %d Session = %q, want %q from the manifest", i, turn.Session, row.ThreadID)
				}
				if turn.CWD != row.CWD {
					t.Errorf("turn %d CWD = %q, want %q from the manifest", i, turn.CWD, row.CWD)
				}
				if turn.Origin != "" {
					t.Errorf("turn %d Origin = %q, want empty: the store stamps it", i, turn.Origin)
				}
				if turn.Repo != "" {
					t.Errorf("turn %d Repo = %q, want empty: the archive's resolver fills it", i, turn.Repo)
				}
				if turn.Text == "" {
					t.Errorf("turn %d carries no text", i)
				}
			}
		})
	}
}

// TestPlainRolloutDecodesEveryRecordToItsTierAuthorAndKey states the whole
// expected result for an ordinary rollout up front, from the records on disk
// and the documented key rule, and compares it in one go — so a wrong tier, a
// wrong author, a rewritten timestamp or a leaked argument payload each fail
// on their own line rather than hiding behind a turn count.
func TestPlainRolloutDecodesEveryRecordToItsTierAuthorAndKey(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkPlain)
	lines := codexLines(t, c.Path(row.File))

	userLine := findCodexLine(t, lines, "user message", codexMessageRole("user"))
	assistantLine := findCodexLine(t, lines, "assistant message", codexMessageRole("assistant"))
	callLine := findCodexLine(t, lines, "function_call", codexTypeIs("response_item", "function_call"))
	outputLine := findCodexLine(t, lines, "function_call_output", codexTypeIs("response_item", "function_call_output"))

	call := codexRecord(t, callLine)
	arguments := call.Get("payload.arguments").String()
	if arguments == "" {
		t.Fatal("the plain row's function_call carries no arguments; the drop below would be vacuous")
	}

	turn := func(line jsonl.Line, tier schema.Tier, author schema.Author, text string) schema.Turn {
		rec := codexRecord(t, line)
		return schema.Turn{
			Session: row.ThreadID,
			UUID:    codexWantUUID(line),
			TS:      rec.Get("timestamp").String(),
			Tier:    tier,
			Author:  author,
			CWD:     row.CWD,
			Text:    text,
		}
	}
	want := []schema.Turn{
		turn(userLine, schema.TierConversation, schema.AuthorHuman,
			codexRecord(t, userLine).Get("payload.content.0.text").String()),
		turn(assistantLine, schema.TierConversation, schema.AuthorAssistant,
			codexRecord(t, assistantLine).Get("payload.content.0.text").String()),
		turn(callLine, schema.TierInvocation, schema.AuthorAssistant,
			call.Get("payload.name").String()+" "+call.Get("payload.call_id").String()),
		turn(outputLine, schema.TierResult, schema.AuthorSystem,
			codexRecord(t, outputLine).Get("payload.output").String()),
	}

	got := decodeCodex(t, strip.Codex().Decoder(codexRel(t, c, row)), lines)
	if len(got) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest", len(got), row.ExpectedTurns)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded turns = %#v, want %#v", got, want)
	}
	for _, g := range got {
		if strings.Contains(g.Text, arguments) {
			t.Errorf("turn text %q carries the function_call arguments %q, which the invocation tier exists to drop", g.Text, arguments)
		}
	}
}

// TestEventMsgDuplicateArchivesTheResponseItemAndCountsTheEventMsg is the
// double-count trap: the two records carry the same words, so only the key
// says which one survived. A decoder that kept the event_msg would produce a
// turn keyed at the event_msg's position.
func TestEventMsgDuplicateArchivesTheResponseItemAndCountsTheEventMsg(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkEventMsgDuplicate)
	lines := codexLines(t, c.Path(row.File))

	userLine := findCodexLine(t, lines, "user message", codexMessageRole("user"))
	eventLine := findCodexLine(t, lines, "event_msg", codexTypeIs("event_msg", ""))
	userText := codexRecord(t, userLine).Get("payload.content.0.text").String()
	eventText := codexRecord(t, eventLine).Get("payload.message").String()
	if userText == "" || userText != eventText {
		t.Fatalf("the row's response_item text %q and event_msg text %q differ; nothing here would discriminate the two", userText, eventText)
	}

	p := strip.Codex()
	turns := decodeCodex(t, p.Decoder(codexRel(t, c, row)), lines)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest; texts: %q", len(turns), row.ExpectedTurns, codexTextsOf(turns))
	}
	if n := countCodexText(turns, userText); n != 1 {
		t.Fatalf("%d turns carry the duplicated text %q, want exactly 1", n, userText)
	}

	var got schema.Turn
	for _, turn := range turns {
		if turn.Text == userText {
			got = turn
		}
	}
	if want := codexWantUUID(userLine); got.UUID != want {
		t.Errorf("the surviving turn's UUID = %q, want %q: it must be keyed at the response_item", got.UUID, want)
	}
	if pos := codexUUIDPosition(t, got.UUID); pos == eventLine.Offset {
		t.Errorf("the surviving turn is keyed at the event_msg's position %d: the telemetry copy was archived, not the conversation record", pos)
	}
	if got.Author != schema.AuthorHuman {
		t.Errorf("the surviving turn's Author = %q, want %q", got.Author, schema.AuthorHuman)
	}
	if got.Tier != schema.TierConversation {
		t.Errorf("the surviving turn's Tier = %q, want %q", got.Tier, schema.TierConversation)
	}
	if n := p.Observation().Telemetry; n != 1 {
		t.Errorf("Observation().Telemetry = %d, want 1: the event_msg is counted, not silently dropped", n)
	}
}

// TestMissingSessionMetaTakesTheThreadIDFromTheRolloutName covers the
// truncated head: with no session_meta anywhere in the file, the name is the
// only place the thread id survives, and the alternative to reading it is
// archiving turns nothing can attribute.
func TestMissingSessionMetaTakesTheThreadIDFromTheRolloutName(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkMissingSessionMeta)
	lines := codexLines(t, c.Path(row.File))
	for _, line := range lines {
		if codexTypeIs("session_meta", "")(codexRecord(t, line)) {
			t.Fatal("the missing-session-meta row carries a session_meta record; this test would prove nothing")
		}
	}
	if row.ThreadID == "" || !strings.Contains(row.File, row.ThreadID) {
		t.Fatalf("the manifest's thread id %q is not in the file name %q", row.ThreadID, row.File)
	}

	turns := decodeCodex(t, strip.Codex().Decoder(codexRel(t, c, row)), lines)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest", len(turns), row.ExpectedTurns)
	}
	for i, turn := range turns {
		if turn.Session != row.ThreadID {
			t.Errorf("turn %d Session = %q, want %q from the file name", i, turn.Session, row.ThreadID)
		}
	}
}

// TestForkedRolloutNameYieldsTheThreadNotTheRollout pins the fork spelling:
// a reverted thread appends a second id, and keying on it would file the
// fork's turns under a session nothing else in the archive uses.
func TestForkedRolloutNameYieldsTheThreadNotTheRollout(t *testing.T) {
	const (
		thread  = "c0dec001-0000-4000-8000-0000000000aa"
		rollout = "c0dec001-0000-4000-8000-0000000000bb"
	)
	rel := "2026/06/09/rollout-2026-06-09T09-00-00-" + thread + "_" + rollout + ".jsonl"

	dec := strip.Codex().Decoder(rel)
	turns := decodeCodex(t, dec, []jsonl.Line{
		codexLineOf(0, codexMessageJSON("2026-06-09T09:00:05Z", "user", "resume from the fork.")),
	})
	if len(turns) != 1 {
		t.Fatalf("decoded %d turns from one user message, want 1", len(turns))
	}
	if turns[0].Session != thread {
		t.Errorf("Session = %q, want the thread id %q rather than the rollout id %q", turns[0].Session, thread, rollout)
	}
}

// TestAnUnreadableRolloutNameStillArchivesItsTurns keeps the failure to
// recover an id from costing the words: an unattributed turn is still
// findable, a dropped one is the false negative recall exists to prevent.
func TestAnUnreadableRolloutNameStillArchivesItsTurns(t *testing.T) {
	dec := strip.Codex().Decoder("2026/06/09/rollout-migrated.jsonl")
	turns := decodeCodex(t, dec, []jsonl.Line{
		codexLineOf(0, codexMessageJSON("2026-06-09T09:00:05Z", "user", "the name lost its thread id.")),
	})
	if len(turns) != 1 {
		t.Fatalf("decoded %d turns from one user message, want 1", len(turns))
	}
	if turns[0].Session != "" {
		t.Errorf("Session = %q, want empty: the name carries no thread id to read", turns[0].Session)
	}
	if want := "the name lost its thread id."; turns[0].Text != want {
		t.Errorf("Text = %q, want %q", turns[0].Text, want)
	}
}

// TestCompactedArchivesTheSummaryAndNotTheReplacedHistory covers the second
// double-count trap: replacement_history restates turns already archived from
// their own earlier records, so archiving it again would put the same words in
// the store twice.
func TestCompactedArchivesTheSummaryAndNotTheReplacedHistory(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkCompacted)
	lines := codexLines(t, c.Path(row.File))

	compacted := codexRecord(t, findCodexLine(t, lines, "compacted", codexTypeIs("compacted", "")))
	summary := compacted.Get("payload.message").String()
	replaced := compacted.Get("payload.replacement_history.0.content.0.text").String()
	if summary == "" || replaced == "" {
		t.Fatalf("the compacted row carries summary %q and replaced text %q; both must be present for this test to discriminate", summary, replaced)
	}

	p := strip.Codex()
	turns := decodeCodex(t, p.Decoder(codexRel(t, c, row)), lines)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest; texts: %q", len(turns), row.ExpectedTurns, codexTextsOf(turns))
	}
	if n := countCodexText(turns, summary); n != 1 {
		t.Errorf("%d turns carry the compaction summary %q, want exactly 1", n, summary)
	}
	for i, turn := range turns {
		if strings.Contains(turn.Text, replaced) {
			t.Errorf("turn %d text %q carries the replaced history %q, which was archived from its own earlier record", i, turn.Text, replaced)
		}
	}
	if n := p.Observation().Replaced; n != 1 {
		t.Errorf("Observation().Replaced = %d, want 1: what was skipped has to be countable", n)
	}

	for _, turn := range turns {
		if turn.Text != summary {
			continue
		}
		if turn.Tier != schema.TierConversation {
			t.Errorf("the summary turn's Tier = %q, want %q", turn.Tier, schema.TierConversation)
		}
		if turn.Author != schema.AuthorSystem {
			t.Errorf("the summary turn's Author = %q, want %q: the tool wrote the summary, the operator did not", turn.Author, schema.AuthorSystem)
		}
	}
}

// TestZstdRolloutIsCountedAndNeverWalked walks the corpus the way the archive
// does. A cold rollout is opaque bytes with no JSON in them, so the only
// honest handling is to count it as unread and leave it out of the walk that
// decides what gets decoded.
func TestZstdRolloutIsCountedAndNeverWalked(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	p := strip.Codex()
	root, err := p.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}

	walked := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if p.IsTranscript(rel) {
			walked[rel] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	opaque := 0
	for _, row := range c.Manifest.Rows {
		rel := codexRel(t, c, row)
		if row.Opaque {
			opaque++
			if row.ExpectedTurns != 0 {
				t.Errorf("the manifest states %d turns for the opaque row, want 0", row.ExpectedTurns)
			}
			if walked[rel] {
				t.Errorf("IsTranscript accepted the compressed rollout %q; nothing here decompresses zstd", rel)
			}
			continue
		}
		if !walked[rel] {
			t.Errorf("IsTranscript rejected the plain rollout %q", rel)
		}
	}
	if opaque == 0 {
		t.Fatal("the manifest holds no opaque row; this test would prove nothing")
	}
	if got := p.Observation().Compressed; got != opaque {
		t.Errorf("Observation().Compressed = %d, want %d: doctor can only declare an unread file if the walk counts it", got, opaque)
	}
}

// TestSubagentRolloutAttributesEveryTurnToTheAgent covers the whole file, not
// its first turn: the nickname outranks the role each record speaks in, the
// same order Claude Code's rule uses for a sidechain record.
func TestSubagentRolloutAttributesEveryTurnToTheAgent(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkSubagent)
	lines := codexLines(t, c.Path(row.File))

	meta := codexRecord(t, findCodexLine(t, lines, "session_meta", codexTypeIs("session_meta", "")))
	nickname := meta.Get("payload.agent_nickname").String()
	if nickname == "" {
		t.Fatal("the subagent row's session_meta carries no agent_nickname")
	}

	roles := map[string]bool{}
	for _, line := range lines {
		rec := codexRecord(t, line)
		if codexTypeIs("response_item", "message")(rec) {
			roles[rec.Get("payload.role").String()] = true
		}
	}
	if !roles["user"] || !roles["assistant"] {
		t.Fatalf("the subagent row's messages carry roles %v; without both, agent attribution could be the answer a role rule gives anyway", roles)
	}

	turns := decodeCodexRow(t, strip.Codex(), c, row)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest", len(turns), row.ExpectedTurns)
	}
	if len(turns) < 2 {
		t.Fatalf("the subagent row yields %d turns; 'every turn' needs more than one", len(turns))
	}
	for i, turn := range turns {
		if turn.Author != schema.AuthorAgent {
			t.Errorf("turn %d Author = %q, want %q", i, turn.Author, schema.AuthorAgent)
		}
		if turn.Agent != nickname {
			t.Errorf("turn %d Agent = %q, want %q", i, turn.Agent, nickname)
		}
	}

	// The negative control: without a nickname the roles come through, so the
	// attribution above is read from session_meta rather than applied always.
	plain := decodeCodexRow(t, strip.Codex(), c, codexRow(t, c, fixtures.CodexQuirkPlain))
	for i, turn := range plain {
		if turn.Author == schema.AuthorAgent {
			t.Errorf("plain turn %d Author = %q on a rollout with no agent_nickname", i, turn.Author)
		}
		if turn.Agent != "" {
			t.Errorf("plain turn %d Agent = %q, want empty on a rollout with no agent_nickname", i, turn.Agent)
		}
	}
}

// TestUnknownEnvelopeTypesAreCountedByTypeAndTheFileStillArchives is the
// fail-loud branch. Codex adds record kinds between releases, so the file has
// to keep decoding around one, and doctor has to be able to name it.
func TestUnknownEnvelopeTypesAreCountedByTypeAndTheFileStillArchives(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkPlain)
	lines := codexLines(t, c.Path(row.File))

	unknown := func(offset int64, typ string) jsonl.Line {
		return codexLineOf(offset, fmt.Sprintf(
			`{"timestamp":"2026-06-01T09:00:07Z","type":%q,"payload":{"note":"a record kind this build does not read"}}`, typ))
	}
	// Past the end of the real file, so the injected records cannot collide
	// with a real record's position.
	const base = 1 << 20
	mixed := []jsonl.Line{lines[0],
		unknown(base, "thread_migration_marker"),
		unknown(base+1, "thread_migration_marker"),
		unknown(base+2, "attention_budget"),
	}
	mixed = append(mixed, lines[1:]...)

	p := strip.Codex()
	turns := decodeCodex(t, p.Decoder(codexRel(t, c, row)), mixed)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns around three unknown records, want %d from the manifest; texts: %q",
			len(turns), row.ExpectedTurns, codexTextsOf(turns))
	}

	obs := p.Observation()
	want := map[string]int{"thread_migration_marker": 2, "attention_budget": 1}
	if !reflect.DeepEqual(obs.Tally.Unknown, want) {
		t.Errorf("Observation().Tally.Unknown = %v, want %v", obs.Tally.Unknown, want)
	}
	if got := obs.Tally.Lines; got != len(mixed) {
		t.Errorf("Observation().Tally.Lines = %d, want %d: every record is observed, decoded or not", got, len(mixed))
	}
}

// TestUnknownResponseItemPayloadIsCountedSeparately keeps the second
// discriminator honest: an unreadable payload type arrives under a known
// envelope, so the envelope tally cannot see it and doctor would report a
// clean corpus while turns went missing.
func TestUnknownResponseItemPayloadIsCountedSeparately(t *testing.T) {
	p := strip.Codex()
	dec := p.Decoder("2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000cc.jsonl")
	turns := decodeCodex(t, dec, []jsonl.Line{
		codexLineOf(0, `{"timestamp":"2026-06-09T09:00:05Z","type":"response_item","payload":{"type":"local_shell_call","action":{}}}`),
		codexLineOf(200, codexMessageJSON("2026-06-09T09:00:06Z", "assistant", "and the file keeps decoding.")),
	})
	if len(turns) != 1 {
		t.Fatalf("decoded %d turns, want 1: the unknown payload yields none and the message still does", len(turns))
	}
	if want := "and the file keeps decoding."; turns[0].Text != want {
		t.Errorf("Text = %q, want %q", turns[0].Text, want)
	}

	obs := p.Observation()
	if want := map[string]int{"local_shell_call": 1}; !reflect.DeepEqual(obs.UnknownPayloads, want) {
		t.Errorf("Observation().UnknownPayloads = %v, want %v", obs.UnknownPayloads, want)
	}
	if len(obs.Tally.Unknown) != 0 {
		t.Errorf("Observation().Tally.Unknown = %v, want empty: response_item is a known envelope type", obs.Tally.Unknown)
	}
}

// TestEncryptedReasoningCarriesNoWordsIntoTheArchive: the model's thinking is
// stored encrypted with an empty summary, so a turn made from it would be a
// blob of base64 in the conversation tier.
func TestEncryptedReasoningCarriesNoWordsIntoTheArchive(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkEncryptedReasoning)
	lines := codexLines(t, c.Path(row.File))

	reasoning := codexRecord(t, findCodexLine(t, lines, "reasoning", codexTypeIs("response_item", "reasoning")))
	blob := reasoning.Get("payload.encrypted_content").String()
	if blob == "" {
		t.Fatal("the encrypted-reasoning row carries no encrypted_content")
	}

	turns := decodeCodex(t, strip.Codex().Decoder(codexRel(t, c, row)), lines)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest; texts: %q", len(turns), row.ExpectedTurns, codexTextsOf(turns))
	}
	for i, turn := range turns {
		if strings.Contains(turn.Text, blob) {
			t.Errorf("turn %d text %q carries the encrypted reasoning blob", i, turn.Text)
		}
		if codexUUIDPosition(t, turn.UUID) == reasoning.Offset {
			t.Errorf("turn %d is keyed at the reasoning record's position", i)
		}
	}
}

// TestSessionMetaGitBranchReachesEveryTurn: the branch is written once, in the
// head, and every turn in the file is on it.
func TestSessionMetaGitBranchReachesEveryTurn(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	row := codexRow(t, c, fixtures.CodexQuirkRepoIdentity)
	lines := codexLines(t, c.Path(row.File))

	meta := codexRecord(t, findCodexLine(t, lines, "session_meta", codexTypeIs("session_meta", "")))
	branch := meta.Get("payload.git.branch").String()
	if branch == "" {
		t.Fatal("the repo-identity row's session_meta carries no git.branch")
	}

	turns := decodeCodex(t, strip.Codex().Decoder(codexRel(t, c, row)), lines)
	if len(turns) != row.ExpectedTurns {
		t.Fatalf("decoded %d turns, want %d from the manifest", len(turns), row.ExpectedTurns)
	}
	for i, turn := range turns {
		if turn.Branch != branch {
			t.Errorf("turn %d Branch = %q, want %q from session_meta", i, turn.Branch, branch)
		}
	}

	// The negative control: a rollout whose session_meta carries no git block
	// leaves the branch empty rather than inheriting one from anywhere else.
	plainRow := codexRow(t, c, fixtures.CodexQuirkPlain)
	plainMeta := codexRecord(t, findCodexLine(t, codexLines(t, c.Path(plainRow.File)), "session_meta", codexTypeIs("session_meta", "")))
	if plainMeta.Get("payload.git").Exists() {
		t.Fatal("the plain row's session_meta carries a git block; it cannot serve as the no-branch control")
	}
	for i, turn := range decodeCodexRow(t, strip.Codex(), c, plainRow) {
		if turn.Branch != "" {
			t.Errorf("plain turn %d Branch = %q, want empty", i, turn.Branch)
		}
	}
}

// TestSynthesisedKeySeparatesIdenticalRecordsAtDifferentPositions proves the
// position half of the key. Two records with the same bytes are two messages,
// not one, and a key made from the content alone would delete the second.
func TestSynthesisedKeySeparatesIdenticalRecordsAtDifferentPositions(t *testing.T) {
	body := codexMessageJSON("2026-06-09T09:00:05Z", "user", "run it again.")
	dec := strip.Codex().Decoder("2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000dd.jsonl")

	turns := decodeCodex(t, dec, []jsonl.Line{codexLineOf(0, body), codexLineOf(512, body)})
	if len(turns) != 2 {
		t.Fatalf("decoded %d turns from two identical records, want 2", len(turns))
	}
	if turns[0].Text != turns[1].Text {
		t.Fatalf("the two turns differ in text (%q, %q); the records were meant to be identical", turns[0].Text, turns[1].Text)
	}
	if turns[0].UUID == turns[1].UUID {
		t.Errorf("both turns keyed %q: a repeated message would collapse to one", turns[0].UUID)
	}
	if got, want := codexUUIDPosition(t, turns[1].UUID), int64(512); got != want {
		t.Errorf("the second turn's key position = %d, want %d", got, want)
	}
}

// TestSynthesisedKeySeparatesDifferentRecordsAtOnePosition proves the content
// half. Every rollout has a record at position zero, so a key made from the
// position alone would merge one file's opening turn into another's.
func TestSynthesisedKeySeparatesDifferentRecordsAtOnePosition(t *testing.T) {
	p := strip.Codex()
	first := decodeCodex(t, p.Decoder("2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000ee.jsonl"),
		[]jsonl.Line{codexLineOf(0, codexMessageJSON("2026-06-09T09:00:05Z", "user", "first thread's opening line."))})
	second := decodeCodex(t, p.Decoder("2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000ff.jsonl"),
		[]jsonl.Line{codexLineOf(0, codexMessageJSON("2026-06-09T09:00:05Z", "user", "second thread's opening line."))})
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("decoded %d and %d turns, want 1 each", len(first), len(second))
	}
	if codexUUIDPosition(t, first[0].UUID) != codexUUIDPosition(t, second[0].UUID) {
		t.Fatal("the two records are not at the same position; this test would prove nothing")
	}
	if first[0].UUID == second[0].UUID {
		t.Errorf("both turns keyed %q: two distinct opening messages would collapse to one", first[0].UUID)
	}
}

// TestIdenticalRecordsInTwoRolloutsAreSeparatedByTheirSession states the
// remaining reliance: the same words at the same position in two rollouts do
// key alike, and the session half of the archive's dedup key is what keeps
// them apart.
func TestIdenticalRecordsInTwoRolloutsAreSeparatedByTheirSession(t *testing.T) {
	body := codexMessageJSON("2026-06-09T09:00:05Z", "user", "hello.")
	p := strip.Codex()
	first := decodeCodex(t, p.Decoder("2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-00000000ab01.jsonl"),
		[]jsonl.Line{codexLineOf(0, body)})
	second := decodeCodex(t, p.Decoder("2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-00000000ab02.jsonl"),
		[]jsonl.Line{codexLineOf(0, body)})
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("decoded %d and %d turns, want 1 each", len(first), len(second))
	}
	if first[0].UUID != second[0].UUID {
		t.Errorf("keys %q and %q differ; the uuid halves read the record alone, so identical records must key alike",
			first[0].UUID, second[0].UUID)
	}
	if first[0].Session == second[0].Session {
		t.Errorf("both turns carry session %q, so nothing separates them in the archive's dedup key", first[0].Session)
	}
}

// TestPrimingAResumedReadKeysEveryRecordTheSameWay is the archive's resumed
// read: a grown file is decoded from a byte cursor with its head replayed for
// context. The keys it produces have to be the ones a whole read produces, or
// a later rebuild keeps both numberings as two copies of one turn.
func TestPrimingAResumedReadKeysEveryRecordTheSameWay(t *testing.T) {
	c := fixtures.MaterializeCodex(t)
	for _, quirk := range []fixtures.CodexQuirk{fixtures.CodexQuirkPlain, fixtures.CodexQuirkMissingSessionMeta} {
		t.Run(string(quirk), func(t *testing.T) {
			row := codexRow(t, c, quirk)
			lines := codexLines(t, c.Path(row.File))
			if len(lines) < 2 {
				t.Fatalf("the %q row holds %d lines; a resumed read needs a head and a tail", quirk, len(lines))
			}
			rel := codexRel(t, c, row)

			whole := decodeCodex(t, strip.Codex().Decoder(rel), lines)
			if len(whole) != row.ExpectedTurns {
				t.Fatalf("a whole read decoded %d turns, want %d from the manifest", len(whole), row.ExpectedTurns)
			}

			// What archive.primeHead does: hand the decoder the first record for
			// context and throw away whatever it decodes from it.
			resumedDec := strip.Codex().Decoder(rel)
			resumedDec.Turns(codexRecord(t, lines[0]))
			resumed := decodeCodex(t, resumedDec, lines[max(1, len(lines)-2):])

			if len(resumed) == 0 {
				t.Fatal("the resumed read decoded no turns; this test would prove nothing")
			}
			want := whole[len(whole)-len(resumed):]
			if !reflect.DeepEqual(resumed, want) {
				t.Errorf("resumed read produced %#v, want %#v from the whole read", resumed, want)
			}
		})
	}
}

// TestSessionMetaIDOverridesTheFilenameThreadID is the primary path the
// fixture corpus cannot exercise: every fixture rollout's name embeds the same
// id its session_meta carries, so only a name and a session_meta built to
// disagree can prove the id is read from the record rather than the name.
func TestSessionMetaIDOverridesTheFilenameThreadID(t *testing.T) {
	const (
		nameID = "c0dec001-0000-4000-8000-0000000000a1"
		metaID = "c0dec001-0000-4000-8000-0000000000b2"
	)
	rel := "2026/06/09/rollout-2026-06-09T09-00-00-" + nameID + ".jsonl"
	meta := fmt.Sprintf(`{"timestamp":"2026-06-09T09:00:00Z","type":"session_meta","payload":{"id":%q}}`, metaID)

	turns := decodeCodex(t, strip.Codex().Decoder(rel), []jsonl.Line{
		codexLineOf(0, meta),
		codexLineOf(200, codexMessageJSON("2026-06-09T09:00:05Z", "user", "read the id from session_meta, not the filename.")),
	})
	if len(turns) != 1 {
		t.Fatalf("decoded %d turns, want 1", len(turns))
	}
	if turns[0].Session != metaID {
		t.Errorf("Session = %q, want %q from session_meta.id, not the filename's %q", turns[0].Session, metaID, nameID)
	}
}

// TestCompactedWithNoReplacementHistoryStillArchivesTheSummary covers the
// compacted record shape TestCompactedArchivesTheSummaryAndNotTheReplacedHistory
// cannot: a record whose payload carries no replacement_history key at all,
// rather than an empty one.
func TestCompactedWithNoReplacementHistoryStillArchivesTheSummary(t *testing.T) {
	rel := "2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000c3.jsonl"
	body := `{"timestamp":"2026-06-09T09:00:05Z","type":"compacted","payload":` +
		`{"message":"summary with no replacement_history key."}}`

	p := strip.Codex()
	turns := decodeCodex(t, p.Decoder(rel), []jsonl.Line{codexLineOf(0, body)})
	if len(turns) != 1 {
		t.Fatalf("decoded %d turns, want 1: a compacted record with no replacement_history key still carries its summary", len(turns))
	}
	if want := "summary with no replacement_history key."; turns[0].Text != want {
		t.Errorf("Text = %q, want %q", turns[0].Text, want)
	}
	if n := p.Observation().Replaced; n != 0 {
		t.Errorf("Observation().Replaced = %d, want 0: the payload names no replacement count", n)
	}
}

// TestMultiPartMessageContentIsJoinedByABlankLine covers the shape no fixture
// message carries: two content parts in one record, which codexText must join
// rather than truncate to the first.
func TestMultiPartMessageContentIsJoinedByABlankLine(t *testing.T) {
	rel := "2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000d4.jsonl"
	body := `{"timestamp":"2026-06-09T09:00:05Z","type":"response_item","payload":` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"first part."},{"type":"input_text","text":"second part."}]}}`

	turns := decodeCodex(t, strip.Codex().Decoder(rel), []jsonl.Line{codexLineOf(0, body)})
	if len(turns) != 1 {
		t.Fatalf("decoded %d turns, want 1", len(turns))
	}
	if want := "first part.\n\nsecond part."; turns[0].Text != want {
		t.Errorf("Text = %q, want %q: multiple content parts join with a blank line", turns[0].Text, want)
	}
}

// TestRecordWithNoTypeIsCountedAsUntyped pins the tally observe drops
// otherwise: a record carrying no type key at all still has to register as one
// line read, keyed under Untyped rather than silently skipped.
func TestRecordWithNoTypeIsCountedAsUntyped(t *testing.T) {
	rel := "2026/06/09/rollout-2026-06-09T09-00-00-c0dec001-0000-4000-8000-0000000000e5.jsonl"
	body := `{"timestamp":"2026-06-09T09:00:05Z"}`

	p := strip.Codex()
	turns := decodeCodex(t, p.Decoder(rel), []jsonl.Line{codexLineOf(0, body)})
	if len(turns) != 0 {
		t.Fatalf("decoded %d turns from an untyped record, want 0", len(turns))
	}
	obs := p.Observation()
	if obs.Tally.Untyped != 1 {
		t.Errorf("Observation().Tally.Untyped = %d, want 1", obs.Tally.Untyped)
	}
	if obs.Tally.Lines != 1 {
		t.Errorf("Observation().Tally.Lines = %d, want 1: the record is observed even though it carries no type", obs.Tally.Lines)
	}
}
