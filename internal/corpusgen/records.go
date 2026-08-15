package corpusgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

// epoch dates every generated record. A wall clock in the write path would make
// two runs of one Spec differ, which is the property the whole package sells.
var epoch = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

const (
	sessionSpacing = 6 * time.Hour
	recordSpacing  = 7 * time.Second
	writerVersion  = "2.1.231"
)

// record is a transcript entry. Every optional field is omitempty because
// absence is meaningful to the reader: a record with no promptSource is a turn
// nobody typed, and a relocated record carries relocatedCwd and no cwd at all.
type record struct {
	Type         string          `json:"type"`
	UUID         string          `json:"uuid,omitempty"`
	ParentUUID   string          `json:"parentUuid,omitempty"`
	SessionID    string          `json:"sessionId"`
	Timestamp    string          `json:"timestamp,omitempty"`
	CWD          string          `json:"cwd,omitempty"`
	RelocatedCWD string          `json:"relocatedCwd,omitempty"`
	GitBranch    string          `json:"gitBranch,omitempty"`
	Version      string          `json:"version,omitempty"`
	IsSidechain  bool            `json:"isSidechain,omitempty"`
	AgentID      string          `json:"agentId,omitempty"`
	PromptSource string          `json:"promptSource,omitempty"`
	Message      json.RawMessage `json:"message,omitempty"`
}

type message struct {
	Role    string          `json:"role"`
	Model   string          `json:"model,omitempty"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string            `json:"type"`
	Text      string            `json:"text,omitempty"`
	Thinking  string            `json:"thinking,omitempty"`
	Signature string            `json:"signature,omitempty"`
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name,omitempty"`
	Input     map[string]string `json:"input,omitempty"`
	ToolUseID string            `json:"tool_use_id,omitempty"`
	Content   string            `json:"content,omitempty"`
}

// sess accumulates one session's files while it is being written.
type sess struct {
	g       *generator
	co      checkout
	id      string
	branch  string
	at      time.Time
	parent  string
	main    []byte
	sidecar []byte
	dup     []byte

	// Turns written so far, by tier, which is what the filler steers on. Strip
	// yields at most one turn per tier per record, so a record's contribution is
	// known where it is written and never has to be read back.
	conv int
	inv  int
	res  int

	// plain is main's size with the corpus root's own bytes discounted. Sizing
	// the filler against it is what keeps the tree a function of the Spec: an
	// absolute path is an argument to Generate, and budgeting against the real
	// bytes would write a different number of records under a longer root.
	plain int
}

func (g *generator) session(co checkout, cos []checkout, sp spots, ordinal, n int, budget int64) {
	s := &sess{
		g:      g,
		co:     co,
		id:     g.uuid(),
		branch: branchFor(n),
		at:     epoch.Add(time.Duration(ordinal) * sessionSpacing),
	}
	if sp.relocated {
		s.emit(record{Type: "relocated", SessionID: s.id, RelocatedCWD: co.cwd})
	}

	s.typedPrompt(g.filler(convTarget.MeanTurnBytes))
	first := g.reply(s)
	g.exchange(s)
	s.untypedUser(g.filler(convTarget.MeanTurnBytes))
	s.slashCommand(g.filler(convTarget.MeanTurnBytes - commandPrefix))

	g.plantInto(s, co, cos, sp)

	for int64(s.plain) < budget {
		if s.behindOnConversation() {
			g.reply(s)
			continue
		}
		g.exchange(s)
	}

	if sp.subagent {
		s.subagent(g.filler(convTarget.MeanTurnBytes), g.filler(convTarget.MeanTurnBytes))
	}
	if sp.dup {
		s.dup = first
	}
	s.commit()
}

// reply writes the conversation turn and exchange the invocation and result
// pair, each at the size a turn of that tier averages in a real store. Every
// ordinary record goes through one of the two, so a tier has one mean size in
// the corpus rather than one per call site.
func (g *generator) reply(s *sess) []byte {
	half := convTarget.MeanTurnBytes / 2
	return s.reasonedReply(g.filler(half), g.filler(half))
}

