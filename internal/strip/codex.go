package strip

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// Codex envelope types this decoder acts on. Every other type in
// jsonl.KnownCodexType carries no words and yields no turn.
const (
	codexSessionMeta  = "session_meta"
	codexResponseItem = "response_item"
	codexEventMsg     = "event_msg"
	codexCompacted    = "compacted"
)

// response_item payload types this decoder acts on.
const (
	codexMessage            = "message"
	codexFunctionCall       = "function_call"
	codexFunctionCallOutput = "function_call_output"
	codexReasoning          = "reasoning"
)

// rolloutPrefix opens the name of every rollout file Codex writes:
// rollout-<ISO timestamp>-<thread id>.jsonl.
const rolloutPrefix = "rollout-"

// CodexProvider is Codex CLI's rollout store seen as a Provider. It holds only
// what `recall doctor` reports, under atomics and one mutex for the rare
// branches, because a corpus walk reaches one provider from every worker while
// each file gets a decoder of its own.
type CodexProvider struct {
	lines      atomic.Int64
	untyped    atomic.Int64
	telemetry  atomic.Int64
	replaced   atomic.Int64
	compressed atomic.Int64

	mu       sync.Mutex
	unknown  map[string]int
	payloads map[string]int
}

// Codex returns a provider that has decoded nothing yet.
func Codex() *CodexProvider { return &CodexProvider{} }

// CodexObservation is what every rollout this provider has decoded held, for
// `recall doctor` to report.
type CodexObservation struct {
	// Tally counts every record handed to a decoder, including the envelope
	// types this build does not recognise.
	Tally jsonl.Tally

	// UnknownPayloads counts response_item payload types this build does not
	// recognise, which the envelope tally cannot see: they all arrive under
	// the one known envelope type.
	UnknownPayloads map[string]int

	// Telemetry counts event_msg records. They restate conversation a
	// response_item already carries, so the count is the size of the
	// double-count a naive reader would have made.
	Telemetry int

	// Replaced counts the replacement_history items compacted records carried.
	// Those turns were archived from their own earlier records, so the count
	// is what was deliberately not archived twice.
	Replaced int

	// Compressed counts rollout files Codex has rewritten to .jsonl.zst.
	// Nothing here decompresses zstd, so they are unread, and doctor can only
	// declare that if the provider counts them as it skips them.
	Compressed int
}

// Agent is codex's vocabulary entry.
func (p *CodexProvider) Agent() schema.Agent { return schema.AgentCodex }

