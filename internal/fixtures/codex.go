package fixtures

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// CodexCorpus is a synthetic Codex CLI session store, built in process from Go
// constants rather than checked in under tests/fixtures. Every expected value
// in Manifest is derivable by reading it — never by decoding the corpus and
// keeping what came out.
type CodexCorpus struct {
	Root     string
	Scratch  string
	Manifest CodexManifest
}

// Path is the absolute location of a Root-relative rollout file.
func (c CodexCorpus) Path(rel string) string { return filepath.Join(c.Root, rel) }

// ScratchPath is the absolute location of a scratch-relative directory, the
// same git shapes Corpus.ScratchPath resolves for the Claude corpus.
func (c CodexCorpus) ScratchPath(rel string) string { return filepath.Join(c.Scratch, rel) }

// MaterializeCodex builds a Codex CLI session store shaped like
// ~/.codex/sessions, one rollout per row of CodexManifest, and points
// CODEX_HOME at it for the duration of the test. It never reads or writes
// anything under the caller's real ~/.codex: the store lives entirely under
// t.TempDir, and nothing here consults the user's home directory.
//
// It skips rather than fails when git is missing, matching Materialize: the
// repo-identity row needs a real git checkout to resolve, and a test that
// passes because git never ran would be a worse failure than one that never
// executes.
func MaterializeCodex(t testing.TB) CodexCorpus {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("fixtures: git is not on PATH, skipping: %v", err)
	}

	base := t.TempDir()
	root := filepath.Join(base, "codexhome")
	scratch := filepath.Join(base, "scratch")

	buildScratch(t, scratch)
	t.Setenv("CODEX_HOME", root)

	plants := codexPlants(scratch)
	rows := make([]CodexRow, 0, len(plants))
	for _, p := range plants {
		path := filepath.Join(root, p.row.File)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("fixtures: cannot create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, p.body, 0o644); err != nil {
			t.Fatalf("fixtures: cannot write %s: %v", path, err)
		}
		rows = append(rows, p.row)
	}

	return CodexCorpus{Root: root, Scratch: scratch, Manifest: CodexManifest{Rows: rows}}
}

// codexRecord is the three-key envelope every Codex rollout line shares.
type codexRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexGit struct {
	CommitHash    string `json:"commit_hash"`
	Branch        string `json:"branch"`
	RepositoryURL string `json:"repository_url"`
}