func (g *generator) exchange(s *sess) {
	s.toolExchange(g.filler(invTarget.MeanTurnBytes-invPrefix), g.filler(resTarget.MeanTurnBytes))
}

// behindOnConversation reports whether a conversation turn is what the session
// needs next to hold the real store's tier ratio. Steering on the deficit
// rather than repeating a fixed pattern is what absorbs the opening records,
// whose tiers are dictated by the record shapes they exist to cover.
func (s *sess) behindOnConversation() bool {
	total := s.conv + s.inv + s.res
	return float64(s.conv) < convTarget.TurnShare*float64(total)
}

// plantInto writes the needles this session was chosen to carry and records
// where each one landed, so a test asserts on coordinates rather than on the
// mere existence of a hit.
func (g *generator) plantInto(s *sess, co checkout, cos []checkout, sp spots) {
	if sp.cross != "" {
		s.reasonedReply(g.sentence(9), "the "+sp.cross+" path is the one we settled on")
		g.plants = append(g.plants, Plant{
			Term: sp.cross, Kind: KindCrossCheckout, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierConversation, Author: schema.AuthorAssistant, Count: 1,
			otherCwd: sibling(cos, co),
		})
	}
	if sp.single != "" {
		s.reasonedReply(g.sentence(9), "only this session mentions "+sp.single+" at all")
		g.plants = append(g.plants, Plant{
			Term: sp.single, Kind: KindSingleSession, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierConversation, Author: schema.AuthorAssistant, Count: 1,
		})
	}
	if sp.result != "" {
		s.toolExchange(g.sentence(6), "grep found "+sp.result+" in the vendored tree")
		g.plants = append(g.plants, Plant{
			Term: sp.result, Kind: KindResultOnly, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierResult, Author: schema.AuthorSystem, Count: 1,
		})
	}
	if len(sp.phrase) > 0 {
		for _, word := range sp.phrase {
			s.reasonedReply(g.sentence(9), "one part of it is "+word+" and nothing else here")
		}
		g.plants = append(g.plants, Plant{
			Term: joinWords(sp.phrase), Kind: KindPhrase, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierConversation, Author: schema.AuthorAssistant, Count: len(sp.phrase),
		})
	}
}

// sibling is another checkout of the same repo, which for a cross-checkout
// needle is the directory the search must be run from.
func sibling(cos []checkout, co checkout) string {
	for _, other := range cos {
		if other.repo == co.repo && other.cwd != co.cwd {
			return other.cwd
		}
	}
	return ""
}

func branchFor(n int) string {
	if n == 0 {
		return "main"
	}
	return fmt.Sprintf("feature-%d", n)
}

// emit appends one record and returns its bytes, so a caller can write the same
// line into a second file and give the corpus a duplicated uuid.
func (s *sess) emit(r record) []byte {
	r.SessionID = s.id
	if r.Type != "relocated" {
		r.CWD = s.co.cwd
		r.GitBranch = s.branch
		r.Version = writerVersion
		r.Timestamp = s.at.Format(time.RFC3339Nano)
		r.ParentUUID = s.parent
		s.parent = r.UUID
		s.at = s.at.Add(recordSpacing)
	}
	line := append(mustJSON(r), '\n')
	if r.IsSidechain {
		s.sidecar = append(s.sidecar, line...)
	} else {
		s.main = append(s.main, line...)
		s.plain += len(line) - bytes.Count(line, s.g.root)*len(s.g.root)
	}
	return line
}

func (s *sess) typedPrompt(text string) {
	s.conv++
	s.emit(record{
		Type: "user", UUID: s.g.uuid(), PromptSource: "typed",
		Message: userText(text),
	})
}

// untypedUser is a user-role record with no promptSource — a third to two
// thirds of human-shaped records across the real corpus, and none of them
// typed by the operator.
func (s *sess) untypedUser(text string) {
	s.conv++
	s.emit(record{
		Type: "user", UUID: s.g.uuid(),
		Message: mustMessage("user", []block{{Type: "text", Text: text}}),
	})
}

