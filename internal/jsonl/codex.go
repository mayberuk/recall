package jsonl

// CodexEnvelope reads the three-key envelope every Codex rollout line shares,
// {timestamp, type, payload}, and reports whether the line carried a type at
// all. A record with a type and no payload is still a typed record: ok answers
// for the discriminator alone, so a caller counts an unrecognised envelope by
// its type instead of discarding it as shapeless.
//
// Codex rollouts are read by path rather than through parseHeader: that scan
// exists for Claude Code's flat envelope and its key set, and would have to
// learn a second, disjoint one to serve this shape.
func (r Record) CodexEnvelope() (typ string, payload Value, ok bool) {
	t := r.Get("type")
	if !t.Exists() {
		return "", Value{}, false
	}
	return t.String(), r.Get("payload"), true
}

// knownCodexTypes is the envelope discriminator's current catalog. A type
// outside it is counted by its reader, never dropped: Codex is mid-migration to
// a paginated store and gains record kinds between releases, so an
// unrecognised envelope is expected rather than exceptional.
var knownCodexTypes = map[string]bool{
	"session_meta": true, "response_item": true, "event_msg": true,
	"turn_context": true, "compacted": true, "world_state": true,
	"security_risk_score": true, "inter_agent_communication": true,
	"inter_agent_communication_metadata": true,
}

// KnownCodexType reports whether this build has seen the envelope type before.
func KnownCodexType(t string) bool { return knownCodexTypes[t] }

// knownCodexPayloadTypes is the second half of Codex's two-level
// discrimination: the payload.type values a response_item carries.
var knownCodexPayloadTypes = map[string]bool{
	"message": true, "function_call": true,
	"function_call_output": true, "reasoning": true,
}

// KnownCodexPayloadType reports whether this build has seen a response_item's
// payload.type before.
func KnownCodexPayloadType(t string) bool { return knownCodexPayloadTypes[t] }
