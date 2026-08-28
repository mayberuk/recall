package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeSearcher stands in for the CLI adapter. It answers with whatever a test
// set, records the arguments that reached it, and reports whether it was ever
// re-entered — the one-query-at-a-time invariant is invisible from outside
// otherwise.
type fakeSearcher struct {
	find     render.Find
	turns    render.Turns
	show     render.Show
	when     render.When
	guide    string
	preamble string
	err      error

	// dwell is how long each answer takes. A call that returns instantly
	// cannot demonstrate the absence of overlap.
	dwell time.Duration

	inFlight atomic.Int32
	overlaps atomic.Int32
	calls    atomic.Int32

	mu   sync.Mutex
	last struct {
		find  FindArgs
		turns TurnsArgs
		show  ShowArgs
		when  WhenArgs
	}
}

func (f *fakeSearcher) enter() error {
	f.calls.Add(1)
	if f.inFlight.Add(1) > 1 {
		f.overlaps.Add(1)
	}
	time.Sleep(f.dwell)
	f.inFlight.Add(-1)
	return f.err
}

func (f *fakeSearcher) Find(_ context.Context, args FindArgs) (render.Find, error) {
	f.mu.Lock()
	f.last.find = args
	f.mu.Unlock()
	if err := f.enter(); err != nil {
		return render.Find{}, err
	}
	return f.find, nil
}

func (f *fakeSearcher) Turns(_ context.Context, args TurnsArgs) (render.Turns, error) {
	f.mu.Lock()
	f.last.turns = args
	f.mu.Unlock()
	if err := f.enter(); err != nil {
		return render.Turns{}, err
	}
	return f.turns, nil
}

func (f *fakeSearcher) Show(_ context.Context, args ShowArgs) (render.Show, error) {
	f.mu.Lock()
	f.last.show = args
	f.mu.Unlock()
	if err := f.enter(); err != nil {
		return render.Show{}, err
	}
	return f.show, nil
}

func (f *fakeSearcher) When(_ context.Context, args WhenArgs) (render.When, error) {
	f.mu.Lock()
	f.last.when = args
	f.mu.Unlock()
	if err := f.enter(); err != nil {
		return render.When{}, err
	}
	return f.when, nil
}

func (f *fakeSearcher) Guide(context.Context) (string, error) {
	if err := f.enter(); err != nil {
		return "", err
	}
	return f.guide, nil
}

// Preamble does not go through enter: it is answered by the middleware, not
// by a serialized tool call, and counting it there would misreport the
// one-query-at-a-time invariant enter exists to check.
func (f *fakeSearcher) Preamble(context.Context) (string, error) {
	return f.preamble, nil
}

// serve builds a server around fake and drives it through a real in-process
// client, so every assertion below reads what a client would actually receive.
func serve(t *testing.T, fake *fakeSearcher) *mcpsdk.ClientSession {
	t.Helper()
	s, err := NewServer(Options{Version: "0.0.0-test", Searcher: fake})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return connect(t, s)
}

func connect(t *testing.T, s *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverSide, clientSide := mcpsdk.NewInMemoryTransports()
	ss, err := s.Connect(ctx, serverSide, nil)
	if err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "0"}, nil).
		Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listTools(t *testing.T, cs *mcpsdk.ClientSession) *mcpsdk.ListToolsResult {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return res
}

func callTool(t *testing.T, cs *mcpsdk.ClientSession, name string, args any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

// structuredInto decodes structuredContent back into the view type the tool
// declares, which is also a check that the wire form round trips.
func structuredInto(t *testing.T, res *mcpsdk.CallToolResult, into any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatal("structuredContent is absent")
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("re-marshalling structuredContent: %v", err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("decoding structuredContent: %v", err)
	}
}

// soleText is the call's one content block: the compatible JSON text block on
// a success, the error's own message on a failure.
func soleText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("want one content block, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("want text content, got %T", res.Content[0])
	}
	return text.Text
}

// The five names, in the order tools/list must return them.
var wantToolNames = []string{"recall_find", "recall_guide", "recall_show", "recall_turns", "recall_when"}

func TestListToolsReturnsTheFiveRecallToolsInSortedOrder(t *testing.T) {
	res := listTools(t, serve(t, &fakeSearcher{}))

	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	if !slices.Equal(got, wantToolNames) {
		t.Errorf("tools/list returned %v, want %v", got, wantToolNames)
	}
}

func TestListToolsCarriesAnHourLongPrivateCacheHint(t *testing.T) {
	res := listTools(t, serve(t, &fakeSearcher{}))

	if res.TTLMs != 3600000 {
		t.Errorf("ttlMs = %d, want 3600000", res.TTLMs)
	}
	if res.CacheScope != "private" {
		t.Errorf("cacheScope = %q, want %q", res.CacheScope, "private")
	}
}

