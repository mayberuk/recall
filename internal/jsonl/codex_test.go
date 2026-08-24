package jsonl

import (
	"reflect"
	"sort"
	"testing"
)

func TestCodexEnvelopeSplitsTheThreeKeyRecord(t *testing.T) {
	rec := parse(t, `{"timestamp":"2026-06-01T09:00:05Z","type":"response_item",`+
		`"payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}`)

	typ, payload, ok := rec.CodexEnvelope()
	if !ok {
		t.Fatal("CodexEnvelope reported no type on a well-formed envelope")
	}
	if want := "response_item"; typ != want {
		t.Errorf("type = %q, want %q", typ, want)
	}
	if got, want := payload.Get("type").String(), "message"; got != want {
		t.Errorf("payload.type = %q, want %q", got, want)
	}
	if got, want := payload.Get("content.0.text").String(), "hello"; got != want {
		t.Errorf("payload.content.0.text = %q, want %q", got, want)
	}
}

// TestCodexEnvelopeAcceptsATypedRecordWithNoPayload pins what ok answers for.
// An envelope type recall has never seen may carry no payload this build can
// address, and reporting it as unusable would lose the one thing doctor needs
// from it: that a record of that type was there.
func TestCodexEnvelopeAcceptsATypedRecordWithNoPayload(t *testing.T) {
	rec := parse(t, `{"timestamp":"2026-06-01T09:00:05Z","type":"world_state"}`)

	typ, payload, ok := rec.CodexEnvelope()
	if !ok {
		t.Fatal("CodexEnvelope reported no type on a record that carries one")
	}
	if want := "world_state"; typ != want {
		t.Errorf("type = %q, want %q", typ, want)
	}
	if payload.Exists() {
		t.Errorf("payload.Exists() = true on a record with no payload key, raw %q", payload.Raw())
	}
}

func TestCodexEnvelopeRejectsARecordWithNoType(t *testing.T) {
	rec := parse(t, `{"timestamp":"2026-06-01T09:00:05Z","payload":{"type":"message"}}`)

	typ, _, ok := rec.CodexEnvelope()
	if ok {
		t.Errorf("CodexEnvelope reported ok on a record with no type, type = %q", typ)
	}
	if typ != "" {
		t.Errorf("type = %q, want empty", typ)
	}
}

// TestKnownCodexTypeCoversTheDocumentedCatalog pins the exact set. A type
// quietly added here would stop being counted as unknown, which is the only
// way doctor learns that Codex has grown a record kind recall does not read.
func TestKnownCodexTypeCoversTheDocumentedCatalog(t *testing.T) {
	want := []string{
		"compacted", "event_msg", "inter_agent_communication",
		"inter_agent_communication_metadata", "response_item",
		"security_risk_score", "session_meta", "turn_context", "world_state",
	}
	got := make([]string, 0, len(knownCodexTypes))
	for typ := range knownCodexTypes {
		got = append(got, typ)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("knownCodexTypes = %v, want %v", got, want)
	}
	for _, typ := range want {
		if !KnownCodexType(typ) {
			t.Errorf("KnownCodexType(%q) = false", typ)
		}
	}
	for _, typ := range []string{"", "rollout", "thread_migration_marker"} {
		if KnownCodexType(typ) {
			t.Errorf("KnownCodexType(%q) = true, want false for a type no release has written", typ)
		}
	}
}

// TestTheTwoCatalogsStaySeparate pins that Codex's types were added beside
// Claude Code's rather than into them: one map judging both stores would count
// every Codex record as unknown, or stop counting an unknown Claude one.
func TestTheTwoCatalogsStaySeparate(t *testing.T) {
	for _, typ := range []string{"session_meta", "response_item", "event_msg", "compacted"} {
		if KnownType(typ) {
			t.Errorf("KnownType(%q) = true: a Codex envelope type leaked into the Claude catalog", typ)
		}
	}
	for _, typ := range []string{"assistant", "user", "attachment", "system"} {
		if KnownCodexType(typ) {
			t.Errorf("KnownCodexType(%q) = true: a Claude record type leaked into the Codex catalog", typ)
		}
	}
}

func TestKnownCodexPayloadTypeCoversTheResponseItemCatalog(t *testing.T) {
	want := []string{"function_call", "function_call_output", "message", "reasoning"}
	got := make([]string, 0, len(knownCodexPayloadTypes))
	for typ := range knownCodexPayloadTypes {
		got = append(got, typ)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("knownCodexPayloadTypes = %v, want %v", got, want)
	}
	// The event stream's own payload types are not response_item payloads, and
	// treating them as known here would hide a real response_item this build
	// cannot read.
	for _, typ := range []string{"", "user_message", "agent_message", "local_shell_call"} {
		if KnownCodexPayloadType(typ) {
			t.Errorf("KnownCodexPayloadType(%q) = true, want false", typ)
		}
	}
}
