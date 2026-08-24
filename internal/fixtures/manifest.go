// Package fixtures materializes the shared synthetic corpus for tests.
//
// Every package tests against this one corpus rather than inventing a private
// fixture for shared behaviour. Expected values derive from Manifest, never
// from running the code under test — a test that hardcodes what its own code
// produced proves nothing.
package fixtures

// Scratch roots, relative to Corpus.Scratch. The gone shape is deliberately
// never created: a relocated record points at a checkout no longer on disk.
const (
	ScratchNormal     = "normal"
	ScratchAndroid    = "normal/android"
	ScratchOrphan     = "normal/.claude/worktrees/orphan"
	ScratchRemoteless = "remoteless"
	ScratchGone       = "gone"
)

// OriginURL is the remote the normal repo, its subdirectory, and its orphaned
// worktree must all resolve to. The host is reserved as unresolvable so nothing
// can reach it.
const OriginURL = "https://example.invalid/acme/normal.git"

// Repo identities a cwd resolves to. RepoNoRemote is keyed by toplevel path and
// is a distinct identity, never "unresolved".
const (
	RepoRemote   = "remote"
	RepoNoRemote = "repo, no remote"
	RepoNone     = "outside any repo"
)

// Session ids planted in the corpus.
const (
	SessNeedle         = "3f9c1d20-4b7e-4a51-9c02-1d6e8b40a771"
	SessDup            = "7a41e5b8-2c93-4f16-b8de-05c7a91e2d64"
	SessMultiFirst     = "c58d0e12-9f34-4b7a-8e51-2a6c3d90f4b8"
	SessMultiSecond    = "e21b7c46-8d05-4e93-a712-9f4b6c81d305"
	SessOrphan         = "9d6f2a13-5e80-4c47-b93a-71e5d8206c4f"
	SessRemoteless     = "b47c8e91-0a26-4d5f-8712-3c9e6b54a0d8"
	SessSubdir         = "1e83f5c7-6b40-49d2-a8e1-5f70c2946b3a"
	SessRelocated      = "4c72d908-3f61-4a85-9b0c-8e26d71f5a49"
	SessSkew           = "8b05a3e7-1d94-4f62-8c37-6e50b9a24d1f"
	SessUnknownType    = "2a6e4b83-7c15-4d09-b64e-91f30d8a5726"
	SessNoPromptSource = "6d19c7f4-8a52-4e63-91b0-7c48e2d60395"
	SessEmptyThinking  = "5f30b8d6-2e47-4915-a7c8-0b61f94e3d27"
	SessHugeResult     = "0c94e716-5b83-4a27-8d51-2e63fa085b41"
)

// Corpus-relative paths, one per row of the shared-fixtures table. The store
// layout is <project-dir>/<sessionId>.jsonl, so the table's names live here
// rather than on disk; tests/fixtures/corpus/README.md carries the mapping.
const (
	FileNeedle         = "-scratch-normal/" + SessNeedle + ".jsonl"
	FileSubagent       = "-scratch-normal/" + SessNeedle + "/subagents/agent-b1c2d3e4f50617289.jsonl"
	FileDupA           = "-scratch-normal/" + SessDup + ".jsonl"
	FileDupB           = "-scratch-normal-android/" + SessDup + ".jsonl"
	FileMultiSession   = "-scratch-normal/" + SessMultiFirst + ".jsonl"
	FileUnknownType    = "-scratch-normal/" + SessUnknownType + ".jsonl"
	FileNoPromptSource = "-scratch-normal/" + SessNoPromptSource + ".jsonl"
	FileEmptyThinking  = "-scratch-normal/" + SessEmptyThinking + ".jsonl"
	FileHugeResult     = "-scratch-normal/" + SessHugeResult + ".jsonl"
	FileOrphan         = "-scratch-normal--claude-worktrees-orphan/" + SessOrphan + ".jsonl"
	FileRemoteless     = "-scratch-remoteless/" + SessRemoteless + ".jsonl"
	FileSubdir         = "-scratch-normal-android/" + SessSubdir + ".jsonl"
	FileRelocated      = "-scratch-gone/" + SessRelocated + ".jsonl"
	FileSkew           = "-scratch-mtime-skew/" + SessSkew + ".jsonl"
)

// Tier names from the pinned stripped-turn schema.
const (
	TierConversation = "conversation"
	TierInvocation   = "invocation"
	TierResult       = "result"
)