func TestEveryToolIsAnnotatedReadOnlyAndClosedWorld(t *testing.T) {
	res := listTools(t, serve(t, &fakeSearcher{}))

	for _, tool := range res.Tools {
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}
		if !a.ReadOnlyHint {
			t.Errorf("%s: readOnlyHint = false, want true", tool.Name)
		}
		// Both of these default to true when omitted, so an absent field is
		// the bug, not merely a wrong value.
		if a.DestructiveHint == nil {
			t.Errorf("%s: destructiveHint absent; it defaults to true", tool.Name)
		} else if *a.DestructiveHint {
			t.Errorf("%s: destructiveHint = true, want false", tool.Name)
		}
		if a.OpenWorldHint == nil {
			t.Errorf("%s: openWorldHint absent; it defaults to true", tool.Name)
		} else if *a.OpenWorldHint {
			t.Errorf("%s: openWorldHint = true, want false", tool.Name)
		}
		// The archive refreshes on each call, so two identical calls can
		// legitimately differ.
		if a.IdempotentHint {
			t.Errorf("%s: idempotentHint = true, but a call can refresh the archive", tool.Name)
		}
	}
}

func TestEveryToolDeclaresAnOutputSchemaNamingItsOwnAnswer(t *testing.T) {
	// One property that only that tool's answer carries, so a schema copied
	// from the wrong type fails here instead of passing a non-empty check.
	wantProperty := map[string]string{
		"recall_find":  "sessions",
		"recall_guide": "text",
		"recall_show":  "windows",
		"recall_turns": "passages",
		"recall_when":  "buckets",
	}

	res := listTools(t, serve(t, &fakeSearcher{}))
	for _, tool := range res.Tools {
		props := schemaProperties(t, tool.Name+" output", tool.OutputSchema)
		if _, ok := props[wantProperty[tool.Name]]; !ok {
			t.Errorf("%s: output schema has no %q property; it has %v",
				tool.Name, wantProperty[tool.Name], propertyNames(props))
		}
		// The coverage footer is the honesty contract, so every searching
		// answer must declare it. The guide does not search.
		if _, ok := props["coverage"]; !ok && tool.Name != "recall_guide" {
			t.Errorf("%s: output schema has no coverage property", tool.Name)
		}
	}
}

func schemaProperties(t *testing.T, what string, s any) map[string]any {
	t.Helper()
	if s == nil {
		t.Fatalf("%s: schema is absent", what)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("%s: marshalling schema: %v", what, err)
	}
	var decoded struct {
		Type       string                     `json:"type"`
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("%s: decoding schema: %v", what, err)
	}
	if decoded.Type != "object" {
		t.Errorf("%s: schema type = %q, want object", what, decoded.Type)
	}
	out := make(map[string]any, len(decoded.Properties))
	for name, raw := range decoded.Properties {
		var prop map[string]any
		if err := json.Unmarshal(raw, &prop); err != nil {
			t.Fatalf("%s: decoding property %q: %v", what, name, err)
		}
		out[name] = prop
	}
	return out
}

func schemaRequired(t *testing.T, what string, s any) []string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("%s: marshalling schema: %v", what, err)
	}
	var decoded struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("%s: decoding schema: %v", what, err)
	}
	return decoded.Required
}

