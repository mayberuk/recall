package mcp

import (
	"context"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// methodCallTool is the JSON-RPC method a tool invocation arrives as. The SDK
// keeps its own copy of this string unexported, so this package names the
// wire constant itself rather than reaching for a symbol it cannot import.
const methodCallTool = "tools/call"

// searchingTools are the calls whose first successful result in a server
// process carries the compact guide. recall_guide is deliberately absent:
// its own answer already is a guide, and prepending another would double the
// exact per-call cost this mechanism exists to cut.
var searchingTools = map[string]bool{
	toolFind:  true,
	toolTurns: true,
	toolShow:  true,
	toolWhen:  true,
}

// preambleOnce prepends Searcher.Preamble to the first searching tool result
// of a server process and withholds it on every call after.
//
// It runs as receiving middleware, wrapping the whole tools/call dispatch,
// rather than living inside serialized in tools.go: every searching tool
// already shares that one handler-registration path, but the once-per-process
// bookkeeping is cross-cutting to all four of them, not particular to any one
// verb's answer type.
//
// It must never rebuild the JSON compatibility block the generic tool
// wrapper already attached. go-sdk@v1.7.0 mcp/server.go:394-444 sets a
// result's Content to a TextContent carrying the identical bytes it assigns
// to StructuredContent, but only when the handler leaves Content nil — which
// every searching tool's handler (serialized, in tools.go) does. Prepending
// onto that slice, rather than constructing a second copy from the answer
// value, is what keeps the two blocks byte-identical: an independent
// json.Marshal of the answer would not match, because mcp/tool.go's
// applySchema always takes the map-remarshal branch for an object-shaped
// output (append-only defaulting is skipped, but the re-marshal is not), and
// Go always marshals a map's keys sorted while a struct marshals in
// declaration order.
type preambleOnce struct {
	source func(ctx context.Context) (string, error)

	mu   sync.Mutex
	sent bool
}

func newPreambleOnce(searcher Searcher) *preambleOnce {
	return &preambleOnce{source: searcher.Preamble}
}

func (p *preambleOnce) middleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		res, err := next(ctx, method, req)
		if err != nil || method != methodCallTool {
			return res, err
		}
		params, ok := req.GetParams().(*mcpsdk.CallToolParamsRaw)
		if !ok || !searchingTools[params.Name] {
			return res, err
		}
		ctr, ok := res.(*mcpsdk.CallToolResult)
		if !ok || ctr.IsError {
			return res, err
		}
		text := p.take(ctx)
		if text == "" {
			return res, err
		}
		ctr.Content = append([]mcpsdk.Content{&mcpsdk.TextContent{Text: text}}, ctr.Content...)
		return res, err
	}
}

// take answers the preamble at most once per process: the first searching
// call to reach here claims the slot, even if the source then errors or
// answers empty, because leaving the slot open for a retry would mean a
// misconfigured Searcher never stops paying the per-call cost this exists to
// cut.
func (p *preambleOnce) take(ctx context.Context) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sent {
		return ""
	}
	p.sent = true
	text, err := p.source(ctx)
	if err != nil {
		return ""
	}
	return text
}
