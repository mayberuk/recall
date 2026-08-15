package strip

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// stripFile runs a whole fixture file through one Stripper and returns the turns
// in file order, so a golden case can compare the pass rather than one record.
func stripFile(t *testing.T, c fixtures.Corpus, rel string) ([]schema.Turn, *Stripper) {
	t.Helper()
	s := New()
	return stripInto(t, s, c, rel), s
}

func stripInto(t *testing.T, s *Stripper, c fixtures.Corpus, rel string) []schema.Turn {
	t.Helper()
	r, err := jsonl.Open(c.Path(rel))
	if err != nil {
		t.Fatalf("open %s: %v", rel, err)
	}
	defer r.Close()

	var out []schema.Turn
	for r.Next() {
		rec, ok := r.Record()
		if !ok {
			t.Fatalf("%s: line at offset %d did not parse", rel, r.Line().Offset)
		}
		turns, produced := s.Strip(rec)
		if produced != (len(turns) > 0) {
			t.Fatalf("%s: Strip reported produced=%v with %d turns", rel, produced, len(turns))
		}
		out = append(out, turns...)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return out
}

// want is one expected stripped turn. Every value derives from the fixture's own
// JSON, checked against the tier and author rules in docs/design.md (Decisions,
// Human-turn discrimination).
type want struct {
	uuid   string
	tier   schema.Tier
	author schema.Author
	text   string
}

func check(t *testing.T, got []schema.Turn, wants []want) {
	t.Helper()
	if len(got) != len(wants) {
		t.Fatalf("got %d turns, want %d\n%s", len(got), len(wants), format(got))
	}
	for i, w := range wants {
		g := got[i]
		if g.UUID != w.uuid || g.Tier != w.tier || g.Author != w.author || g.Text != w.text {
			t.Errorf("turn %d:\n got uuid=%s tier=%s author=%s text=%q\nwant uuid=%s tier=%s author=%s text=%q",
				i, g.UUID, g.Tier, g.Author, g.Text, w.uuid, w.tier, w.author, w.text)
		}
	}
}

func format(turns []schema.Turn) string {
	var b strings.Builder
	for i, tn := range turns {
		b.WriteString(strings.TrimSpace(strings.Join([]string{
			"  ", itoa(i), tn.UUID, string(tn.Tier), string(tn.Author), truncate(tn.Text),
		}, " ")))
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(i int) string { return string(rune('0' + i%10)) }

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func TestGoldenNeedleFile(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileNeedle)

	check(t, got, []want{
		{"aaaaaaaa-0000-4000-8000-000000000001", schema.TierConversation, schema.AuthorHuman,
			"Look into the checkout retry handling and tell me what we decided."},
		{"aaaaaaaa-0000-4000-8000-000000000002", schema.TierConversation, schema.AuthorAssistant,
			"The flimberdash path is the one that was rewritten last month, so start there.\n\n" +
				"We settled on the quixotrope adapter because it keeps retries idempotent."},
		{"aaaaaaaa-0000-4000-8000-000000000003", schema.TierInvocation, schema.AuthorAssistant,
			"Bash rg -n grimbleflax internal/"},
		{"aaaaaaaa-0000-4000-8000-000000000004", schema.TierResult, schema.AuthorSystem,
			"internal/checkout/adapter.go:42:  // retry guard is idempotent\n"},
		{"aaaaaaaa-0000-4000-8000-000000000005", schema.TierConversation, schema.AuthorHuman,
			"/compact and keep the adapter decision in the summary"},
		{"aaaaaaaa-0000-4000-8000-000000000006", schema.TierConversation, schema.AuthorSystem,
			"<command-name>/plugin</command-name>\n            <command-message>plugin</command-message>\n            <command-args></command-args>"},
		{"aaaaaaaa-0000-4000-8000-000000000007", schema.TierConversation, schema.AuthorHuman,
			"Good. Write that down before we lose it."},
	})

	for _, turn := range got {
		if turn.Session != fixtures.SessNeedle {
			t.Errorf("turn %s: session %q, want %q", turn.UUID, turn.Session, fixtures.SessNeedle)
		}
		if turn.Branch != "main" {
			t.Errorf("turn %s: branch %q, want main", turn.UUID, turn.Branch)
		}
		if turn.CWD != c.ScratchPath(fixtures.ScratchNormal) {
			t.Errorf("turn %s: cwd %q, want %q", turn.UUID, turn.CWD, c.ScratchPath(fixtures.ScratchNormal))
		}
		if turn.TS == "" {
			t.Errorf("turn %s: empty timestamp", turn.UUID)
		}
		if turn.Repo != "" {
			t.Errorf("turn %s: repo %q, but internal/repo fills Repo", turn.UUID, turn.Repo)
		}
	}
}

// The signature is 97% of the thinking bytes and carries no words. A thinking
// block with no text must leave nothing behind but the prose beside it.
func TestGoldenEmptyThinking(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileEmptyThinking)

	check(t, got, []want{
		{"66666666-0000-4000-8000-000000000001", schema.TierConversation, schema.AuthorHuman,
			"Think about this one."},
		{"66666666-0000-4000-8000-000000000002", schema.TierConversation, schema.AuthorAssistant,
			"Most reasoning is not persisted, only its signature."},
	})
	for _, turn := range got {
		if strings.Contains(turn.Text, "CAISlwIKhwEIEBgC") {
			t.Errorf("turn %s carries thinking signature bytes", turn.UUID)
		}
	}
}

func TestGoldenSubagent(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileSubagent)

	check(t, got, []want{
		{"bbbbbbbb-0000-4000-8000-000000000001", schema.TierConversation, schema.AuthorAgent,
			"Explore the checkout retry path and report back."},
		{"bbbbbbbb-0000-4000-8000-000000000002", schema.TierConversation, schema.AuthorAgent,
			"The snorplewick guard is what makes the retry safe to repeat."},
	})
	for _, turn := range got {
		if turn.Agent != "b1c2d3e4f50617289" {
			t.Errorf("turn %s: agent %q, want the agentId", turn.UUID, turn.Agent)
		}
		if turn.Session != fixtures.SessNeedle {
			t.Errorf("turn %s: session %q, want the parent session %q", turn.UUID, turn.Session, fixtures.SessNeedle)
		}
	}
}