type codexSessionMeta struct {
	ID            string    `json:"id"`
	Timestamp     string    `json:"timestamp"`
	CWD           string    `json:"cwd"`
	Originator    string    `json:"originator"`
	CLIVersion    string    `json:"cli_version"`
	Source        string    `json:"source"`
	ModelProvider string    `json:"model_provider"`
	Git           *codexGit `json:"git,omitempty"`
	AgentNickname string    `json:"agent_nickname,omitempty"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexMessageItem struct {
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []codexContentPart `json:"content"`
}

type codexFunctionCallItem struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
}

type codexFunctionCallOutputItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type codexReasoningItem struct {
	Type             string             `json:"type"`
	Summary          []codexContentPart `json:"summary"`
	EncryptedContent string             `json:"encrypted_content"`
}

type codexEventMsgUser struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type codexCompacted struct {
	Message            string            `json:"message"`
	ReplacementHistory []json.RawMessage `json:"replacement_history,omitempty"`
}

// codexPlant pairs a CodexRow with the JSONL bytes it describes, so
// codexPlants can build both together and MaterializeCodex only has to write
// what it is handed.
type codexPlant struct {
	row  CodexRow
	body []byte
}

// codexThread ids, one per quirk row. Shaped like a UUID because a real Codex
// thread id is one; the value itself carries no meaning.
const (
	codexThreadPlain      = "c0dec001-0000-4000-8000-000000000001"
	codexThreadEventDup   = "c0dec001-0000-4000-8000-000000000002"
	codexThreadCompacted  = "c0dec001-0000-4000-8000-000000000003"
	codexThreadNoMeta     = "c0dec001-0000-4000-8000-000000000004"
	codexThreadSubagent   = "c0dec001-0000-4000-8000-000000000005"
	codexThreadZstd       = "c0dec001-0000-4000-8000-000000000006"
	codexThreadRepoID     = "c0dec001-0000-4000-8000-000000000007"
	codexThreadReasoning  = "c0dec001-0000-4000-8000-000000000008"
	codexOriginator       = "codex_cli_rs"
	codexCLIVersion       = "0.135.0"
	codexSourceCLI        = "cli"
	codexModelProviderOAI = "openai"
)

// codexPlants builds every quirk row and the bytes of its rollout file.
//
// ExpectedTurns on each row is stated here by hand against the records the
// row's builder writes below, following one fixed rule so a reader can check
// it by eye: a response_item counts as one turn when it is a message with
// non-empty content, a function_call, or a function_call_output; event_msg
// records never count, because they restate a response_item that already
// does; a reasoning item never counts, because encrypted_content carries no
// readable text regardless of what summary holds; a compacted record counts
// its summary message only — replacement_history holds the pre-compaction
// turns the summary replaced, already archived from their own earlier
// response_item records, so counting them again would archive that content
// twice, and they never count; session_meta, turn_context and every other
// envelope type carry no text and never count.
func codexPlants(scratch string) []codexPlant {
	return []codexPlant{
		codexPlainRow(),
		codexEventMsgDuplicateRow(),
		codexCompactedRow(),
		codexMissingSessionMetaRow(),
		codexSubagentRow(),
		codexZstdOpaqueRow(),
		codexRepoIdentityRow(scratch),
		codexEncryptedReasoningRow(),
	}
}

// codexPath lays a rollout out the way Codex itself does: dated by day, one
// file per thread.
func codexPath(day time.Time, threadID string) string {
	return filepath.Join("sessions", day.Format("2006"), day.Format("01"), day.Format("02"),
		"rollout-"+day.UTC().Format("2006-01-02T15-04-05")+"-"+threadID+".jsonl")
}

func codexTS(day time.Time, offset time.Duration) string {
	return day.Add(offset).UTC().Format(time.RFC3339Nano)
}

func codexLine(ts, typ string, payload any) []byte {
	body, err := json.Marshal(payload)
	if err != nil {
		panic("fixtures: a generated codex payload does not marshal: " + err.Error())
	}
	line, err := json.Marshal(codexRecord{Timestamp: ts, Type: typ, Payload: body})
	if err != nil {
		panic("fixtures: a generated codex record does not marshal: " + err.Error())
	}
	return append(line, '\n')
}

func codexSessionMetaLine(ts string, meta codexSessionMeta) []byte {
	return codexLine(ts, "session_meta", meta)
}

func codexMessageLine(ts, role, text string) []byte {
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}
	item := codexMessageItem{Type: "message", Role: role, Content: []codexContentPart{{Type: contentType, Text: text}}}
	return codexLine(ts, "response_item", item)
}

func codexFunctionCallLine(ts, name, arguments, callID string) []byte {
	item := codexFunctionCallItem{Type: "function_call", Name: name, Arguments: arguments, CallID: callID}
	return codexLine(ts, "response_item", item)
}

func codexFunctionCallOutputLine(ts, callID, output string) []byte {
	item := codexFunctionCallOutputItem{Type: "function_call_output", CallID: callID, Output: output}
	return codexLine(ts, "response_item", item)
}

// codexPlainRow: session_meta, a user/assistant exchange, and one tool
// call/result pair. Turns: message(user) + message(assistant) +
// function_call + function_call_output = 4. The assistant message carries
// NeedleCodexConversation, the only planted token in either corpus, so a hit
// on it proves a search reached the Codex store and not claude-code's.
func codexPlainRow() codexPlant {
	day := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	cwd := "/workspace/codex-plain"
	var body []byte
	body = append(body, codexSessionMetaLine(codexTS(day, 0), codexSessionMeta{
		ID: codexThreadPlain, Timestamp: codexTS(day, 0), CWD: cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
	})...)
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user",
		"please add a health check endpoint.")...)
	body = append(body, codexMessageLine(codexTS(day, 10*time.Second), "assistant",
		"sure, adding the health check endpoint now, tracked as quenlaphor.")...)
	body = append(body, codexFunctionCallLine(codexTS(day, 15*time.Second),
		"shell", `{"command":["grep","-rn","health"]}`, "call_plain_1")...)
	body = append(body, codexFunctionCallOutputLine(codexTS(day, 20*time.Second),
		"call_plain_1", "main.go:12: healthHandler")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkPlain, File: codexPath(day, codexThreadPlain),
			ThreadID: codexThreadPlain, CWD: cwd, ExpectedTurns: 4,
		},
		body: body,
	}
}

// codexEventMsgDuplicateRow: a user message restated by an event_msg record.
// Turns: message(user) + message(assistant) = 2; the event_msg counts as 0,
// because it duplicates a response_item that already counted.
func codexEventMsgDuplicateRow() codexPlant {
	day := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	cwd := "/workspace/codex-event-dup"
	const text = "let's rename the config loader."
	var body []byte
	body = append(body, codexSessionMetaLine(codexTS(day, 0), codexSessionMeta{
		ID: codexThreadEventDup, Timestamp: codexTS(day, 0), CWD: cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
	})...)
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user", text)...)
	body = append(body, codexLine(codexTS(day, 6*time.Second), "event_msg",
		codexEventMsgUser{Type: "user_message", Message: text})...)
	body = append(body, codexMessageLine(codexTS(day, 10*time.Second), "assistant",
		"renamed config loader to configLoader.")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkEventMsgDuplicate, File: codexPath(day, codexThreadEventDup),
			ThreadID: codexThreadEventDup, CWD: cwd, ExpectedTurns: 2,
		},
		body: body,
	}
}

// codexCompactedRow: a compacted record between two ordinary messages. Turns:
// message(user) + [compacted: summary only, replacement_history skipped] +
// message(assistant) = 1 + 1 + 1 = 3.
func codexCompactedRow() codexPlant {
	day := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	cwd := "/workspace/codex-compacted"
	var body []byte
	body = append(body, codexSessionMetaLine(codexTS(day, 0), codexSessionMeta{
		ID: codexThreadCompacted, Timestamp: codexTS(day, 0), CWD: cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
	})...)
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user", "before compaction.")...)

	history := codexMessageItem{Type: "message", Role: "user",
		Content: []codexContentPart{{Type: "input_text", Text: "renamed config loader to configLoader, as discussed before compaction."}}}
	historyBytes, err := json.Marshal(history)
	if err != nil {
		panic("fixtures: a generated codex replacement_history item does not marshal: " + err.Error())
	}
	body = append(body, codexLine(codexTS(day, 10*time.Second), "compacted", codexCompacted{
		Message:            "conversation summarized: renamed config loader.",
		ReplacementHistory: []json.RawMessage{historyBytes},
	})...)
	body = append(body, codexMessageLine(codexTS(day, 15*time.Second), "assistant", "continuing after compaction.")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkCompacted, File: codexPath(day, codexThreadCompacted),
			ThreadID: codexThreadCompacted, CWD: cwd, ExpectedTurns: 3,
		},
		body: body,
	}
}

// codexMissingSessionMetaRow: a truncated head with no session_meta record at
// all, so the thread id can only come from the file name. Turns:
// message(user) + message(assistant) = 2.
func codexMissingSessionMetaRow() codexPlant {
	day := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	var body []byte
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user",
		"the file no longer needs the header.")...)
	body = append(body, codexMessageLine(codexTS(day, 10*time.Second), "assistant",
		"removed the header, thanks for the reminder.")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkMissingSessionMeta, File: codexPath(day, codexThreadNoMeta),
			ThreadID: codexThreadNoMeta, CWD: "", ExpectedTurns: 2,
		},
		body: body,
	}
}

// codexSubagentRow: session_meta carries agent_nickname. Turns:
// message(user) + message(assistant) = 2.
func codexSubagentRow() codexPlant {
	day := time.Date(2026, 6, 5, 9, 0, 0, 0, time.UTC)
	cwd := "/workspace/codex-subagent"
	var body []byte
	body = append(body, codexSessionMetaLine(codexTS(day, 0), codexSessionMeta{
		ID: codexThreadSubagent, Timestamp: codexTS(day, 0), CWD: cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
		AgentNickname: "reviewer-alpha",
	})...)
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user",
		"please review this diff for style issues.")...)
	body = append(body, codexMessageLine(codexTS(day, 10*time.Second), "assistant",
		"the diff looks fine, one nit about naming.")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkSubagent, File: codexPath(day, codexThreadSubagent),
			ThreadID: codexThreadSubagent, CWD: cwd, ExpectedTurns: 2,
		},
		body: body,
	}
}

// codexZstdOpaqueRow: a .jsonl.zst file. Its bytes are not JSONL at all —
// opaque, and never decompressed by anything in this repository — so it
// carries no session_meta and no turns; it exists to be enumerated and
// skipped. The bytes below open with zstd's magic number so a reader that
// sniffs the format sees a real zstd frame header, not JSON.
func codexZstdOpaqueRow() codexPlant {
	day := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)
	body := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x58, 0x19, 0x00, 0x00}
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkZstdOpaque, File: codexPath(day, codexThreadZstd) + ".zst",
			ThreadID: codexThreadZstd, CWD: "", Opaque: true, ExpectedTurns: 0,
		},
		body: body,
	}
}

// codexRepoIdentityRow: cwd is the same git scratch shape Materialize builds
// (ScratchNormal), so repo resolution against it must land on the identity a
// Claude Code session under that checkout resolves to (RepoRemote, OriginURL).
// Turns: message(user) + message(assistant) = 2.
func codexRepoIdentityRow(scratch string) codexPlant {
	day := time.Date(2026, 6, 7, 9, 0, 0, 0, time.UTC)
	cwd := filepath.Join(scratch, ScratchNormal)
	var body []byte
	body = append(body, codexSessionMetaLine(codexTS(day, 0), codexSessionMeta{
		ID: codexThreadRepoID, Timestamp: codexTS(day, 0), CWD: cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
		Git: &codexGit{CommitHash: "f44894bb", Branch: "main", RepositoryURL: OriginURL},
	})...)
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user",
		"confirm this checkout's remote before pushing.")...)
	body = append(body, codexMessageLine(codexTS(day, 10*time.Second), "assistant",
		"confirmed, origin points at the acme repo.")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkRepoIdentity, File: codexPath(day, codexThreadRepoID),
			ThreadID: codexThreadRepoID, CWD: cwd, ExpectedTurns: 2,
		},
		body: body,
	}
}

// codexEncryptedReasoningRow: a reasoning item with encrypted_content and an
// empty summary, between two ordinary messages. Turns: message(user) +
// message(assistant) = 2; the reasoning item counts as 0 because reasoning
// items never count, regardless of summary — encrypted_content and an empty
// summary just make this row a realistic instance of that case.
func codexEncryptedReasoningRow() codexPlant {
	day := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)
	cwd := "/workspace/codex-reasoning"
	var body []byte
	body = append(body, codexSessionMetaLine(codexTS(day, 0), codexSessionMeta{
		ID: codexThreadReasoning, Timestamp: codexTS(day, 0), CWD: cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
	})...)
	body = append(body, codexMessageLine(codexTS(day, 5*time.Second), "user",
		"why did the last build fail?")...)
	body = append(body, codexLine(codexTS(day, 8*time.Second), "response_item", codexReasoningItem{
		Type: "reasoning", Summary: []codexContentPart{}, EncryptedContent: "b64:9f2c7a1e5d0834f6",
	})...)
	body = append(body, codexMessageLine(codexTS(day, 10*time.Second), "assistant",
		"the build failed because a dependency was missing.")...)
	return codexPlant{
		row: CodexRow{
			Quirk: CodexQuirkEncryptedReasoning, File: codexPath(day, codexThreadReasoning),
			ThreadID: codexThreadReasoning, CWD: cwd, ExpectedTurns: 2,
		},
		body: body,
	}
}
