package archive

import (
	"sort"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// Decoder turns one provider's raw records into recall's turn shape.
//
// It is a type alias to a bare interface literal rather than a named
// interface. Go only lets a type from another package satisfy Provider
// structurally if the declared return type of its Decoder method is
// identical to this one — a distinct named interface with the same method
// set does not count, even though its values are assignable to it. The alias
// keeps the return type anonymous so internal/strip can implement Provider
// without importing archive.
type Decoder = interface {
	Turns(rec jsonl.Record) ([]schema.Turn, bool)
}

// Provider is one coding agent's session store: where it lives, which paths
// inside it are transcripts, and how to decode them into turns. It is
// consumer-defined and implemented in internal/strip, never imported there —
// archive stays the package that owns the corpus walk, strip stays the
// package that knows the record formats. Every method signature names only
// stdlib, schema or jsonl types, which is what keeps that injection
// direction intact.
type Provider interface {
	// Agent is the vocabulary entry this provider answers for.
	Agent() schema.Agent

	// Root is the directory this provider's sessions live under.
	Root() (string, error)

	// IsTranscript reports whether a path relative to Root names a session
	// file, as opposed to a sidecar the provider's directory also holds.
	IsTranscript(rel string) bool

	// NeedsHead reports whether a transcript's project identity can only be
	// read from its first record rather than from its path — true for a
	// provider that keys sessions by date instead of by encoded cwd.
	NeedsHead() bool

	// Decoder returns the decoder for one transcript file, named relative to
	// Root.
	Decoder(rel string) Decoder
}

var providers = map[schema.Agent]Provider{}

// Register adds a provider. Each provider package calls it from its own
// init(), so adding an agent touches exactly one file and no two providers
// ever edit the same one. Registering an agent twice panics at startup
// rather than letting one provider silently shadow the other.
func Register(p Provider) {
	if p == nil {
		panic("archive: refusing to register a nil provider")
	}
	agent := p.Agent()
	if agent == "" {
		panic("archive: refusing to register a provider with no agent")
	}
	if _, dup := providers[agent]; dup {
		panic("archive: duplicate provider registration: " + string(agent))
	}
	providers[agent] = p
}

// Registered is every registered provider, sorted by agent so any listing
// built from it is stable between runs.
func Registered() []Provider {
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent() < out[j].Agent() })
	return out
}

// ProviderFor looks up the provider registered for one agent.
func ProviderFor(a schema.Agent) (Provider, bool) {
	p, ok := providers[a]
	return p, ok
}
