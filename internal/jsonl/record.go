// Package jsonl streams Claude Code transcripts and extracts fields from them.
//
// It is the only package in recall that imports gjson, which extracts one path
// lazily instead of materializing every line: 1.31 s over the 1.29 GB corpus
// against 7.21 s for encoding/json. The corpus spans 24 Claude Code versions and
// the entry format is documented as internal and changing, so every accessor here
// reports absence rather than failing on it.
package jsonl

import (
	"bytes"
	"sort"

	"github.com/tidwall/gjson"
)

// Value is a JSON value read out of a record. It is a copy, so it outlives the
// reader buffer the record came from.
type Value struct{ res gjson.Result }

// Exists reports whether the path was present and non-null. An absent field is
// normal across 24 versions and is never an error.
func (v Value) Exists() bool { return v.res.Exists() }

// String is the value as text, empty when absent.
func (v Value) String() string { return v.res.String() }

// Int is the value as a whole number, zero when absent.
func (v Value) Int() int64 { return v.res.Int() }

// Bool is the value as a boolean, false when absent.
func (v Value) Bool() bool { return v.res.Bool() }

// IsArray reports whether the value is a JSON array.
func (v Value) IsArray() bool { return v.res.IsArray() }

// IsObject reports whether the value is a JSON object.
func (v Value) IsObject() bool { return v.res.IsObject() }

// Raw is the value's original JSON text.
func (v Value) Raw() string { return v.res.Raw }

// Get reads a dotted path relative to this value.
func (v Value) Get(path string) Value { return Value{res: v.res.Get(path)} }

// Array is the value's elements, empty when the value is absent or not an array.
func (v Value) Array() []Value {
	src := v.res.Array()
	out := make([]Value, len(src))
	for i, r := range src {
		out[i] = Value{res: r}
	}
	return out
}

// Str reads a dotted path as text and reports whether it was present, for the
// fields where presence itself is the signal — promptSource being absent means
// the turn was machine-generated, not that the value is empty.
func (v Value) Str(path string) (string, bool) {
	r := v.res.Get(path)
	if !r.Exists() {
		return "", false
	}
	return r.String(), true
}

// Record is one parsed transcript entry.
//
// It borrows the reader's line buffer, so a Record is valid only until the next
// call to Reader.Next; every Value it yields is a copy and is not.
type Record struct {
	raw    []byte
	Offset int64
	Length int
}

// Parse decodes one line. ok is false when the line is not a JSON object.
//
// Rejecting a half-written line matters because gjson answers a path on
// malformed input without complaining. Each record is written by one append, so
// a partial line is always one cut off at the end, and an unclosed object is
// what that looks like. Scanning every line to prove validity instead costs
// 0.87 s over the 1.29 GB corpus against a 4 s cold-strip gate; ParseStrict is
// there for the pass that is worth paying it.
func Parse(line Line) (Record, bool) {
	b := bytes.TrimSpace(line.Bytes)
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return Record{}, false
	}
	return Record{raw: b, Offset: line.Offset, Length: line.Length}, true
}

// ParseStrict decodes one line, validating the whole of it. `recall doctor`
// uses it: proving the store is readable is exactly the job worth a full scan.
func ParseStrict(line Line) (Record, bool) {
	if !gjson.ValidBytes(bytes.TrimSpace(line.Bytes)) {
		return Record{}, false
	}
	return Parse(line)
}

// Get reads a dotted path from the record.
func (r Record) Get(path string) Value { return Value{res: gjson.GetBytes(r.raw, path)} }

// Str reads a dotted path as text and reports whether it was present.
func (r Record) Str(path string) (string, bool) {
	res := gjson.GetBytes(r.raw, path)
	if !res.Exists() {
		return "", false
	}
	return res.String(), true
}

// Raw is the record's original bytes, valid until the next Reader.Next.
func (r Record) Raw() []byte { return r.raw }

// Type is the record's `type`, empty when absent.
func (r Record) Type() string { return r.Get("type").String() }

// SessionID reads `sessionId`, falling back to the `session_id` spelling that
// some versions write instead. Both appear on the same record in the corpus.
func (r Record) SessionID() string {
	if s, ok := r.Str("sessionId"); ok && s != "" {
		return s
	}
	return r.Get("session_id").String()
}

// UUID is the record's dedup key: 9,473 uuids appear in more than one file.
func (r Record) UUID() string { return r.Get("uuid").String() }

