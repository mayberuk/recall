package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mayberuk/recall/internal/render"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A test that only checked the preamble appears once would pass on a build
// that shows it on every call and happens to be exercised only once, or on
// one that never shows it and happens to be checked only after a call that
// skipped it. The two calls below are the negative control against both.
func TestPreambleAppearsOnTheFirstSearchingCallAndNeverAgain(t *testing.T) {
	fake := &fakeSearcher{
		preamble: "PREAMBLE: read this once.",
		find:     render.Find{Query: "agvtool"},
		turns:    render.Turns{Query: "agvtool"},
	}
	cs := serve(t, fake)

	first := callTool(t, cs, "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "agvtool"}})
	if first.IsError {
		t.Fatalf("first call errored: %s", textAt(t, first, 0))
	}
	if got := len(first.Content); got != 2 {
		t.Fatalf("first searching call has %d content blocks, want 2 (preamble, then the compatible JSON)", got)
	}
	if got := textAt(t, first, 0); got != fake.preamble {
		t.Errorf("first block = %q, want the preamble %q", got, fake.preamble)
	}

	second := callTool(t, cs, "recall_turns", TurnsArgs{SearchArgs: SearchArgs{Query: "agvtool"}})
	if second.IsError {
		t.Fatalf("second call errored: %s", textAt(t, second, 0))
	}
	if got := len(second.Content); got != 1 {
		t.Fatalf("second searching call has %d content blocks, want 1 — the preamble must not repeat", got)
	}
}

// recall_guide is not a searching tool: its own answer never carries the
// preamble, and calling it first must not consume the one slot a later
// searching call is owed.
func TestGuideNeitherCarriesNorConsumesThePreamble(t *testing.T) {
	fake := &fakeSearcher{
		preamble: "PREAMBLE",
		guide:    "the full guide text",
		find:     render.Find{Query: "x"},
	}
	cs := serve(t, fake)

	guideRes := callTool(t, cs, "recall_guide", GuideArgs{})
	if got := len(guideRes.Content); got != 1 {
		t.Fatalf("recall_guide has %d content blocks, want 1 — its own answer must not carry the preamble", got)
	}

	findRes := callTool(t, cs, "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "x"}})
	if got := len(findRes.Content); got != 2 {
		t.Fatalf("first searching call after recall_guide has %d content blocks, want 2 — "+
			"recall_guide must not have consumed the once-per-process slot", got)
	}
}

// An empty preamble (a misconfigured Searcher) must not surface as an empty
// text block, and must not stall the mechanism: the slot is still spent, and
// a later call sees no preamble either.
func TestAnEmptyPreambleAddsNoBlockAndStillSpendsTheSlot(t *testing.T) {
	fake := &fakeSearcher{find: render.Find{Query: "x"}, turns: render.Turns{Query: "x"}}
	cs := serve(t, fake)

	first := callTool(t, cs, "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "x"}})
	if got := len(first.Content); got != 1 {
		t.Fatalf("first call with an empty preamble has %d content blocks, want 1", got)
	}
}

// The mechanism must reuse the JSON block the generic tool wrapper already
// built rather than reconstruct one, because a reconstruction taken straight
// from the answer value would not match: mcp/tool.go's applySchema always
// re-marshals object-shaped output from a map, and Go marshals a map's keys
// sorted while it marshals a struct in declaration order — render.Find's own
// field order is not alphabetical (verb, query, scope, sort, hits, ...), so a
// naive json.Marshal(out) block would diverge from structuredContent right
// here. capture reads the server-side result before the wire serializes it,
// which is the only place the comparison is meaningful without a JSON
// decode-and-remarshal on the client side masking the very divergence this
// guards against.
func TestPreambleBlockLeavesTheCompatibilityBlockByteIdenticalToStructuredContent(t *testing.T) {
	fake := &fakeSearcher{
		preamble: "PREAMBLE: read this once.",
		find: render.Find{
			Verb:  "find",
			Query: "agvtool",
			Hits:  3,
			Sessions: []render.Session{
				{ID: "5fd86b00", Repo: "github.com/mayberuk/recall", Hits: 3},
			},
		},
	}
	s, err := NewServer(Options{Version: "0.0.0-test", Searcher: fake})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	seen := &capture{method: methodCallTool}
	s.AddReceivingMiddleware(seen.middleware)
	cs := connect(t, s)

	callTool(t, cs, "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "agvtool"}})

	ctr, ok := seen.get().(*mcpsdk.CallToolResult)
	if !ok {
		t.Fatalf("tools/call produced %T, want *mcpsdk.CallToolResult", seen.get())
	}
	if got := len(ctr.Content); got != 2 {
		t.Fatalf("server-side result has %d content blocks, want 2", got)
	}
	pre, ok := ctr.Content[0].(*mcpsdk.TextContent)
	if !ok || pre.Text != fake.preamble {
		t.Errorf("first block = %+v, want the preamble %q", ctr.Content[0], fake.preamble)
	}
	jsonBlock, ok := ctr.Content[1].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("second block is %T, want *mcpsdk.TextContent", ctr.Content[1])
	}
	structured, ok := ctr.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("structuredContent is %T, want json.RawMessage", ctr.StructuredContent)
	}
	if jsonBlock.Text != string(structured) {
		t.Errorf("compatibility text block is not byte-identical to structuredContent:\ntext:  %s\nstruct: %s",
			jsonBlock.Text, structured)
	}
}

// textAt is one content block's text. Unlike soleText in tools_test.go, it
// does not require the block to be alone — the preamble tests need to read a
// specific block out of two.
func textAt(t *testing.T, res *mcpsdk.CallToolResult, i int) string {
	t.Helper()
	if i >= len(res.Content) {
		t.Fatalf("content block %d requested, only %d present", i, len(res.Content))
	}
	text, ok := res.Content[i].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content block %d is %T, want text", i, res.Content[i])
	}
	return text.Text
}
