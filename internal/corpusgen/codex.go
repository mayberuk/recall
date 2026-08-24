package corpusgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

const (
	codexOriginator       = "codex_cli_rs"
	codexCLIVersion       = "0.135.0"
	codexSourceCLI        = "cli"
	codexModelProviderOAI = "openai"
)

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

// GenerateCodex writes a Codex CLI session store under root, dated
// sessions/YYYY/MM/DD/rollout-<ISO>-<thread>.jsonl the way ~/.codex/sessions
// is laid out. It shares Spec, Plant and Corpus with Generate so the bench and
// differential harnesses can target either agent without changing shape, and
// the same seed-determinism holds: two calls with one Spec write identical
// trees, differing only in the root path itself.
func GenerateCodex(spec Spec, root string) (Corpus, error) {
	if err := spec.check(); err != nil {
		return Corpus{}, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Corpus{}, fmt.Errorf("corpusgen: cannot resolve %s: %w", root, err)
	}
	g := &generator{
		spec:      spec,
		rnd:       rand.New(rand.NewSource(spec.Seed)),
		root:      []byte(abs),
		projects:  filepath.Join(abs, "sessions"),
		checkouts: filepath.Join(abs, "checkouts"),
	}

	cos := g.plan()
	for _, co := range cos {
		if err := g.writeCheckout(co); err != nil {
			return Corpus{}, err
		}
	}

	terms := g.terms()
	budget := g.spec.TargetBytes / int64(g.spec.sessions())
	ordinal := 0
	for i, co := range cos {
		for n := 0; n < g.spec.SessionsEach; n++ {
			g.codexSession(co, cos, g.spotsFor(i, n, len(cos), terms), ordinal, budget)
			ordinal++
		}
	}

	if err := g.flush(); err != nil {
		return Corpus{}, err
	}
	return Corpus{Root: g.projects, Plants: g.plants}, nil
}

// codexSess accumulates one rollout's bytes while it is being written, the
// same role records.go's sess plays for the Claude generator.
type codexSess struct {
	g     *generator
	co    checkout
	id    string
	start time.Time
	at    time.Time
	main  []byte

	// plain is main's size with the corpus root's own bytes discounted, so the
	// tree stays a function of the Spec regardless of where it is written.
	plain int
	// conv, inv, res are turns written so far, by tier, steering the filler.
	conv, inv, res int
}

func (g *generator) codexSession(co checkout, cos []checkout, sp spots, ordinal int, budget int64) {
	s := &codexSess{
		g: g, co: co, id: g.uuid(),
		start: epoch.Add(time.Duration(ordinal) * sessionSpacing),
	}
	s.at = s.start

	s.sessionMeta()
	s.exchange(g.filler(convTarget.MeanTurnBytes/2), g.filler(convTarget.MeanTurnBytes/2))
	s.toolCall(g.filler(invTarget.MeanTurnBytes), g.filler(resTarget.MeanTurnBytes))

	g.codexPlantInto(s, co, cos, sp)

	for int64(s.plain) < budget {
		if s.behindOnConversation() {
			s.exchange(g.filler(convTarget.MeanTurnBytes/2), g.filler(convTarget.MeanTurnBytes/2))
			continue
		}
		s.toolCall(g.filler(invTarget.MeanTurnBytes), g.filler(resTarget.MeanTurnBytes))
	}

	g.commitCodex(s)
}

func (s *codexSess) behindOnConversation() bool {
	total := s.conv + s.inv + s.res
	return float64(s.conv) < convTarget.TurnShare*float64(total)
}

func (s *codexSess) sessionMeta() {
	s.emit("session_meta", codexSessionMeta{
		ID: s.id, Timestamp: s.at.Format(time.RFC3339Nano), CWD: s.co.cwd,
		Originator: codexOriginator, CLIVersion: codexCLIVersion,
		Source: codexSourceCLI, ModelProvider: codexModelProviderOAI,
		Git: &codexGit{
			CommitHash:    s.g.token()[:12],
			Branch:        "main",
			RepositoryURL: "https://git.invalid/corpusgen/" + s.co.repo + ".git",
		},
	})
}