func propertyNames(props map[string]any) []string {
	out := make([]string, 0, len(props))
	for name := range props {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func toolNamed(t *testing.T, res *mcpsdk.ListToolsResult, name string) *mcpsdk.Tool {
	t.Helper()
	for _, tool := range res.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no tool named %s", name)
	return nil
}

func description(t *testing.T, props map[string]any, name string) string {
	t.Helper()
	prop, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("no %q property in %v", name, propertyNames(props))
	}
	text, _ := prop["description"].(string)
	return text
}

func TestSearchingToolsTakeTheCLIsFlagsWithOnlyTheQueryRequired(t *testing.T) {
	res := listTools(t, serve(t, &fakeSearcher{}))

	// Every flag a searching verb binds, in its wire spelling.
	want := []string{
		"query", "repo", "all", "results", "tools", "exact", "all_terms", "not",
		"limit", "hits", "sort", "author", "branch", "agent", "session", "since",
		"until", "mine", "brief", "include_self", "include_recall", "no_update",
		"budget", "provider",
	}
	for _, name := range []string{"recall_find", "recall_turns", "recall_when"} {
		in := toolNamed(t, res, name).InputSchema
		props := schemaProperties(t, name+" input", in)
		for _, field := range want {
			if _, ok := props[field]; !ok {
				t.Errorf("%s: input schema has no %q property", name, field)
			}
		}
		if got := schemaRequired(t, name+" input", in); !slices.Equal(got, []string{"query"}) {
			t.Errorf("%s: required = %v, want [query]", name, got)
		}
	}
}

// The jsonschema tag is meant to be the flag's own help string so the schema
// and `recall <verb> --help` cannot drift. These expected strings are copied
// from cmd/recall, not from this package.
func TestInputSchemaDescriptionsAreTheCLIsOwnHelpStrings(t *testing.T) {
	props := schemaProperties(t, "recall_find input",
		toolNamed(t, listTools(t, serve(t, &fakeSearcher{})), "recall_find").InputSchema)

	for field, want := range map[string]string{
		"all":       "search every repo on the machine, not just this one",
		"exact":     "match terms literally, without stem expansion",
		"all_terms": "require every term, returning nothing rather than the best partial match",
		"hits":      "most matched turns to show per session",
		"since":     "only turns at or after this: 2w, 3d, 12h or a date",
		"no_update": "search the archive as it stands, skipping the refresh from disk",
	} {
		if got := description(t, props, field); got != want {
			t.Errorf("%s description = %q, want %q", field, got, want)
		}
	}
}

// provider selects whose transcripts are searched; agent filters turns inside
// them. A caller reading only the schema has to be able to tell them apart.
func TestProviderAndAgentDescriptionsDistinguishThemselvesFromEachOther(t *testing.T) {
	props := schemaProperties(t, "recall_find input",
		toolNamed(t, listTools(t, serve(t, &fakeSearcher{})), "recall_find").InputSchema)

	provider := description(t, props, "provider")
	if !strings.Contains(provider, "agent") {
		t.Errorf("provider description does not mention agent: %q", provider)
	}
	if !strings.Contains(provider, "corpus") {
		t.Errorf("provider description does not say it picks the corpus: %q", provider)
	}
	agent := description(t, props, "agent")
	if !strings.Contains(agent, "subagent") {
		t.Errorf("agent description does not say it matches a subagent name: %q", agent)
	}
	if !strings.Contains(agent, "provider") {
		t.Errorf("agent description does not distinguish itself from provider: %q", agent)
	}
}

func TestShowTakesASessionAndNothingElseIsRequired(t *testing.T) {
	res := listTools(t, serve(t, &fakeSearcher{}))
	in := toolNamed(t, res, "recall_show").InputSchema

	props := schemaProperties(t, "recall_show input", in)
	for _, field := range []string{"session", "query", "turn", "full", "around", "chars", "results", "tools", "no_update", "provider"} {
		if _, ok := props[field]; !ok {
			t.Errorf("recall_show: input schema has no %q property", field)
		}
	}
	if got := schemaRequired(t, "recall_show input", in); !slices.Equal(got, []string{"session"}) {
		t.Errorf("recall_show required = %v, want [session]", got)
	}
}

func TestGuideTakesNoArguments(t *testing.T) {
	res := listTools(t, serve(t, &fakeSearcher{}))
	props := schemaProperties(t, "recall_guide input", toolNamed(t, res, "recall_guide").InputSchema)
	if len(props) != 0 {
		t.Errorf("recall_guide takes %v, want no arguments", propertyNames(props))
	}
}

func TestASuccessfulFindReturnsTheSearchersOwnAnswer(t *testing.T) {
	fake := &fakeSearcher{find: render.Find{
		Verb:  "find",
		Query: "agvtool",
		Hits:  7,
		Sessions: []render.Session{
			{ID: "5fd86b00-1111", Repo: "github.com/mayberuk/recall", Hits: 7},
		},
		Coverage: render.Coverage{
			Sessions:         12,
			SessionsSearched: 9,
			Unsearched:       []schema.Tier{schema.TierResult},
			LiveFromAt:       "2026-06-01T00:00:00Z",
		},
	}}

	res := callTool(t, serve(t, fake), "recall_find", FindArgs{
		SearchArgs: SearchArgs{Query: "agvtool", All: true, Limit: 3},
	})
	if res.IsError {
		t.Fatalf("IsError = true, content: %s", soleText(t, res))
	}

	var got render.Find
	structuredInto(t, res, &got)
	if got.Query != "agvtool" {
		t.Errorf("query = %q, want %q", got.Query, "agvtool")
	}
	if got.Hits != 7 {
		t.Errorf("hits = %d, want 7", got.Hits)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "5fd86b00-1111" {
		t.Errorf("sessions = %+v, want the one session 5fd86b00-1111", got.Sessions)
	}
	// The coverage footer is the whole honesty contract; it has to survive the
	// second envelope intact.
	if got.Coverage.SessionsSearched != 9 || got.Coverage.Sessions != 12 {
		t.Errorf("coverage = %d of %d sessions, want 9 of 12",
			got.Coverage.SessionsSearched, got.Coverage.Sessions)
	}
	if !slices.Equal(got.Coverage.Unsearched, []schema.Tier{schema.TierResult}) {
		t.Errorf("coverage unsearched = %v, want [%s]", got.Coverage.Unsearched, schema.TierResult)
	}
	if got.Coverage.LiveFromAt != "2026-06-01T00:00:00Z" {
		t.Errorf("coverage live_from = %q, want 2026-06-01T00:00:00Z", got.Coverage.LiveFromAt)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.last.find.Query != "agvtool" || !fake.last.find.All || fake.last.find.Limit != 3 {
		t.Errorf("searcher received %+v, want query agvtool, all true, limit 3", fake.last.find)
	}
}

func TestTheCompatibilityTextBlockCarriesTheSameAnswer(t *testing.T) {
	fake := &fakeSearcher{find: render.Find{Query: "bitrise", Hits: 2}}
	res := callTool(t, serve(t, fake), "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "bitrise"}})

	var fromText render.Find
	if err := json.Unmarshal([]byte(soleText(t, res)), &fromText); err != nil {
		t.Fatalf("the text block is not the answer as JSON: %v", err)
	}
	if fromText.Query != "bitrise" || fromText.Hits != 2 {
		t.Errorf("text block carried %+v, want query bitrise with 2 hits", fromText)
	}
}

func TestEveryToolsAnswerCarriesTheArgumentsItWasGiven(t *testing.T) {
	fake := &fakeSearcher{
		turns: render.Turns{Verb: "turns", Query: "why bitrise",
			Passages: []render.Passage{{Cite: "5fd86b00:a1db2039", Text: "we picked bitrise"}}},
		show: render.Show{Verb: "show", Session: "5fd86b00", Anchor: render.AnchorTail,
			Windows: []render.Window{{From: 0, To: 1, Turns: []render.Turn{{Index: 0, Text: "hello"}}}}},
		when: render.When{Verb: "when", Query: "codepush", First: "2026-01-02",
			Buckets: []render.Bucket{{Month: "2026-01", Hits: 4, Sessions: 2}}},
		guide: "recall — what was said in any past session",
	}
	cs := serve(t, fake)

	turnsRes := callTool(t, cs, "recall_turns", TurnsArgs{
		SearchArgs: SearchArgs{Query: "why bitrise"}, Chars: ptr(400),
	})
	var turns render.Turns
	structuredInto(t, turnsRes, &turns)
	if len(turns.Passages) != 1 || turns.Passages[0].Cite != "5fd86b00:a1db2039" {
		t.Errorf("turns passages = %+v, want the one cite 5fd86b00:a1db2039", turns.Passages)
	}

	showRes := callTool(t, cs, "recall_show", ShowArgs{Session: "5fd86b00", Chars: 2000, Full: true})
	var show render.Show
	structuredInto(t, showRes, &show)
	if show.Session != "5fd86b00" || len(show.Windows) != 1 || show.Windows[0].To != 1 {
		t.Errorf("show = %+v, want session 5fd86b00 with one window ending at 1", show)
	}

	whenRes := callTool(t, cs, "recall_when", WhenArgs{SearchArgs: SearchArgs{Query: "codepush"}})
	var when render.When
	structuredInto(t, whenRes, &when)
	if when.First != "2026-01-02" || len(when.Buckets) != 1 || when.Buckets[0].Month != "2026-01" {
		t.Errorf("when = %+v, want first 2026-01-02 and the 2026-01 bucket", when)
	}

	guideRes := callTool(t, cs, "recall_guide", GuideArgs{})
	var guide GuideResult
	structuredInto(t, guideRes, &guide)
	if guide.Text != fake.guide {
		t.Errorf("guide text = %q, want %q", guide.Text, fake.guide)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.last.turns.Chars == nil || *fake.last.turns.Chars != 400 {
		t.Errorf("turns chars = %v, want 400", fake.last.turns.Chars)
	}
	if fake.last.show.Chars != 2000 || !fake.last.show.Full {
		t.Errorf("show args = %+v, want chars 2000 and full true", fake.last.show)
	}
	if fake.last.when.Query != "codepush" {
		t.Errorf("when query = %q, want codepush", fake.last.when.Query)
	}
}

// A zero-valued view marshals its untagged slices as null. The inferred schema
// admits null for a slice field, so this must pass without any omitempty
// gymnastics in internal/render — assert it rather than assume it.
func TestAZeroValuedAnswerValidatesAgainstItsOwnOutputSchema(t *testing.T) {
	cs := serve(t, &fakeSearcher{})
	for name, args := range map[string]any{
		"recall_find":  FindArgs{SearchArgs: SearchArgs{Query: "x"}},
		"recall_guide": GuideArgs{},
		"recall_show":  ShowArgs{Session: "x"},
		"recall_turns": TurnsArgs{SearchArgs: SearchArgs{Query: "x"}},
		"recall_when":  WhenArgs{SearchArgs: SearchArgs{Query: "x"}},
	} {
		res := callTool(t, cs, name, args)
		if res.IsError {
			t.Errorf("%s: IsError = true on a zero-valued answer, content: %s", name, soleText(t, res))
		}
	}
}

// A search that matched nothing is an answer, not a failure. The coverage
// block carries the terms-nearby survey that turns a dead end into the next
// query, and an error result would throw it away.
func TestNoHitsIsASuccessfulAnswerCarryingTheNextQuery(t *testing.T) {
	fake := &fakeSearcher{find: render.Find{
		Verb:  "find",
		Query: "agvtool",
		Terms: []render.Term{{Term: "agvtool", Turns: 0, Nearby: []string{"agvtools", "xcodebuild"}}},
		Coverage: render.Coverage{
			Sessions:         12,
			SessionsSearched: 12,
			Query:            render.Query{Terms: []string{"agvtool"}, Required: 1, Total: 1},
		},
	}}

	res := callTool(t, serve(t, fake), "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "agvtool"}})
	if res.IsError {
		t.Fatalf("IsError = true on a no-hits search, content: %s", soleText(t, res))
	}

	var got render.Find
	structuredInto(t, res, &got)
	if len(got.Sessions) != 0 {
		t.Errorf("sessions = %+v, want none", got.Sessions)
	}
	if len(got.Terms) != 1 || !slices.Equal(got.Terms[0].Nearby, []string{"agvtools", "xcodebuild"}) {
		t.Errorf("terms = %+v, want agvtool with nearby agvtools, xcodebuild", got.Terms)
	}
	if got.Coverage.SessionsSearched != 12 {
		t.Errorf("coverage searched %d sessions, want 12", got.Coverage.SessionsSearched)
	}
}

// The negative control for the case above: a real failure does become an error
// result, and carries its own message so a model can read it and correct.
func TestASearcherErrorBecomesAnErrorResultCarryingItsMessage(t *testing.T) {
	fake := &fakeSearcher{err: errors.New("there is no archive to search yet; run without no_update to build one")}

	res := callTool(t, serve(t, fake), "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "agvtool"}})
	if !res.IsError {
		t.Fatal("IsError = false on a searcher failure")
	}
	want := "there is no archive to search yet; run without no_update to build one"
	if got := soleText(t, res); got != want {
		t.Errorf("error content = %q, want %q", got, want)
	}
	if res.StructuredContent != nil {
		t.Errorf("structuredContent = %v on an error result, want none", res.StructuredContent)
	}
}

// Two calls that arrive together must not overlap: the archive refresh under a
// search writes to disk, and two of those in one process is not a supported
// state.
func TestConcurrentCallsRunOneAtATime(t *testing.T) {
	const (
		callers = 8
		dwell   = 20 * time.Millisecond
	)
	fake := &fakeSearcher{dwell: dwell}
	cs := serve(t, fake)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      "recall_find",
				Arguments: FindArgs{SearchArgs: SearchArgs{Query: "q"}},
			})
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
				return
			}
			if res.IsError {
				t.Errorf("caller %d: IsError = true", i)
			}
		}()
	}
	began := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(began)

	if got := fake.calls.Load(); got != callers {
		t.Fatalf("the searcher was called %d times, want %d", got, callers)
	}
	if got := fake.overlaps.Load(); got != 0 {
		t.Errorf("the searcher was re-entered %d times while a call was in flight", got)
	}
	// Serialised, callers * dwell is a floor. Concurrent dispatch would finish
	// in roughly one dwell, so this fails on overlap the detector above races
	// past.
	if floor := callers * dwell; elapsed < floor {
		t.Errorf("%d calls of %s finished in %s, which is less than the %s they take one at a time",
			callers, dwell, elapsed, floor)
	}
}

// ptr is for the argument fields whose zero a caller means, which are
// pointers so the wire can tell that zero from an absence.
func ptr[T any](v T) *T { return &v }