// Needle is a token planted in exactly one record, for the property test that
// every planted token is returned by find.
type Needle struct {
	Token   string
	Session string
	UUID    string
	Tier    string
	Files   []string
}

// CWDShape is one cwd in the corpus and the repo identity it must resolve to.
type CWDShape struct {
	Name     string
	CWD      string
	Session  string
	Identity string
	Remote   string
	Toplevel string
}

// Manifest is what the corpus plants. Every expected value a test asserts
// comes from here, not from running the code under test.
type Manifest struct {
	Needles      []Needle
	DupUUIDs     []DupUUID
	CWDShapes    []CWDShape
	Sessions     []string
	SessionFiles int
	SubagentDirs []string

	// MultiSessionFile carries MultiSessionIDs in one file: 6 real files do.
	MultiSessionFile string
	MultiSessionIDs  []string

	// TypedTurns counts distinct uuids with promptSource == "typed".
	// TypedTurnRecords counts them before uuid dedup; the difference is the
	// duplicated record, so a count that skips dedup lands on the wrong number.
	TypedTurns       int
	TypedTurnRecords int
	// CommandArgTurns are slash-command records whose <command-args> carry typed
	// words. Args only, wrapper discarded.
	CommandArgTurns int
	// HumanTurns is TypedTurns + CommandArgTurns, the whole human-turn rule.
	HumanTurns int

	UnknownTypes    map[string]int
	HugeResultBytes int

	SkewFile      string
	SkewContentTS string
	SkewMTime     string
	SkewDays      int
}

// DupUUID is one record uuid present in more than one file.
type DupUUID struct {
	UUID    string
	Session string
	Files   []string
}

// CodexQuirk names the reason one row of the generated Codex corpus exists.
// internal/strip's Codex decoder has to survive every one of these.
type CodexQuirk string

const (
	// CodexQuirkPlain is an ordinary rollout: session_meta, a conversation
	// exchange, and one tool call/result pair.
	CodexQuirkPlain CodexQuirk = "plain"
	// CodexQuirkEventMsgDuplicate carries one user message as both a
	// response_item and an event_msg; only the response_item is a turn.
	CodexQuirkEventMsgDuplicate CodexQuirk = "event-msg-duplicate"
	// CodexQuirkCompacted carries a compacted record with a summary message
	// and a one-item replacement_history.
	CodexQuirkCompacted CodexQuirk = "compacted"
	// CodexQuirkMissingSessionMeta has no session_meta record at all, so the
	// thread id can only come from the file name.
	CodexQuirkMissingSessionMeta CodexQuirk = "missing-session-meta"
	// CodexQuirkSubagent carries agent_nickname in session_meta.
	CodexQuirkSubagent CodexQuirk = "subagent"
	// CodexQuirkZstdOpaque is a .jsonl.zst file: opaque bytes, never
	// decompressed, present only to be enumerated and skipped.
	CodexQuirkZstdOpaque CodexQuirk = "zstd-opaque"
	// CodexQuirkRepoIdentity has a cwd under the same git scratch shape
	// Materialize builds, so it resolves to the identity a Claude Code
	// session in that checkout resolves to.
	CodexQuirkRepoIdentity CodexQuirk = "repo-identity"
	// CodexQuirkEncryptedReasoning carries a reasoning item with
	// encrypted_content and an empty summary, which yields no turn.
	CodexQuirkEncryptedReasoning CodexQuirk = "encrypted-reasoning"
)

// CodexRow is one generated rollout file and what a decoder reading it must
// produce. ExpectedTurns is stated by hand against the records codex.go
// writes for this row, never read back from a decoder — see codex.go for the
// derivation of each value.
type CodexRow struct {
	Quirk    CodexQuirk
	File     string // Root-relative path to the rollout file
	ThreadID string
	// CWD is the cwd the row's session_meta carries, empty when the row plants
	// none (CodexQuirkMissingSessionMeta has no session_meta to carry one).
	CWD string
	// Opaque marks a file that must never be parsed as JSON at all
	// (CodexQuirkZstdOpaque); every other row is valid JSONL.
	Opaque        bool
	ExpectedTurns int
}

// CodexManifest describes the Codex corpus MaterializeCodex builds: one
// CodexRow per quirk internal/strip's Codex decoder must handle.
type CodexManifest struct {
	Rows []CodexRow
}