func (s *codexSess) exchange(userText, assistantText string) {
	s.conv += 2
	s.emit("response_item", codexMessageItem{
		Type: "message", Role: "user",
		Content: []codexContentPart{{Type: "input_text", Text: userText}},
	})
	s.emit("response_item", codexMessageItem{
		Type: "message", Role: "assistant",
		Content: []codexContentPart{{Type: "output_text", Text: assistantText}},
	})
}

func (s *codexSess) toolCall(command, output string) {
	s.inv++
	s.res++
	callID := "call_" + s.g.token()
	s.emit("response_item", codexFunctionCallItem{
		Type: "function_call", Name: "shell", CallID: callID,
		Arguments: string(mustJSON(map[string]string{"command": "rg -n " + command})),
	})
	s.emit("response_item", codexFunctionCallOutputItem{
		Type: "function_call_output", CallID: callID, Output: output,
	})
}

// emit appends one record and advances the session clock. Every ordinary
// record goes through this, which is what keeps s.plain a true count of what
// the byte budget is spent on.
func (s *codexSess) emit(typ string, payload any) {
	line := codexLine(s.at.Format(time.RFC3339Nano), typ, payload)
	s.main = append(s.main, line...)
	s.plain += len(line) - bytes.Count(line, s.g.root)*len(s.g.root)
	s.at = s.at.Add(recordSpacing)
}

func codexLine(ts, typ string, payload any) []byte {
	return append(mustJSON(codexRecord{Timestamp: ts, Type: typ, Payload: mustJSON(payload)}), '\n')
}

// codexPlantInto writes the needles this session was chosen to carry, in the
// same coordinates plantInto records for the Claude generator, so a test
// asserts on where a term landed rather than on its mere existence.
func (g *generator) codexPlantInto(s *codexSess, co checkout, cos []checkout, sp spots) {
	if sp.cross != "" {
		s.exchange(g.sentence(9), "the "+sp.cross+" path is the one we settled on")
		g.plants = append(g.plants, Plant{
			Term: sp.cross, Kind: KindCrossCheckout, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierConversation, Author: schema.AuthorAssistant, Count: 1,
			otherCwd: sibling(cos, co),
		})
	}
	if sp.single != "" {
		s.exchange(g.sentence(9), "only this session mentions "+sp.single+" at all")
		g.plants = append(g.plants, Plant{
			Term: sp.single, Kind: KindSingleSession, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierConversation, Author: schema.AuthorAssistant, Count: 1,
		})
	}
	if sp.result != "" {
		s.toolCall(g.sentence(6), "grep found "+sp.result+" in the vendored tree")
		g.plants = append(g.plants, Plant{
			Term: sp.result, Kind: KindResultOnly, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierResult, Author: schema.AuthorSystem, Count: 1,
		})
	}
	if len(sp.phrase) > 0 {
		for _, word := range sp.phrase {
			s.exchange(g.sentence(9), "one part of it is "+word+" and nothing else here")
		}
		g.plants = append(g.plants, Plant{
			Term: joinWords(sp.phrase), Kind: KindPhrase, Session: s.id, Cwd: co.cwd,
			Tier: schema.TierConversation, Author: schema.AuthorAssistant, Count: len(sp.phrase),
		})
	}
}

// commitCodex files the session's bytes at the dated path Codex itself lays
// rollouts out at, keyed by the session's own start time rather than its
// current clock, so every record inside one file shares a directory.
func (g *generator) commitCodex(s *codexSess) {
	day := s.start
	name := "rollout-" + day.Format("2006-01-02T15-04-05") + "-" + s.id + ".jsonl"
	path := filepath.Join(g.projects, day.Format("2006"), day.Format("01"), day.Format("02"), name)
	g.add(path, s.main)
}
