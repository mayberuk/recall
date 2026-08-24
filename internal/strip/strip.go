// Package strip turns a registered agent's transcript records into stripped
// turns.
//
// It is the only place raw JSONL becomes searchable text; nothing downstream
// parses a record again. The funnel it implements takes 1.29 GB to 36.5 MB by
// dropping what carries no words — thinking signatures, tool payloads, and the
// toolUseResult copy of every result — while keeping every turn that has any,
// because a silently dropped turn is the false negative the tool exists to
// prevent.
package strip

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// Stripper converts records to turns and accumulates what it saw for doctor.
// One Stripper serves every worker of a parallel corpus walk; see Strip.
type Stripper struct {
	lines       atomic.Int64
	untyped     atomic.Int64
	typed       atomic.Int64
	commandArgs atomic.Int64
	humanShaped atomic.Int64

	// mu guards only the unrecognised-type counts, which is the rare branch by
	// definition: the real corpus has none. Everything on the hot path is either
	// a stack value or an atomic add.
	mu      sync.Mutex
	unknown map[string]int
}

// New returns a Stripper that has seen nothing.
func New() *Stripper { return &Stripper{} }

// Observation is what a strip pass saw, for `recall doctor` to report.
type Observation struct {
	// Tally counts every record handed to Strip, including the record types
	// this build does not recognise. Malformed lines never reach Strip, so a
	// caller merges the reader's own tally into this one.
	Tally jsonl.Tally

	// HumanShapedMain counts main-session user records whose content is a
	// string or a text block and carries no tool result.
	HumanShapedMain int

	// Typed counts main-session user records labelled promptSource == "typed",
	// and CommandArgs counts slash-command records whose arguments carry typed
	// words. Both are the records the human rule can actually attribute.
	Typed       int
	CommandArgs int
}

// TypedLabelsMissing reports the degradation doctor warns on: a corpus with
// human-shaped main-session records and not one `typed` label. That means Claude
// Code stopped writing promptSource, not that nobody typed anything, and the
// human rule then degrades to returning nothing rather than noise — silently,
// which is the shape of the dealbreaker.
func (o Observation) TypedLabelsMissing() bool {
	return o.HumanShapedMain > 0 && o.Typed == 0
}

// Merge folds another observation into this one, so a caller that keeps one per
// worker still reports one corpus.
func (o *Observation) Merge(other Observation) {
	o.Tally.Merge(other.Tally)
	o.HumanShapedMain += other.HumanShapedMain
	o.Typed += other.Typed
	o.CommandArgs += other.CommandArgs
}

// Observation returns a snapshot of what every Strip call so far saw.
func (s *Stripper) Observation() Observation {
	o := Observation{
		Tally: jsonl.Tally{
			Lines:   int(s.lines.Load()),
			Untyped: int(s.untyped.Load()),
		},
		HumanShapedMain: int(s.humanShaped.Load()),
		Typed:           int(s.typed.Load()),
		CommandArgs:     int(s.commandArgs.Load()),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for typ, n := range s.unknown {
		if o.Tally.Unknown == nil {
			o.Tally.Unknown = make(map[string]int, len(s.unknown))
		}
		o.Tally.Unknown[typ] = n
	}
	return o
}

// Strip converts one record into its stripped turns, at most one per tier, and
// reports whether it produced any. Safe to call from several goroutines at once,
// which the archive's parallel walk does. One turn per tier is an invariant
// downstream depends on: the dedup key is the record uuid alone, and 17,659
// records exist in more than one file. Every record is observed either way, so a
// type this build has never seen is counted for doctor rather than lost.
func (s *Stripper) Strip(rec jsonl.Record) ([]schema.Turn, bool) {
	h := rec.Header()
	s.observe(h.Type)

	role := role(rec, h)
	if role == "" {
		return nil, false
	}

	var p parts
	p.gather(rec)

	main := role == roleUser && !h.IsSidechain
	// Counted only where the label can produce a human turn. A typed label on an
	// assistant or sidechain record yields no human turn, so counting it would
	// suppress TypedLabelsMissing — the one signal here designed to be fail-loud.
	if main && h.PromptSource == "typed" {
		s.typed.Add(1)
	}
	if main && p.shaped && !p.sawResult {
		s.humanShaped.Add(1)
	}

	conv := strings.Join(p.conv, "\n\n")
	author := author(role, h)
	if author == schema.AuthorSystem {
		if line, ok := commandLine(conv); ok {
			conv, author = line, schema.AuthorHuman
			s.commandArgs.Add(1)
		}
	}

	tiered := [...]struct {
		tier schema.Tier
		text string
	}{
		{schema.TierConversation, conv},
		{schema.TierInvocation, strings.Join(p.inv, "\n")},
		{schema.TierResult, strings.Join(p.res, "\n")},
	}
	n := 0
	for _, t := range tiered {
		if t.text != "" {
			n++
		}
	}
	if n == 0 {
		return nil, false
	}

	turn := schema.Turn{
		Session: h.SessionID,
		UUID:    h.UUID,
		TS:      h.Timestamp,
		Author:  author,
		Agent:   h.AgentID,
		Branch:  h.GitBranch,
		CWD:     workingDir(h),
	}
	turns := make([]schema.Turn, 0, n)
	for _, t := range tiered {
		if t.text == "" {
			continue
		}
		turn.Tier, turn.Text = t.tier, t.text
		turns = append(turns, turn)
	}
	return turns, true
}

// observe applies jsonl.Tally's counting rule to the type already parsed, under
// atomics so one Stripper serves a worker pool.
func (s *Stripper) observe(typ string) {
	s.lines.Add(1)
	if typ == "" {
		s.untyped.Add(1)
		return
	}
	if jsonl.KnownType(typ) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unknown == nil {
		s.unknown = map[string]int{}
	}
	s.unknown[typ]++
}

// workingDir is the raw cwd, or the relocated path when the record has no cwd:
// a relocated record carries relocatedCwd and no cwd at all.
func workingDir(h jsonl.Header) string {
	if h.CWD != "" {
		return h.CWD
	}
	return h.RelocatedCWD
}

const (
	roleUser      = "user"
	roleAssistant = "assistant"
)

// role is the conversational role a record speaks in, empty when it holds no
// conversation. A type this build does not know is asked for a message role
// rather than skipped, so a future record shape carrying words still strips.
func role(rec jsonl.Record, h jsonl.Header) string {
	switch h.Type {
	case roleUser, roleAssistant:
		return h.Type
	}
	if jsonl.KnownType(h.Type) {
		return ""
	}
	switch r := rec.Get("message.role").String(); r {
	case roleUser, roleAssistant:
		return r
	}
	return ""
}

// author attributes a turn. The order is load-bearing: a sidechain record is an
// agent's whatever else it carries, and typed is the settled human rule — content
// shape is refuted for this job, so no rule here reads the message text.
func author(role string, h jsonl.Header) schema.Author {
	switch {
	case h.IsSidechain:
		return schema.AuthorAgent
	case role == roleAssistant:
		return schema.AuthorAssistant
	case h.PromptSource == "typed":
		return schema.AuthorHuman
	default:
		return schema.AuthorSystem
	}
}
