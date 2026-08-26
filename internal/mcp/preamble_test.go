package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mayberuk/recall/internal/render"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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

func TestAnEmptyPreambleAddsNoBlockAndStillSpendsTheSlot(t *testing.T) {
	fake := &fakeSearcher{find: render.Find{Query: "x"}, turns: render.Turns{Query: "x"}}
	cs := serve(t, fake)

	first := callTool(t, cs, "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "x"}})
	if got := len(first.Content); got != 1 {
		t.Fatalf("first call with an empty preamble has %d content blocks, want 1", got)
	}
}

// capture reads the result server-side, before the wire serializes it —
// comparing after a client-side JSON decode would remarshal both sides and
// mask the divergence this test guards against.
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
