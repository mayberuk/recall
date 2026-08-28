package mcp

import (
	"context"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const methodCallTool = "tools/call"

// searchingTools receive the compact preamble once per process. recall_guide
// is excluded: its own answer already is a guide.
var searchingTools = map[string]bool{
	toolFind:  true,
	toolTurns: true,
	toolShow:  true,
	toolWhen:  true,
}

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
		// Prepend rather than rebuild: when Content is left nil (true of
		// every searching tool), the SDK copies it into StructuredContent as
		// JSON from a key-sorted map, but a struct marshals in field order —
		// rebuilding here would diverge from that copy.
		ctr.Content = append([]mcpsdk.Content{&mcpsdk.TextContent{Text: text}}, ctr.Content...)
		return res, err
	}
}

// take claims the slot before calling source, so a failing or empty
// Searcher never retries the cost.
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
