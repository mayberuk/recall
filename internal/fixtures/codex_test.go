package fixtures

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/jsonl"
)

func readCodexRecords(t *testing.T, path string) []jsonl.Record {
	t.Helper()
	r, err := jsonl.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var out []jsonl.Record
	for r.Next() {
		l := r.Line()
		cp := append([]byte(nil), l.Bytes...)
		rec, ok := jsonl.Parse(jsonl.Line{Offset: l.Offset, Length: l.Length, Bytes: cp})
		if !ok {
			t.Fatalf("%s: line at %d did not parse", path, l.Offset)
		}
		out = append(out, rec)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// countCodexTurns recomputes a turn count structurally from the raw records,
// following the rule documented on codexPlants: a response_item counts when
// it is a message with text, a function_call or a function_call_output; an
// event_msg never counts; a reasoning item never counts, regardless of its
// summary; a compacted record counts its summary message only —
// replacement_history holds the pre-compaction turns the summary replaced,
// already archived from their own earlier response_item records, so they
// never count. This is a hand check of the fixture's own self-consistency,
// not a decoder — internal/strip has no Codex reader yet.
func countCodexTurns(t *testing.T, path string) int {
	t.Helper()
	turns := 0
	for _, rec := range readCodexRecords(t, path) {
		payload := rec.Get("payload")
		switch rec.Get("type").String() {
		case "response_item":
			switch payload.Get("type").String() {
			case "message":
				if len(payload.Get("content").Array()) > 0 && payload.Get("content.0.text").String() != "" {
					turns++
				}
			case "function_call", "function_call_output":
				turns++
			}
		case "compacted":
			if payload.Get("message").String() != "" {
				turns++
			}
		}
	}
	return turns
}

// codexRolloutName matches sessions/YYYY/MM/DD/rollout-<ISO>-<threadID>.jsonl,
// the layout the row's own File field must produce.
var codexRolloutName = regexp.MustCompile(
	`^sessions/\d{4}/\d{2}/\d{2}/rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-[0-9a-f-]+\.jsonl(\.zst)?$`)

func TestMaterializeCodexBuildsADatedRolloutTree(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	realCodexHome := filepath.Join(realHome, ".codex")
	before, _ := os.ReadDir(realCodexHome)

	c := MaterializeCodex(t)

	if got := os.Getenv("CODEX_HOME"); got != c.Root {
		t.Errorf("CODEX_HOME = %q, want %q", got, c.Root)
	}
	if c.Root == realCodexHome {
		t.Fatal("CODEX_HOME was relocated onto the caller's real ~/.codex")
	}
	if !strings.HasPrefix(c.Root, os.TempDir()) && !strings.Contains(c.Root, "TMPDIR") {
		// t.TempDir() always nests under a process-wide temp root; this is a
		// sanity check that Root is not some fixed, shared location.
		if fi, err := os.Stat(c.Root); err != nil || !fi.IsDir() {
			t.Errorf("Root %q is not the directory MaterializeCodex just created", c.Root)
		}
	}

	after, _ := os.ReadDir(realCodexHome)
	if len(after) != len(before) {
		t.Errorf("entries under the real ~/.codex changed from %d to %d", len(before), len(after))
	}

	for _, row := range c.Manifest.Rows {
		if !codexRolloutName.MatchString(row.File) {
			t.Errorf("%s: File %q is not a dated rollout path", row.Quirk, row.File)
		}
		if !strings.Contains(row.File, row.ThreadID) {
			t.Errorf("%s: File %q does not name its own thread id %q", row.Quirk, row.File, row.ThreadID)
		}
		if _, err := os.Stat(c.Path(row.File)); err != nil {
			t.Errorf("%s: %v", row.Quirk, err)
		}
	}
}

func TestCodexManifestStatesExpectedTurnsForEveryRow(t *testing.T) {
	c := MaterializeCodex(t)

	want := []CodexQuirk{
		CodexQuirkPlain, CodexQuirkEventMsgDuplicate, CodexQuirkCompacted,
		CodexQuirkMissingSessionMeta, CodexQuirkSubagent, CodexQuirkZstdOpaque,
		CodexQuirkRepoIdentity, CodexQuirkEncryptedReasoning,
	}
	if len(c.Manifest.Rows) != len(want) {
		t.Fatalf("%d rows, want one per quirk (%d)", len(c.Manifest.Rows), len(want))
	}
	seen := map[CodexQuirk]CodexRow{}
	for _, row := range c.Manifest.Rows {
		seen[row.Quirk] = row
	}
	for _, q := range want {
		row, ok := seen[q]
		if !ok {
			t.Errorf("no row for quirk %s", q)
			continue
		}
		if row.Opaque {
			if row.ExpectedTurns != 0 {
				t.Errorf("%s: an opaque row states %d expected turns, want 0", q, row.ExpectedTurns)
			}
			continue
		}
		if row.ExpectedTurns <= 0 {
			t.Errorf("%s: ExpectedTurns is %d, want a positive count", q, row.ExpectedTurns)
		}
	}
}

func TestCodexRowTurnCountsMatchTheirRecords(t *testing.T) {
	c := MaterializeCodex(t)
	for _, row := range c.Manifest.Rows {
		t.Run(string(row.Quirk), func(t *testing.T) {
			if row.Opaque {
				t.Skip("opaque rows are never parsed as JSONL")
			}
			got := countCodexTurns(t, c.Path(row.File))
			if got != row.ExpectedTurns {
				t.Errorf("counted %d turns, manifest says %d", got, row.ExpectedTurns)
			}
		})
	}
}

func TestCodexZstdFileIsOpaqueAndNeverDecompressed(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	found := false
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkZstdOpaque {
			row, found = r, true
		}
	}
	if !found {
		t.Fatal("no zstd-opaque row in the manifest")
	}
	if !row.Opaque {
		t.Error("zstd-opaque row is not marked Opaque")
	}
	if !strings.HasSuffix(row.File, ".jsonl.zst") {
		t.Errorf("File = %q, want a .jsonl.zst suffix", row.File)
	}
	data, err := os.ReadFile(c.Path(row.File))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || data[0] != 0x28 || data[1] != 0xb5 || data[2] != 0x2f || data[3] != 0xfd {
		t.Error("zstd-opaque file does not open with the zstd magic number")
	}
	if _, ok := jsonl.Parse(jsonl.Line{Bytes: data, Length: len(data)}); ok {
		t.Error("zstd-opaque bytes parse as a JSON object; the fixture is not opaque")
	}
}

func TestCodexEventMsgDuplicateIsNotDoubleCounted(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkEventMsgDuplicate {
			row = r
		}
	}
	recs := readCodexRecords(t, c.Path(row.File))
	responseUsers, eventUsers := 0, 0
	for _, rec := range recs {
		switch rec.Get("type").String() {
		case "response_item":
			if rec.Get("payload.type").String() == "message" && rec.Get("payload.role").String() == "user" {
				responseUsers++
			}
		case "event_msg":
			if rec.Get("payload.type").String() == "user_message" {
				eventUsers++
			}
		}
	}
	if responseUsers != 1 || eventUsers != 1 {
		t.Fatalf("response_item users = %d, event_msg users = %d, want 1 and 1", responseUsers, eventUsers)
	}
	if row.ExpectedTurns != 2 {
		t.Errorf("ExpectedTurns = %d, want 2 — the event_msg copy must not add a turn", row.ExpectedTurns)
	}
}

