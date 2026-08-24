package strip

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// archiveDecoder mirrors archive.Decoder's method set exactly. Go treats two
// anonymous interface types as identical when their methods match, regardless
// of which name aliases them, so a *ClaudeCodeProvider satisfies
// archive.Provider without this type naming archive.Decoder.
type archiveDecoder = interface {
	Turns(rec jsonl.Record) ([]schema.Turn, bool)
}

// Provider mirrors archive.Provider for the same reason: an independently
// declared interface with an identical method set satisfies archive.Provider
// structurally, without a method signature here ever naming it.
type Provider interface {
	Agent() schema.Agent
	Root() (string, error)
	IsTranscript(rel string) bool
	NeedsHead() bool
	Decoder(rel string) archiveDecoder
}

// ClaudeCodeProvider is Claude Code's session store seen as a Provider. One
// Stripper decodes every file a walk hands it: Stripper is already safe for
// concurrent use through its atomics, so nothing about that concurrency
// changes by reaching it through a Decoder per file instead of a bare func.
type ClaudeCodeProvider struct {
	stripper *Stripper
}

// ClaudeCode returns a provider that has stripped nothing yet, matching New.
func ClaudeCode() *ClaudeCodeProvider {
	return &ClaudeCodeProvider{stripper: New()}
}

// Agent is claude-code's vocabulary entry.
func (p *ClaudeCodeProvider) Agent() schema.Agent { return schema.AgentClaudeCode }

// Root is Claude Code's session store: $CLAUDE_PROJECTS_DIR, else
// ~/.claude/projects.
func (p *ClaudeCodeProvider) Root() (string, error) {
	if d := os.Getenv("CLAUDE_PROJECTS_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// IsTranscript reports whether rel names a session file: every Claude Code
// transcript is a .jsonl, and nothing else under Root is.
func (p *ClaudeCodeProvider) IsTranscript(rel string) bool {
	return filepath.Ext(rel) == ".jsonl"
}

// NeedsHead is false: a Claude Code session's project identity comes from its
// path, not from reading its first record.
func (p *ClaudeCodeProvider) NeedsHead() bool { return false }

// Decoder returns the decoder for one transcript file, named relative to
// Root. It carries no per-file state of its own: p.stripper already
// accumulates what every worker of the walk saw.
func (p *ClaudeCodeProvider) Decoder(rel string) archiveDecoder {
	return claudeCodeDecoder{p.stripper}
}

// Observation is what every file this provider has decoded saw, for `recall
// doctor` to report from the same object it constructed.
func (p *ClaudeCodeProvider) Observation() Observation {
	return p.stripper.Observation()
}

// claudeCodeDecoder holds no state of its own, so every file in a walk gets
// an equivalent one and the concurrency of that walk is unchanged.
type claudeCodeDecoder struct{ stripper *Stripper }

func (d claudeCodeDecoder) Turns(rec jsonl.Record) ([]schema.Turn, bool) {
	return d.stripper.Strip(rec)
}