// Timestamp is the record's RFC3339 `timestamp`, empty when absent.
func (r Record) Timestamp() string { return r.Get("timestamp").String() }

// CWD is the raw `cwd`. A relocated record carries no cwd; see RelocatedCWD.
func (r Record) CWD() string { return r.Get("cwd").String() }

// RelocatedCWD is the `relocatedCwd` of a relocated record. It is kept separate
// from CWD so repo resolution can tell a moved session from an ordinary one.
func (r Record) RelocatedCWD() string { return r.Get("relocatedCwd").String() }

// GitBranch is the record's `gitBranch`, empty when absent.
func (r Record) GitBranch() string { return r.Get("gitBranch").String() }

// Version is the Claude Code version that wrote the record, empty when absent.
func (r Record) Version() string { return r.Get("version").String() }

// IsSidechain reports subagent authorship. It is present on only the four
// conversational record types; the other sixteen have no author, so absent
// reads as false.
func (r Record) IsSidechain() bool { return r.Get("isSidechain").Bool() }

// AgentID is the `agentId`, present on 43-46% of user and assistant records and
// swinging 0-83% by version, so callers treat it as a refinement not a key.
func (r Record) AgentID() string { return r.Get("agentId").String() }

// PromptSource reads `promptSource` and reports its presence. Presence is the
// human-turn discriminator: `typed` marks a prompt the human typed, and
// absence is meaningful rather than missing data.
func (r Record) PromptSource() (string, bool) { return r.Str("promptSource") }

// Message is the record's `message` object.
func (r Record) Message() Value { return r.Get("message") }

// knownTypes is the twenty record types measured across the whole corpus. A type
// outside it is counted by Tally, never dropped: the format is documented as
// changing between releases, and silently ignoring an unrecognised record is how
// a false negative enters through the back door.
var knownTypes = map[string]bool{
	"assistant": true, "user": true, "attachment": true, "system": true,
	"last-prompt": true, "mode": true, "permission-mode": true, "ai-title": true,
	"custom-title": true, "queue-operation": true, "pr-link": true,
	"file-history-snapshot": true, "file-history-delta": true, "agent-name": true,
	"bridge-session": true, "worktree-state": true, "relocated": true,
	"frame-link": true, "started": true, "result": true,
}

// KnownType reports whether this build has seen the record type before.
func KnownType(t string) bool { return knownTypes[t] }

// TypeCount is one unknown record type and how often it appeared.
type TypeCount struct {
	Type  string
	Count int
}

// Tally is what a scan saw, aggregated so `recall doctor` can report it. An
// unrecognised record type is a countable observation rather than a log line,
// because a log line is not something a later command can assert on.
type Tally struct {
	Lines     int
	Malformed int
	Untyped   int
	Unknown   map[string]int
}

// Observe counts one parsed record.
func (t *Tally) Observe(rec Record) { t.ObserveType(rec.Header().Type) }

// ObserveType counts one parsed record whose type the caller already read, so a
// pass that parsed a Header does not scan the record again to tally it. It is
// the one counting rule; Observe reaches it too.
func (t *Tally) ObserveType(typ string) {
	t.Lines++
	if typ == "" {
		t.Untyped++
		return
	}
	if knownTypes[typ] {
		return
	}
	if t.Unknown == nil {
		t.Unknown = map[string]int{}
	}
	t.Unknown[typ]++
}

// ObserveMalformed counts a line that was not a JSON object.
func (t *Tally) ObserveMalformed() {
	t.Lines++
	t.Malformed++
}

// Merge folds another tally into this one, so per-file scans aggregate.
func (t *Tally) Merge(o Tally) {
	t.Lines += o.Lines
	t.Malformed += o.Malformed
	t.Untyped += o.Untyped
	for typ, n := range o.Unknown {
		if t.Unknown == nil {
			t.Unknown = map[string]int{}
		}
		t.Unknown[typ] += n
	}
}

// UnknownTotal is how many records carried a type this build does not know.
func (t *Tally) UnknownTotal() int {
	n := 0
	for _, c := range t.Unknown {
		n += c
	}
	return n
}

// UnknownCounts lists the unknown types, commonest first and ties broken by
// name so doctor's output is stable between runs.
func (t *Tally) UnknownCounts() []TypeCount {
	out := make([]TypeCount, 0, len(t.Unknown))
	for typ, n := range t.Unknown {
		out = append(out, TypeCount{Type: typ, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type < out[j].Type
	})
	return out
}