func TestCodexCompactedRowCarriesReplacementHistory(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkCompacted {
			row = r
		}
	}
	found := false
	for _, rec := range readCodexRecords(t, c.Path(row.File)) {
		if rec.Get("type").String() != "compacted" {
			continue
		}
		found = true
		if rec.Get("payload.message").String() == "" {
			t.Error("compacted record has no summary message")
		}
		history := rec.Get("payload.replacement_history").Array()
		if len(history) == 0 {
			t.Error("compacted record has no replacement_history")
		}
	}
	if !found {
		t.Error("no compacted record in the compacted-row fixture")
	}
}

func TestCodexMissingSessionMetaHasNoSessionMetaRecord(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkMissingSessionMeta {
			row = r
		}
	}
	if row.CWD != "" {
		t.Errorf("CWD = %q, want empty — nothing in the file states one", row.CWD)
	}
	for _, rec := range readCodexRecords(t, c.Path(row.File)) {
		if rec.Get("type").String() == "session_meta" {
			t.Fatal("the missing-session-meta fixture carries a session_meta record")
		}
	}
	if !strings.Contains(row.File, row.ThreadID) {
		t.Error("with no session_meta, the thread id must still be recoverable from the file name")
	}
}

func TestCodexSubagentRowCarriesAgentNickname(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkSubagent {
			row = r
		}
	}
	recs := readCodexRecords(t, c.Path(row.File))
	if len(recs) == 0 || recs[0].Get("type").String() != "session_meta" {
		t.Fatal("subagent fixture does not open with session_meta")
	}
	if got := recs[0].Get("payload.agent_nickname").String(); got == "" {
		t.Error("session_meta carries no agent_nickname")
	}
}