// commandPrefix is what strip puts before the arguments of a slash command:
// the command name, so the turn is that much longer than the text written here.
const commandPrefix = len("/status ")

func (s *sess) slashCommand(args string) {
	s.conv++
	body := "<command-name>/status</command-name>" +
		"<command-message>status</command-message>" +
		"<command-args>" + args + "</command-args>"
	s.emit(record{Type: "user", UUID: s.g.uuid(), Message: userText(body)})
}

// reasonedReply is an assistant turn carrying a thinking block beside its
// prose. The signature is the bulk of a real one and none of its words.
func (s *sess) reasonedReply(thinking, text string) []byte {
	s.conv++
	return s.emit(record{
		Type: "assistant", UUID: s.g.uuid(),
		Message: mustMessage("assistant", []block{
			{Type: "thinking", Thinking: thinking, Signature: s.g.signature()},
			{Type: "text", Text: text},
		}),
	})
}

// invPrefix is what strip prepends to a Bash command to make the invocation
// turn — the tool name, then the command as it is written below. Sizing the
// command against the target without it would overshoot every invocation turn.
const invPrefix = len("Bash rg -n ")

// toolExchange is the invocation and result pair: the tool call in one record,
// its output in the next, which is how the two non-default tiers are filled.
// One result to one invocation is the real store's 76,522 to 78,375.
func (s *sess) toolExchange(command, result string) {
	s.inv++
	s.res++
	id := "toolu_" + s.g.token()
	s.emit(record{
		Type: "assistant", UUID: s.g.uuid(),
		Message: mustMessage("assistant", []block{{
			Type: "tool_use", ID: id, Name: "Bash",
			Input: map[string]string{"command": "rg -n " + command, "description": "search"},
		}}),
	})
	s.emit(record{
		Type: "user", UUID: s.g.uuid(),
		Message: mustMessage("user", []block{{Type: "tool_result", ToolUseID: id, Content: result}}),
	})
}

// subagent writes the session's sidechain records, which Claude Code files in a
// subagents/ sidecar beside the session rather than in the session file.
func (s *sess) subagent(prompt, reply string) {
	s.conv += 2
	agent := s.g.token()
	s.emit(record{
		Type: "user", UUID: s.g.uuid(), IsSidechain: true, AgentID: agent,
		PromptSource: "sdk", Message: userText(prompt),
	})
	s.emit(record{
		Type: "assistant", UUID: s.g.uuid(), IsSidechain: true, AgentID: agent,
		Message: mustMessage("assistant", []block{{Type: "text", Text: reply}}),
	})
}

// commit files the session's bytes. The duplicate copy goes under a
// subdirectory of the same checkout, which is how one session comes to be
// written to two files on a real machine and why dedup keys on the record uuid.
func (s *sess) commit() {
	dir := s.g.projectDir(s.co.cwd)
	s.g.add(filepath.Join(dir, s.id+".jsonl"), s.main)
	if len(s.sidecar) > 0 {
		s.g.add(filepath.Join(dir, s.id, "subagents", "agent-"+s.g.token()+".jsonl"), s.sidecar)
	}
	if len(s.dup) > 0 {
		sub := s.g.projectDir(filepath.Join(s.co.cwd, "pkg"))
		s.g.add(filepath.Join(sub, s.id+".jsonl"), s.dup)
	}
}

func userText(text string) json.RawMessage {
	return mustJSON(message{Role: "user", Content: mustJSON(text)})
}

func mustMessage(role string, blocks []block) json.RawMessage {
	m := message{Role: role, Content: mustJSON(blocks)}
	if role == "assistant" {
		m.Model = "claude-opus-5"
	}
	return mustJSON(m)
}

// mustJSON encodes without HTML escaping, because the slash-command records
// Claude Code writes carry literal <command-args> tags and strip finds a typed
// turn by looking for exactly those bytes.
func mustJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic("corpusgen: a generated record does not marshal: " + err.Error())
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}