func TestGoldenNoPromptSource(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileNoPromptSource)

	check(t, got, []want{
		{"55555555-0000-4000-8000-000000000001", schema.TierConversation, schema.AuthorSystem,
			"This session is being continued from a previous conversation that ran out of context."},
		{"55555555-0000-4000-8000-000000000002", schema.TierConversation, schema.AuthorSystem,
			"<local-command-stdout>build succeeded</local-command-stdout>"},
		{"55555555-0000-4000-8000-000000000003", schema.TierConversation, schema.AuthorAssistant,
			"Continuing from the earlier context."},
	})
}

func TestGoldenRelocatedFile(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileRelocated)

	check(t, got, []want{
		{"22222222-0000-4000-8000-000000000001", schema.TierConversation, schema.AuthorHuman,
			"This checkout no longer exists on disk."},
		{"22222222-0000-4000-8000-000000000002", schema.TierConversation, schema.AuthorAssistant,
			"Its directory was removed after the session ended."},
	})
	for _, turn := range got {
		if turn.CWD != c.ScratchPath(fixtures.ScratchGone) {
			t.Errorf("turn %s: cwd %q, want %q", turn.UUID, turn.CWD, c.ScratchPath(fixtures.ScratchGone))
		}
	}
}

// One file carries two sessions, so the session must come off the record.
func TestGoldenMultiSession(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileMultiSession)

	wantSessions := []string{
		fixtures.SessMultiFirst, fixtures.SessMultiFirst,
		fixtures.SessMultiSecond, fixtures.SessMultiSecond,
	}
	if len(got) != len(wantSessions) {
		t.Fatalf("got %d turns, want %d\n%s", len(got), len(wantSessions), format(got))
	}
	for i, want := range wantSessions {
		if got[i].Session != want {
			t.Errorf("turn %d: session %q, want %q", i, got[i].Session, want)
		}
	}
}