func TestCodexRepoIdentityRowSharesTheClaudeScratchShape(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkRepoIdentity {
			row = r
		}
	}
	want := c.ScratchPath(ScratchNormal)
	if row.CWD != want {
		t.Errorf("CWD = %q, want %q (the same scratch shape Materialize builds)", row.CWD, want)
	}
	if _, err := os.Stat(filepath.Join(row.CWD, ".git")); err != nil {
		t.Fatalf("%s is not a real git checkout: %v", row.CWD, err)
	}
}

func TestCodexEncryptedReasoningYieldsNoTurn(t *testing.T) {
	c := MaterializeCodex(t)
	var row CodexRow
	for _, r := range c.Manifest.Rows {
		if r.Quirk == CodexQuirkEncryptedReasoning {
			row = r
		}
	}
	found := false
	for _, rec := range readCodexRecords(t, c.Path(row.File)) {
		if rec.Get("payload.type").String() != "reasoning" {
			continue
		}
		found = true
		if rec.Get("payload.encrypted_content").String() == "" {
			t.Error("reasoning record has no encrypted_content")
		}
		if len(rec.Get("payload.summary").Array()) != 0 {
			t.Error("reasoning record has a non-empty summary; the fixture needs it empty")
		}
	}
	if !found {
		t.Error("no reasoning record in the encrypted-reasoning fixture")
	}
}

// TestCodexNeedleIsUniqueAcrossBothCorpora proves NeedleCodexConversation
// identifies the Codex store and no other: a hit on it inside the
// claude-code corpus, or more than one hit in total, would mean a search
// scoped to Codex could be satisfied by the wrong provider.
func TestCodexNeedleIsUniqueAcrossBothCorpora(t *testing.T) {
	codex := MaterializeCodex(t)
	claude := Materialize(t)

	var plain CodexRow
	found := false
	for _, r := range codex.Manifest.Rows {
		if r.Quirk == CodexQuirkPlain {
			plain, found = r, true
		}
	}
	if !found {
		t.Fatal("no plain-rollout row in the Codex manifest")
	}
	if !fileContains(t, codex.Path(plain.File), NeedleCodexConversation) {
		t.Errorf("%s is not in the plain rollout %s", NeedleCodexConversation, plain.File)
	}

	hits := countOccurrencesUnder(t, codex.Root, NeedleCodexConversation) +
		countOccurrencesUnder(t, claude.Root, NeedleCodexConversation)
	if hits != 1 {
		t.Errorf("%s appears %d times across both corpora, want exactly 1", NeedleCodexConversation, hits)
	}
}

func countOccurrencesUnder(t *testing.T, root, token string) int {
	t.Helper()
	total := 0
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += strings.Count(string(data), token)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return total
}

func TestMaterializeCodexIsIndependentPerCall(t *testing.T) {
	a := MaterializeCodex(t)
	b := MaterializeCodex(t)
	if a.Root == b.Root || a.Scratch == b.Scratch {
		t.Error("two calls shared a directory, so one test could corrupt another")
	}
}
