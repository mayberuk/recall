package mcp

import (
	"context"

	"github.com/mayberuk/recall/internal/render"
)

// DefaultBudget shapes output for an MCP call that never sets --budget,
// so a silent agent gets a shaped answer rather than the full refusal cap.
const DefaultBudget = 4000

// Searcher answers the four searching verbs and the guide. It is an interface
// rather than a call into internal/scan so that the protocol surface and the
// CLI share one assembly path: the coverage footer is this tool's honesty
// contract, and a second place to build it is a second place for it to go
// wrong.
type Searcher interface {
	Find(ctx context.Context, args FindArgs) (render.Find, error)
	Turns(ctx context.Context, args TurnsArgs) (render.Turns, error)
	Show(ctx context.Context, args ShowArgs) (render.Show, error)
	When(ctx context.Context, args WhenArgs) (render.When, error)
	Guide(ctx context.Context) (string, error)

	// Preamble is the compact guide prepended to the first searching tool
	// result of a server process (see preamble.go). It is answered separately
	// from Guide so a caller that never triggers the mechanism pays nothing for
	// it, and so the two can diverge in size without either call's contract
	// changing.
	Preamble(ctx context.Context) (string, error)
}

// SearchArgs are the arguments every searching tool accepts, embedded rather
// than copied into each so the three cannot drift apart. Each jsonschema tag
// is the corresponding flag's own help string, so the schema a caller reads
// and `recall <verb> --help` cannot say different things either.
//
// Query is the only field without omitempty, which is what puts it — and
// nothing else — in the schema's required list.
type SearchArgs struct {
	Query string `json:"query" jsonschema:"what to look for; terms are ANDed, and a quoted phrase is matched together"`

	Repo    string `json:"repo,omitempty" jsonschema:"search this repo identity instead of the current one"`
	All     bool   `json:"all,omitempty" jsonschema:"search every repo on the machine, not just this one"`
	Results bool   `json:"results,omitempty" jsonschema:"also search tool output"`
	Tools   bool   `json:"tools,omitempty" jsonschema:"show tool invocation lines"`

	Exact    bool     `json:"exact,omitempty" jsonschema:"match terms literally, without stem expansion"`
	AllTerms bool     `json:"all_terms,omitempty" jsonschema:"require every term, returning nothing rather than the best partial match"`
	Not      []string `json:"not,omitempty" jsonschema:"skip turns carrying this term; repeatable"`

	Limit int    `json:"limit,omitempty" jsonschema:"most results to show: sessions for find and when, passages for turns"`
	Hits  *int   `json:"hits,omitempty" jsonschema:"most matched turns to show per session"`
	Sort  string `json:"sort,omitempty" jsonschema:"override the verb's order: recent"`

	Author  string `json:"author,omitempty" jsonschema:"only turns by human, assistant, agent or system"`
	Branch  string `json:"branch,omitempty" jsonschema:"only turns recorded on this git branch"`
	Agent   string `json:"agent,omitempty" jsonschema:"only turns from a subagent whose name contains this; it narrows turns within the transcripts provider already selected, and is not the provider"`
	Session string `json:"session,omitempty" jsonschema:"only this session, by id or unique prefix"`
	Since   string `json:"since,omitempty" jsonschema:"only turns at or after this: 2w, 3d, 12h or a date"`
	Until   string `json:"until,omitempty" jsonschema:"only turns at or before this: 2w, 3d, 12h or a date"`
	Mine    bool   `json:"mine,omitempty" jsonschema:"only turns you typed, the same as author human"`

	IncludeSelf   bool `json:"include_self,omitempty" jsonschema:"include the session asking the question"`
	IncludeRecall bool `json:"include_recall,omitempty" jsonschema:"include recall's own recorded commands and output"`

	Brief    bool `json:"brief,omitempty" jsonschema:"one line per session, no snippets"`
	NoUpdate bool `json:"no_update,omitempty" jsonschema:"search the archive as it stands, skipping the refresh from disk"`
	Budget   int  `json:"budget,omitempty" jsonschema:"shape output to roughly this many tokens instead of refusing; defaults to 4000 when unset"`

	Provider string `json:"provider,omitempty" jsonschema:"auto, an agent name, or all — which agent's transcripts are searched at all; this picks the corpus, where agent filters turns inside it"`
}

// FindArgs are the arguments to recall_find.
type FindArgs struct {
	SearchArgs
}

// TurnsArgs are the arguments to recall_turns, which quotes the passages
// themselves and so needs a say in how much of each one it quotes.
type TurnsArgs struct {
	SearchArgs

	// Chars is a pointer because zero is a value a caller means, not an
	// absence: it asks for the whole turn, where leaving it out asks for this
	// verb's own default.
	Chars *int `json:"chars,omitempty" jsonschema:"most characters of each turn to quote; 0 for the whole turn"`
}

// WhenArgs are the arguments to recall_when.
type WhenArgs struct {
	SearchArgs
}

// ShowArgs are the arguments to recall_show. Session is required and is the
// only required field: show recovers one named session, and a query merely
// anchors the windows inside it.
type ShowArgs struct {
	Session string `json:"session" jsonschema:"the session to show, by id or unique prefix"`

	Query string `json:"query,omitempty" jsonschema:"anchor the windows on the turns carrying this; without it the window sits at the end of the session, where a conclusion is"`
	Turn  string `json:"turn,omitempty" jsonschema:"anchor the window on this record uuid, which is the second half of a session:uuid citation from recall_turns"`
	Full  bool   `json:"full,omitempty" jsonschema:"the whole session, still refused when it breaches the byte cap"`

	// Around is a pointer for the same reason Chars is on recall_turns: zero
	// asks for the matched turn alone, which is not what leaving it out asks
	// for.
	Around *int `json:"around,omitempty" jsonschema:"turns of context each side of a match"`
	Chars  int  `json:"chars,omitempty" jsonschema:"most characters of each turn to quote; 0 for the whole turn"`

	Results  bool `json:"results,omitempty" jsonschema:"also show tool output"`
	Tools    bool `json:"tools,omitempty" jsonschema:"also show tool invocation lines"`
	NoUpdate bool `json:"no_update,omitempty" jsonschema:"read the archive as it stands, skipping the refresh from disk"`

	Provider string `json:"provider,omitempty" jsonschema:"auto, an agent name, or all — which agent's transcripts are read at all"`
}

// GuideArgs is empty: the guide is one page and takes no narrowing.
type GuideArgs struct{}

// GuideResult is the guide page. It is a struct rather than a bare string so
// the tool has an output schema like every other one.
type GuideResult struct {
	Text string `json:"text" jsonschema:"the guide page, verbatim"`
}