// Root is Codex CLI's rollout store: $CODEX_HOME/sessions, else
// ~/.codex/sessions.
func (p *CodexProvider) Root() (string, error) {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the home directory: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// IsTranscript reports whether rel names a plain rollout. A cold rollout Codex
// has rewritten to rollout-*.jsonl.zst is counted here and left out of the
// walk: nothing in recall decompresses zstd, so handing one to a JSONL reader
// would report a file of malformed lines rather than the unread file it is.
func (p *CodexProvider) IsTranscript(rel string) bool {
	base := filepath.Base(rel)
	if !strings.HasPrefix(base, rolloutPrefix) {
		return false
	}
	if strings.HasSuffix(base, ".jsonl") {
		return true
	}
	if strings.HasSuffix(base, ".jsonl.zst") {
		p.compressed.Add(1)
	}
	return false
}

// NeedsHead is true: a rollout's thread id, cwd and branch live in its
// session_meta record and nowhere in its path, so a read that starts at a byte
// cursor cannot say whose session the rest of the file is.
func (p *CodexProvider) NeedsHead() bool { return true }

// Decoder returns the decoder for one rollout, named relative to Root. It is
// per file and stateful: a rollout's identity arrives once, in a record the
// rest of the file is read against.
func (p *CodexProvider) Decoder(rel string) archiveDecoder {
	return &codexDecoder{provider: p, thread: threadIDFromName(filepath.Base(rel))}
}

// Observation is what every rollout this provider has decoded held.
func (p *CodexProvider) Observation() CodexObservation {
	o := CodexObservation{
		Tally: jsonl.Tally{
			Lines:   int(p.lines.Load()),
			Untyped: int(p.untyped.Load()),
		},
		Telemetry:  int(p.telemetry.Load()),
		Replaced:   int(p.replaced.Load()),
		Compressed: int(p.compressed.Load()),
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for typ, n := range p.unknown {
		if o.Tally.Unknown == nil {
			o.Tally.Unknown = make(map[string]int, len(p.unknown))
		}
		o.Tally.Unknown[typ] = n
	}
	for typ, n := range p.payloads {
		if o.UnknownPayloads == nil {
			o.UnknownPayloads = make(map[string]int, len(p.payloads))
		}
		o.UnknownPayloads[typ] = n
	}
	return o
}

// observe applies the counting rule to an envelope type. It cannot reuse
// jsonl.Tally.ObserveType, which judges a type against Claude Code's catalog:
// every Codex type would land in the unknown map.
func (p *CodexProvider) observe(typ string) {
	p.lines.Add(1)
	if typ == "" {
		p.untyped.Add(1)
		return
	}
	if jsonl.KnownCodexType(typ) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unknown == nil {
		p.unknown = map[string]int{}
	}
	p.unknown[typ]++
}

func (p *CodexProvider) observePayload(typ string) {
	if typ == "" || jsonl.KnownCodexPayloadType(typ) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.payloads == nil {
		p.payloads = map[string]int{}
	}
	p.payloads[typ]++
}

// codexDecoder reads one rollout. Every field here comes from session_meta or
// the file's name, which is why the provider needs the head of a resumed file:
// the records after it carry no identity of their own.
type codexDecoder struct {
	provider *CodexProvider
	thread   string
	cwd      string
	branch   string
	nickname string
}

// codexPart is the one turn a record yields, empty text meaning none.
type codexPart struct {
	tier   schema.Tier
	author schema.Author
	text   string
}

// Turns decodes one rollout record. Discrimination is two-level, as it is for
// Claude Code: the envelope type first, then the payload's own type.
func (d *codexDecoder) Turns(rec jsonl.Record) ([]schema.Turn, bool) {
	typ, payload, ok := rec.CodexEnvelope()
	d.provider.observe(typ)
	if !ok {
		return nil, false
	}

	switch typ {
	case codexSessionMeta:
		d.absorb(payload)
		return nil, false
	case codexResponseItem:
		return d.turn(rec, d.responseItem(payload))
	case codexCompacted:
		// replacement_history holds the turns the summary replaced. They were
		// archived from their own earlier response_item records, so archiving
		// them again would put the same words in the store twice.
		d.provider.replaced.Add(payload.Get("replacement_history.#").Int())
		return d.turn(rec, codexPart{schema.TierConversation, schema.AuthorSystem, payload.Get("message").String()})
	case codexEventMsg:
		// The UI event stream restates conversation a response_item already
		// carries; reading both shows every user turn twice.
		d.provider.telemetry.Add(1)
		return nil, false
	}
	return nil, false
}

func (d *codexDecoder) responseItem(payload jsonl.Value) codexPart {
	typ := payload.Get("type").String()
	switch typ {
	case codexMessage:
		return codexPart{schema.TierConversation, codexAuthor(payload.Get("role").String()), codexText(payload)}
	case codexFunctionCall:
		// The arguments are the payload the invocation tier exists to leave
		// behind: whole file bodies and command scripts, none of it words.
		return codexPart{schema.TierInvocation, schema.AuthorAssistant, codexSignature(payload)}
	case codexFunctionCallOutput:
		return codexPart{schema.TierResult, schema.AuthorSystem, payload.Get("output").String()}
	case codexReasoning:
		// encrypted_content is the model's thinking and is not readable, and
		// the summary beside it is empty, so there is nothing to search.
		return codexPart{}
	}
	d.provider.observePayload(typ)
	return codexPart{}
}

// turn stamps the rollout's identity onto one part. A rollout whose
// session_meta names a subagent attributes every turn to that agent, however
// the record's own role reads — the same order Claude Code's rule uses, where
// a sidechain record outranks the role it speaks in.
func (d *codexDecoder) turn(rec jsonl.Record, p codexPart) ([]schema.Turn, bool) {
	if p.text == "" {
		return nil, false
	}
	author := p.author
	if d.nickname != "" {
		author = schema.AuthorAgent
	}
	return []schema.Turn{{
		Session: d.thread,
		UUID:    codexUUID(rec),
		// Verbatim: Codex writes RFC3339 with a Z, the shape Claude Code writes,
		// so the archive's string ordering holds across both stores.
		TS:     rec.Get("timestamp").String(),
		Tier:   p.tier,
		Author: author,
		Agent:  d.nickname,
		Branch: d.branch,
		CWD:    d.cwd,
		Text:   p.text,
	}}, true
}

// absorb takes the rollout's identity from session_meta. A field is only
// overwritten when the record carries one, so priming a resumed read with the
// head cannot blank what a later record already established.
func (d *codexDecoder) absorb(payload jsonl.Value) {
	if id := payload.Get("id").String(); id != "" {
		d.thread = id
	}
	if cwd := payload.Get("cwd").String(); cwd != "" {
		d.cwd = cwd
	}
	if branch := payload.Get("git.branch").String(); branch != "" {
		d.branch = branch
	}
	if nickname := payload.Get("agent_nickname").String(); nickname != "" {
		d.nickname = nickname
	}
}

// codexAuthor attributes a message by its role. developer and system are the
// prompts the harness sends on the operator's behalf, which is what
// AuthorSystem means: searchable, never attributed to the human.
func codexAuthor(role string) schema.Author {
	switch role {
	case roleUser:
		return schema.AuthorHuman
	case roleAssistant:
		return schema.AuthorAssistant
	}
	return schema.AuthorSystem
}

// codexText is a message payload's readable content. Blocks are addressed by
// index rather than fetched whole, matching parts.gather: the text is the only
// part of a content array worth copying.
func codexText(payload jsonl.Value) string {
	n := payload.Get("content.#")
	if !n.Exists() {
		return payload.Get("content").String()
	}
	var texts []string
	for i := range int(n.Int()) {
		if t := payload.Get("content." + strconv.Itoa(i) + ".text").String(); t != "" {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n\n")
}

// codexSignature identifies a tool call without its arguments: the name, and
// the call_id its separate function_call_output record carries back.
func codexSignature(payload jsonl.Value) string {
	name := payload.Get("name").String()
	if id := payload.Get("call_id").String(); id != "" && name != "" {
		return name + " " + id
	}
	return name
}

// codexUUID synthesises the dedup key Codex does not write. The archive
// deduplicates on session plus uuid, and a rollout record carries no id of its
// own, so both halves are load-bearing: the position alone collapses distinct
// content in a fork that restarts numbering, and the content hash alone
// collapses a genuinely repeated message.
//
// The position is the record's byte offset in the file rather than a count of
// the records this decoder has been handed. A resumed read is primed with the
// file's head and then starts at a byte cursor, so a counter would number the
// same record differently on an appending pass than on a whole re-read, and a
// later rebuild would keep both numberings as two copies of one turn.
func codexUUID(rec jsonl.Record) string {
	h := fnv.New64a()
	_, _ = h.Write(rec.Raw())
	return fmt.Sprintf("%d-%016x", rec.Offset, h.Sum64())
}

// threadIDFromName recovers the thread id from a rollout's name, which is
// where it is legible when the head holding session_meta was truncated away.
// The name is rollout-<ISO timestamp>-<thread id>.jsonl, and a forked thread
// appends a second id after an underscore.
func threadIDFromName(base string) string {
	name, ok := strings.CutPrefix(strings.TrimSuffix(base, ".jsonl"), rolloutPrefix)
	if !ok {
		return ""
	}
	if i := strings.IndexByte(name, '_'); i >= 0 {
		name = name[:i]
	}
	if len(name) < uuidLen {
		return ""
	}
	// The timestamp before it is dash-separated too, so the id is found by its
	// shape rather than by counting the fields of a format that has changed.
	tail := name[len(name)-uuidLen:]
	if !uuidShaped(tail) {
		return ""
	}
	return tail
}

const uuidLen = 36

func uuidShaped(s string) bool {
	for i := range uuidLen {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHex(s[i]) {
				return false
			}
		}
	}
	return true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