// A tool result is stored whole. Head/tail truncation was rejected because it
// indexes only ~19% of tool-result text by bytes.
func TestHugeResultKeptWhole(t *testing.T) {
	c := fixtures.Materialize(t)
	got, _ := stripFile(t, c, fixtures.FileHugeResult)

	var results []schema.Turn
	for _, turn := range got {
		if turn.Tier == schema.TierResult {
			results = append(results, turn)
		}
	}
	if len(results) != 1 {
		t.Fatalf("got %d result turns, want 1\n%s", len(results), format(got))
	}
	if n := len(results[0].Text); n != c.Manifest.HugeResultBytes {
		t.Errorf("result text is %d bytes, want %d — nothing may be truncated", n, c.Manifest.HugeResultBytes)
	}
	if !strings.Contains(results[0].Text, fixtures.NeedleResult) {
		t.Errorf("result turn lost the planted token %q", fixtures.NeedleResult)
	}
}

// An unrecognised record type must not crash, must not take the turn beside it
// down with it, and must reach doctor as a count.
func TestUnknownTypesSurfaceAndDoNotDrop(t *testing.T) {
	c := fixtures.Materialize(t)
	got, s := stripFile(t, c, fixtures.FileUnknownType)

	check(t, got, []want{
		{"44444444-0000-4000-8000-000000000001", schema.TierConversation, schema.AuthorHuman,
			"A future release wrote records we have never seen."},
		{"44444444-0000-4000-8000-000000000002", schema.TierConversation, schema.AuthorAssistant,
			"The turn after the unknown records is still indexed."},
	})

	unknown := s.Observation().Tally.Unknown
	for typ, want := range c.Manifest.UnknownTypes {
		if unknown[typ] != want {
			t.Errorf("unknown type %q counted %d times, want %d", typ, unknown[typ], want)
		}
	}
	if len(unknown) != len(c.Manifest.UnknownTypes) {
		t.Errorf("counted unknown types %v, want %v", unknown, c.Manifest.UnknownTypes)
	}
}

func TestAuthorRules(t *testing.T) {
	cases := []struct {
		name string
		line string
		want schema.Author
	}{
		{
			"typed is the human rule",
			`{"type":"user","uuid":"u1","promptSource":"typed","message":{"role":"user","content":"ship it"}}`,
			schema.AuthorHuman,
		},
		{
			"sidechain is never the operator, whatever else it says",
			`{"type":"user","uuid":"u2","isSidechain":true,"promptSource":"typed","message":{"role":"user","content":"ship it"}}`,
			schema.AuthorAgent,
		},
		{
			"an assistant inside a subagent transcript is the agent",
			`{"type":"assistant","uuid":"u3","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
			schema.AuthorAgent,
		},
		{
			"an sdk prompt is machine text, not the operator",
			`{"type":"user","uuid":"u4","promptSource":"sdk","message":{"role":"user","content":"Explore the retry path"}}`,
			schema.AuthorSystem,
		},
		{
			"a queued prompt is machine text",
			`{"type":"user","uuid":"u5","promptSource":"queued","message":{"role":"user","content":"and then deploy"}}`,
			schema.AuthorSystem,
		},
		{
			"prose with no label is machine text — content shape is refuted",
			`{"type":"user","uuid":"u6","message":{"role":"user","content":"[Request interrupted by user]"}}`,
			schema.AuthorSystem,
		},
		{
			"a compact continuation summary is machine text",
			`{"type":"user","uuid":"u7","message":{"role":"user","content":"This session is being continued from a previous conversation."}}`,
			schema.AuthorSystem,
		},
		{
			"slash-command arguments carry typed words",
			`{"type":"user","uuid":"u8","message":{"role":"user","content":"<command-name>/livecheck:run</command-name><command-args>on an android device</command-args>"}}`,
			schema.AuthorHuman,
		},
		{
			"a slash command with no arguments is the wrapper alone",
			`{"type":"user","uuid":"u9","message":{"role":"user","content":"<command-name>/compact</command-name><command-args></command-args>"}}`,
			schema.AuthorSystem,
		},
		{
			"a tool result is machine text",
			`{"type":"user","uuid":"u10","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
			schema.AuthorSystem,
		},
		{
			"an assistant record is the assistant",
			`{"type":"assistant","uuid":"u11","message":{"role":"assistant","content":[{"type":"text","text":"here"}]}}`,
			schema.AuthorAssistant,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turns := stripLine(t, tc.line)
			if len(turns) != 1 {
				t.Fatalf("got %d turns, want 1", len(turns))
			}
			if turns[0].Author != tc.want {
				t.Errorf("author %q, want %q", turns[0].Author, tc.want)
			}
		})
	}
}

