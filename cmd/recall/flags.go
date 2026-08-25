package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/style"
)

// DefaultMaxBytes caps what any verb may emit. The mean session's conversation
// is 268 KB and the largest is 2.22 MB, so an uncapped response reproduces the
// context bloat the tool exists to remove; 64 KiB is roughly 16K tokens. The cap
// refuses rather than truncates, because a truncated answer looks complete.
const DefaultMaxBytes int64 = 64 << 10

// ProviderAuto is --provider's default: let the run's own environment decide
// which agent's transcripts to read, the same resolution RECALL_AGENT unset
// falls back to.
const ProviderAuto = "auto"

// Globals are the flags every verb accepts. A verb binds them onto its own
// FlagSet, so a new verb never edits this file.
type Globals struct {
	MaxBytes int64
	JSON     bool
	Format   string

	// Budget shapes output to roughly this many tokens instead of refusing over
	// it. A caller budgeting a turn needs less of an answer, not an error.
	Budget int

	// Provider is the CLI's spelling of RECALL_AGENT: auto, an agent name, or
	// all. Every verb — find, turns, when, show and doctor — reads whatever
	// it resolves to.
	Provider string

	// Color is auto, always or never. Auto means "colour if a terminal is
	// reading" — every other destination, a pipe and a file included, gets the
	// same bytes it got before colour existed.
	Color string
}

// NewGlobals returns the defaults a verb starts from.
func NewGlobals() *Globals {
	return &Globals{MaxBytes: DefaultMaxBytes, Format: FormatText, Provider: ProviderAuto, Color: string(style.Auto)}
}

// The output formats. jsonl is one object per line so a caller can stream or
// pipe it; json is one object for the whole answer.
const (
	FormatText  = "text"
	FormatJSON  = "json"
	FormatJSONL = "jsonl"
)

// BytesPerToken converts a token budget to bytes. Four is the usual rule of
// thumb for English prose and is close enough for a budget; the exact figure
// only matters if it under-counts, and over-counting bytes shapes output down
// rather than up.
const BytesPerToken = 4

// Bind attaches the global flags to fs.
func (g *Globals) Bind(fs *flag.FlagSet) {
	fs.Int64Var(&g.MaxBytes, "max-bytes", g.MaxBytes, "refuse to emit more than this many bytes")
	fs.BoolVar(&g.JSON, "json", g.JSON, "emit machine-readable output (the same as --format json)")
	fs.StringVar(&g.Format, "format", g.Format, "text, json or jsonl")
	fs.IntVar(&g.Budget, "budget", g.Budget, "shape output to roughly this many tokens instead of refusing")
	fs.StringVar(&g.Provider, "provider", g.Provider,
		"auto, an agent name, or all — which agent's transcripts every verb reads: find, turns, "+
			"when, show and doctor all resolve through this")
	fs.StringVar(&g.Color, "color", g.Color, "auto, always or never — auto colours a terminal and nothing else")
}

// Palette resolves the run's colour for w. JSON and JSONL are never coloured
// whatever --color says: those formats exist to be parsed, and a caller that
// asked for machine output did not ask to strip escapes back out of it.
func (g *Globals) Palette(w io.Writer) style.Palette {
	if g.Format != FormatText {
		return style.Palette{}
	}
	if g.Color == "" {
		return style.Resolve(style.Auto, w)
	}
	return style.Resolve(style.Mode(g.Color), w)
}

// Check rejects a cap that cannot bound anything, resolves the two spellings
// of the output format, and records an explicit --provider as the run's agent
// selection — the same single path RECALL_AGENT resolves through, so the flag
// wins when the two disagree.
func (g *Globals) Check() error {
	if g.MaxBytes <= 0 {
		return fperr.New(fperr.ArgError, "--max-bytes must be positive, got %d", g.MaxBytes)
	}
	if g.Budget < 0 {
		return fperr.New(fperr.ArgError, "--budget cannot be negative, got %d", g.Budget)
	}
	switch g.Format {
	case FormatText, FormatJSON, FormatJSONL:
	default:
		return fperr.New(fperr.ArgError, "--format takes text, json or jsonl, got %q", g.Format)
	}
	// The zero value means auto, the same way it does for Provider: a Globals
	// nobody filled in is a working default, not a configuration error.
	if g.Color == "" {
		g.Color = string(style.Auto)
	}
	if !style.ValidMode(g.Color) {
		return fperr.New(fperr.ArgError, "--color takes %s, got %q", strings.Join(style.Modes, ", "), g.Color)
	}
	if g.JSON {
		if g.Format == FormatJSONL {
			return fperr.New(fperr.ArgError, "--json and --format jsonl are two formats; pick one")
		}
		g.Format = FormatJSON
	}
	// auto and the zero value both mean "let the environment decide", so
	// neither ever reaches SetSelection — which refuses an empty spec, and
	// which would otherwise shadow RECALL_AGENT for the rest of the process.
	if g.Provider != "" && g.Provider != ProviderAuto {
		if err := archive.SetSelection(g.Provider); err != nil {
			return err
		}
	}
	return nil
}

// Cap is the byte ceiling actually applied, which is the tighter of the hard
// refusal cap and the token budget.
func (g *Globals) Cap() int64 {
	if g.Budget <= 0 {
		return g.MaxBytes
	}
	if budget := int64(g.Budget) * BytesPerToken; budget < g.MaxBytes {
		return budget
	}
	return g.MaxBytes
}

// SearchFlags are the flags shared by every verb that searches. The spellings
// are pinned by the contract: --results opts into the tool-output tier that the
// coverage line declares unsearched, and --all lifts the default repo scope.
type SearchFlags struct {
	All     bool
	Repo    string
	Results bool
	Tools   bool
}

// Bind attaches the search flags to fs.
func (s *SearchFlags) Bind(fs *flag.FlagSet) {
	fs.BoolVar(&s.All, "all", s.All, "search every repo on the machine, not just this one")
	fs.StringVar(&s.Repo, "repo", s.Repo, "search this repo identity instead of the current one")
	fs.BoolVar(&s.Results, "results", s.Results, "also search tool output")
	fs.BoolVar(&s.Tools, "tools", s.Tools, "show tool invocation lines")
}

// newFlags suppresses flag's own usage dump so every failure leaves stderr in
// recall's one-line-plus-ERROR_CODE shape.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags turns a parse failure into a message with a next move in it. The
// stdlib says only "flag provided but not defined: -bogusflag", which is a dead
// end: the caller learns the flag is wrong and nothing about what is right.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if names := flagNames(fs.Name()); len(names) > 0 {
			return fperr.New(fperr.ArgError, "%s: %v\n%s takes: %s\nrun `recall %s --help` for what each one does",
				fs.Name(), err, fs.Name(), strings.Join(names, " "), fs.Name())
		}
		return fperr.New(fperr.ArgError, "%s: %v", fs.Name(), err)
	}
	return nil
}
