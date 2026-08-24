package mcp

import (
	"context"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tool names. The recall_ prefix is load-bearing rather than decoration:
// an aggregating client is told not to lean on serverInfo.name to
// disambiguate, so the name is the only thing separating recall's find from
// any other server's.
//
// recall doctor is deliberately absent. It answers a question about archive
// integrity, not about past sessions, and every listed tool spends a session's
// tool-roster budget. recall_guide earns its place against that same cost,
// because the failure this package exists to fix was an agent guessing at
// query semantics instead of being told them, and a guide costs nothing until
// it is called.
const (
	toolFind  = "recall_find"
	toolGuide = "recall_guide"
	toolShow  = "recall_show"
	toolTurns = "recall_turns"
	toolWhen  = "recall_when"
)

// calls is one query at a time. The SDK dispatches tool calls concurrently and
// the archive refresh underneath a search writes to disk, so two of them in
// one process is not a supported state; serialising also bounds peak memory to
// a single corpus load. A call is on the order of 30 ms, which is not enough
// overlap to be worth engineering around.
type calls struct {
	mu       sync.Mutex
	searcher Searcher
}

// registerTools adds the five tools in sorted order. The SDK lists them sorted
// whatever order they arrive in, but registering them sorted keeps the file
// and the wire agreeing — deterministic tool ordering is a documented
// prompt-cache lever in this protocol revision, not a nicety.
func registerTools(s *mcpsdk.Server, c *calls) {
	mcpsdk.AddTool(s,
		tool(toolFind, "recall find", "which sessions talked about something, and how much"),
		serialized(c, c.searcher.Find))
	mcpsdk.AddTool(s,
		tool(toolGuide, "recall guide", "read this first: which command answers which question, and how a query is read"),
		serialized(c, func(ctx context.Context, _ GuideArgs) (GuideResult, error) {
			text, err := c.searcher.Guide(ctx)
			return GuideResult{Text: text}, err
		}))
	mcpsdk.AddTool(s,
		tool(toolShow, "recall show", "recover what was concluded, with the turns around it"),
		serialized(c, c.searcher.Show))
	mcpsdk.AddTool(s,
		tool(toolTurns, "recall turns", "the passages themselves, ranked across every session at once"),
		serialized(c, c.searcher.Turns))
	mcpsdk.AddTool(s,
		tool(toolWhen, "recall when", "place a topic in time: first said, last said, and the months between"),
		serialized(c, c.searcher.When))
}

// tool declares one tool. Every recall tool only reads, and its whole world is
// this machine's own archive, so both hints whose documented default is true
// are pinned to false. Each declaration takes its own addressable false, so no
// later write through one tool's pointer can reach another's.
//
// IdempotentHint stays at its default false on purpose: the archive refreshes
// on each call, so two identical calls can legitimately differ, and claiming
// idempotence would mislead in the direction that costs a caller a stale
// answer it never re-asked for.
func tool(name, title, description string) *mcpsdk.Tool {
	no := false
	return &mcpsdk.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           title,
			ReadOnlyHint:    true,
			DestructiveHint: &no,
			OpenWorldHint:   &no,
		},
	}
}

// serialized wraps one verb in the package-wide lock and hands the typed value
// straight back to the SDK, which fills structuredContent from it and emits
// the compatible JSON text block beside it.
//
// A returned error becomes IsError with the message in the content, which is
// what lets a model read what went wrong and correct itself. A search that
// matched nothing is not that: it is a successful answer with zero sessions,
// whose coverage block carries the terms-nearby survey that turns a dead end
// into the next query. Nothing here maps an empty result to an error.
func serialized[In, Out any](c *calls, answer func(context.Context, In) (Out, error)) mcpsdk.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		c.mu.Lock()
		defer c.mu.Unlock()

		out, err := answer(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	}
}