// The command name was typed too, so it stays searchable; the XML scaffolding
// and the command message were not typed, so they go.
func TestCommandTurnKeepsTheNameAndDropsTheScaffolding(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"u1","message":{"role":"user","content":"<command-name>/atlas</command-name>\n<command-message>atlas</command-message>\n<command-args>look at the cc-plugins repo</command-args>"}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	got := turns[0].Text
	if got != "/atlas look at the cc-plugins repo" {
		t.Errorf("text %q, want the command name followed by its arguments", got)
	}
	for _, tag := range []string{"<command-name>", "</command-name>", "<command-args>", "</command-args>", "<command-message>", "atlas</command-message>"} {
		if strings.Contains(got, tag) {
			t.Errorf("text %q still carries the scaffolding %q", got, tag)
		}
	}
	if turns[0].Author != schema.AuthorHuman {
		t.Errorf("author %q, want human", turns[0].Author)
	}
}

// Searching the command name must reach the turn: that is the false negative
// the name-in-text rule exists to prevent.
func TestCommandNameIsSearchable(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"u2","message":{"role":"user","content":"<command-name>/livecheck:run</command-name><command-message>livecheck:run</command-message><command-args>on an android device to test</command-args>"}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].Text != "/livecheck:run on an android device to test" {
		t.Errorf("text %q, want the whole typed line", turns[0].Text)
	}
	if !strings.Contains(turns[0].Text, "/livecheck:run") {
		t.Error("the command name is not searchable in the turn")
	}
}

// A wrapper with no arguments is not a typed turn, name or no name.
func TestCommandWithNoArgumentsIsNotHuman(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"u3","message":{"role":"user","content":"<command-name>/compact</command-name><command-args></command-args>"}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].Author != schema.AuthorSystem {
		t.Errorf("author %q, want system", turns[0].Author)
	}
	if !strings.Contains(turns[0].Text, "<command-name>") {
		t.Errorf("text %q, want the wrapper kept whole and searchable", turns[0].Text)
	}
}

// The invocation tier is a tool name and its identifying argument. Edit and
// Write inputs carry whole file contents — 65 MB of the corpus.
func TestInvocationNeverCarriesPayload(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			"Bash keeps the command line",
			`{"type":"assistant","uuid":"i1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}`,
			"Bash go test ./...",
		},
		{
			"Read keeps the path",
			`{"type":"assistant","uuid":"i2","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/a.go","limit":40}}]}}`,
			"Read /tmp/a.go",
		},
		{
			"Edit keeps the path and drops both file bodies",
			`{"type":"assistant","uuid":"i3","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/a.go","old_string":"WHOLEFILEBEFORE","new_string":"WHOLEFILEAFTER"}}]}}`,
			"Edit /tmp/a.go",
		},
		{
			"Write keeps the path and drops the content",
			`{"type":"assistant","uuid":"i4","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/a.go","content":"WHOLEFILEBODY"}}]}}`,
			"Write /tmp/a.go",
		},
		{
			"an unknown tool keeps its short arguments, ordered",
			`{"type":"assistant","uuid":"i5","message":{"role":"assistant","content":[{"type":"tool_use","name":"mcp__gateway__pipelines","input":{"project_id":42,"merge_request_iid":7}}]}}`,
			"mcp__gateway__pipelines merge_request_iid=7 project_id=42",
		},
		{
			"two calls in one record become one turn",
			`{"type":"assistant","uuid":"i7","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/a"}},{"type":"tool_use","name":"Read","input":{"file_path":"/b"}}]}}`,
			"Read /a\nRead /b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turns := stripLine(t, tc.line)
			if len(turns) != 1 {
				t.Fatalf("got %d turns, want 1: %v", len(turns), turns)
			}
			if turns[0].Tier != schema.TierInvocation {
				t.Fatalf("tier %q, want invocation", turns[0].Tier)
			}
			if turns[0].Text != tc.want {
				t.Errorf("text %q, want %q", turns[0].Text, tc.want)
			}
		})
	}
}

// contractIdentBytes is the invocation tier's bound in bytes. It is written out
// rather than read from identMax on purpose: a test that measures itself against
// the constant moves with it, so setting identMax to 100000 would still pass and
// a 100 KB tool body could enter a tier whose whole point is dropping payloads.
const contractIdentBytes = 256

func TestInvocationArgumentBoundIsAbsolute(t *testing.T) {
	atBound := strings.Repeat("K", contractIdentBytes)
	overBound := strings.Repeat("D", contractIdentBytes+1)
	call := func(arg string) []schema.Turn {
		return stripLine(t, `{"type":"assistant","uuid":"b1","message":{"role":"assistant","content":[{"type":"tool_use","name":"mcp__x__run","input":{"id":"a1","body":"`+arg+`"}}]}}`)
	}

	kept := call(atBound)
	if len(kept) != 1 {
		t.Fatalf("got %d turns, want 1", len(kept))
	}
	if want := "mcp__x__run body=" + atBound + " id=a1"; kept[0].Text != want {
		t.Errorf("an argument of exactly %d bytes was not kept whole:\n got %q\nwant %q",
			contractIdentBytes, kept[0].Text, want)
	}

	dropped := call(overBound)
	if len(dropped) != 1 {
		t.Fatalf("got %d turns, want 1", len(dropped))
	}
	if dropped[0].Text != "mcp__x__run id=a1" {
		t.Errorf("an argument of %d bytes reached the invocation tier: %q",
			contractIdentBytes+1, dropped[0].Text)
	}
	if strings.Contains(dropped[0].Text, "DD") {
		t.Errorf("the oversize argument leaked into %q", dropped[0].Text)
	}
}

// A payload-shaped argument is what the bound exists for: Edit and Write inputs
// carry whole file contents, 65 MB of the corpus.
func TestInvocationNeverCarriesAPayloadSizedArgument(t *testing.T) {
	body := strings.Repeat("P", 100000)
	for _, line := range []string{
		`{"type":"assistant","uuid":"p1","message":{"role":"assistant","content":[{"type":"tool_use","name":"mcp__x__run","input":{"id":"a1","body":"` + body + `"}}]}}`,
		`{"type":"assistant","uuid":"p2","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{"file_path":"/tmp/a.go","content":"` + body + `"}}]}}`,
		`{"type":"assistant","uuid":"p3","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/a.go","old_string":"` + body + `","new_string":"` + body + `"}}]}}`,
	} {
		turns := stripLine(t, line)
		if len(turns) != 1 {
			t.Fatalf("got %d turns, want 1", len(turns))
		}
		if n := len(turns[0].Text); n > contractIdentBytes {
			t.Errorf("invocation turn is %d bytes, over the %d-byte bound: %.80q",
				n, contractIdentBytes, turns[0].Text)
		}
		if strings.Contains(turns[0].Text, "PP") {
			t.Errorf("payload bytes reached the invocation tier: %.80q", turns[0].Text)
		}
	}
}

// toolUseResult is a second structured copy of the same result — 362 MB of the
// corpus. The result tier reads message.content and never that copy.
func TestResultComesFromMessageNotToolUseResult(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"FROM_MESSAGE"}]},"toolUseResult":{"stdout":"FROM_TOOLUSERESULT","stderr":""}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].Text != "FROM_MESSAGE" {
		t.Errorf("text %q, want the message copy", turns[0].Text)
	}
}

func TestResultBlocksWithNestedContent(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"r2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"first"},{"type":"image","source":{"type":"base64","data":"AAAA"}},{"type":"text","text":"second"}]}]}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].Text != "first\nsecond" {
		t.Errorf("text %q, want the text blocks only", turns[0].Text)
	}
}

// A record may speak in more than one tier. Each tier is one turn, because the
// dedup key downstream is the record uuid.
func TestOneTurnPerTier(t *testing.T) {
	turns := stripLine(t, `{"type":"assistant","uuid":"m1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"weighing it","signature":"SIG"},{"type":"text","text":"the answer"},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`)
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}
	if turns[0].Tier != schema.TierConversation || turns[0].Text != "weighing it\n\nthe answer" {
		t.Errorf("conversation turn is %q / %q", turns[0].Tier, turns[0].Text)
	}
	if turns[1].Tier != schema.TierInvocation || turns[1].Text != "Bash ls" {
		t.Errorf("invocation turn is %q / %q", turns[1].Tier, turns[1].Text)
	}
	seen := map[schema.Tier]bool{}
	for _, turn := range turns {
		if seen[turn.Tier] {
			t.Errorf("tier %q appears twice on uuid %s", turn.Tier, turn.UUID)
		}
		seen[turn.Tier] = true
	}
}

func TestRelocatedRecordSuppliesTheCWD(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"c1","relocatedCwd":"/moved/here","message":{"role":"user","content":"where did it go"}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].CWD != "/moved/here" {
		t.Errorf("cwd %q, want the relocated path", turns[0].CWD)
	}
}

func TestRecordsWithoutWordsProduceNothing(t *testing.T) {
	lines := []string{
		`{"type":"ai-title","sessionId":"s1","aiTitle":"a title"}`,
		`{"type":"system","uuid":"y1","subtype":"turn_duration","durationMs":10}`,
		`{"type":"attachment","uuid":"y2","attachment":{"type":"skill_listing","content":"a listing"}}`,
		`{"type":"relocated","sessionId":"s1","relocatedCwd":"/gone"}`,
		`{"type":"assistant","uuid":"y3","message":{"role":"assistant","content":[]}}`,
		`{"type":"assistant","uuid":"y4","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"SIG"}]}}`,
	}
	for _, line := range lines {
		if turns := stripLine(t, line); len(turns) != 0 {
			t.Errorf("%s produced %d turns, want 0", line, len(turns))
		}
	}
}

// A type this build has never seen, carrying a message, still strips: silence
// there is how a false negative enters through the back door.
func TestUnknownTypeCarryingWordsStillStrips(t *testing.T) {
	turns := stripLine(t, `{"type":"holo-turn","uuid":"z1","message":{"role":"assistant","content":[{"type":"text","text":"a shape from a later release"}]}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].Text != "a shape from a later release" || turns[0].Author != schema.AuthorAssistant {
		t.Errorf("got %q by %q", turns[0].Text, turns[0].Author)
	}
}

func TestVersionToleranceOfAbsentFields(t *testing.T) {
	turns := stripLine(t, `{"type":"user","uuid":"v1","message":{"role":"user","content":"bare"}}`)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	got := turns[0]
	if got.Session != "" || got.TS != "" || got.CWD != "" || got.Branch != "" || got.Agent != "" {
		t.Errorf("absent fields became %#v, want empty rather than an error", got)
	}
	if got.Text != "bare" {
		t.Errorf("text %q, want the turn kept despite the absent fields", got.Text)
	}
}

func stripLine(t *testing.T, line string) []schema.Turn {
	t.Helper()
	turns, _ := New().Strip(parseLine(t, line))
	return turns
}

func parseLine(t *testing.T, line string) jsonl.Record {
	t.Helper()
	rec, ok := jsonl.Parse(jsonl.Line{Bytes: []byte(line), Length: len(line)})
	if !ok {
		t.Fatalf("test line did not parse: %s", line)
	}
	return rec
}
