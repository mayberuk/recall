package archive

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

type fakeProvider struct {
	agent   schema.Agent
	root    string
	rootErr error
}

func (f fakeProvider) Agent() schema.Agent          { return f.agent }
func (f fakeProvider) IsTranscript(rel string) bool { return strings.HasSuffix(rel, ".jsonl") }
func (f fakeProvider) NeedsHead() bool              { return false }
func (f fakeProvider) Decoder(rel string) Decoder   { return fakeDecoder{} }

// Root returns f.root even when f.rootErr is set, so a test can pin an
// existing root against an error path: real code must fall back on the
// error alone, without ever consulting whether that root happens to exist.
func (f fakeProvider) Root() (string, error) {
	return f.root, f.rootErr
}

type fakeDecoder struct{}

func (fakeDecoder) Turns(rec jsonl.Record) ([]schema.Turn, bool) { return nil, false }

// withProviders swaps in a fresh registry built directly from ps, bypassing
// Register so tests can set up conflicting or empty registries without
// tripping its duplicate panic, then restores the real one after the test.
func withProviders(t *testing.T, ps ...Provider) {
	t.Helper()
	orig := providers
	fresh := map[schema.Agent]Provider{}
	for _, p := range ps {
		fresh[p.Agent()] = p
	}
	providers = fresh
	t.Cleanup(func() { providers = orig })
}

// resetSelection isolates Select from both the real environment and any
// explicit spec left over from another test.
func resetSelection(t *testing.T) {
	t.Helper()
	origSpec, origHave := explicitSpec, haveExplicit
	explicitSpec, haveExplicit = "", false
	t.Cleanup(func() { explicitSpec, haveExplicit = origSpec, origHave })

	for _, name := range []string{envRecallAgent, envCodexThreadID, envCodexSessionID, envGeminiCLI, envCursorAgent, envClaudeCode} {
		t.Setenv(name, "")
	}
}

func TestSelectRejectsUnknownExplicitAgentAndSelectsNothing(t *testing.T) {
	resetSelection(t)
	withProviders(t,
		fakeProvider{agent: schema.AgentClaudeCode, root: t.TempDir()},
		fakeProvider{agent: schema.AgentCodex, root: t.TempDir()},
	)
	t.Setenv(envRecallAgent, "cursor")

	sel, err := Select()
	if err == nil {
		t.Fatal("Select() = nil error, want an argument error naming the registered agents")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) {
		t.Fatalf("Select() error is not an fperr.Error: %v", err)
	}
	if coded.Code != fperr.ArgError {
		t.Errorf("Code = %q, want %q", coded.Code, fperr.ArgError)
	}
	for _, name := range []string{"claude-code", "codex"} {
		if !strings.Contains(coded.Msg, name) {
			t.Errorf("error message %q does not name registered agent %q", coded.Msg, name)
		}
	}
	if len(sel.Agents) != 0 {
		t.Errorf("Agents = %v, want none selected on error", sel.Agents)
	}
}

func TestSelectRejectsUnknownExplicitAgentWithEmptyRegistry(t *testing.T) {
	resetSelection(t)
	withProviders(t)
	t.Setenv(envRecallAgent, "codex")

	_, err := Select()
	if err == nil {
		t.Fatal("Select() = nil error, want an argument error")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) {
		t.Fatalf("Select() error is not an fperr.Error: %v", err)
	}
	if !strings.Contains(coded.Msg, "(none registered)") {
		t.Errorf("error message %q does not report an empty registry", coded.Msg)
	}
}

func TestSelectFallsBackWhenDetectedProviderRootMissing(t *testing.T) {
	resetSelection(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	withProviders(t, fakeProvider{agent: schema.AgentCodex, root: missing})
	t.Setenv(envCodexThreadID, "thread-123")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]", sel.Agents)
	}
	if sel.Detected != schema.AgentCodex {
		t.Errorf("Detected = %q, want %q", sel.Detected, schema.AgentCodex)
	}
	if !sel.Fallback {
		t.Error("Fallback = false, want true")
	}
	if !strings.Contains(sel.Reason, "codex") || !strings.Contains(sel.Reason, "does not exist") {
		t.Errorf("Reason = %q, want it to name codex and state that its root does not exist", sel.Reason)
	}
}

func TestSelectFallsBackWhenDetectedProviderRootErrors(t *testing.T) {
	resetSelection(t)
	withProviders(t, fakeProvider{
		agent:   schema.AgentCodex,
		root:    t.TempDir(), // exists, so only the error (not dirExists) can explain the fallback
		rootErr: errors.New("stat: permission denied"),
	})
	t.Setenv(envCodexThreadID, "thread-123")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]", sel.Agents)
	}
	if !sel.Fallback {
		t.Error("Fallback = false, want true: Root() returned an error")
	}
	if sel.Reason == "" {
		t.Error("Reason is empty, want an explanation of the fallback")
	}
}

func TestSelectFallsBackWhenDetectedAgentHasNoProvider(t *testing.T) {
	resetSelection(t)
	withProviders(t)
	t.Setenv(envGeminiCLI, "1")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]", sel.Agents)
	}
	if sel.Detected != schema.AgentGemini {
		t.Errorf("Detected = %q, want %q", sel.Detected, schema.AgentGemini)
	}
	if !sel.Fallback {
		t.Error("Fallback = false, want true")
	}
	if !strings.Contains(sel.Reason, "gemini") || !strings.Contains(sel.Reason, "no provider is registered") {
		t.Errorf("Reason = %q, want it to name gemini and state that no provider is registered", sel.Reason)
	}
}

func TestSelectDetectsCodexWhenRootExists(t *testing.T) {
	resetSelection(t)
	withProviders(t, fakeProvider{agent: schema.AgentCodex, root: t.TempDir()})
	t.Setenv(envCodexThreadID, "thread-123")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentCodex {
		t.Errorf("Agents = %v, want [codex]", sel.Agents)
	}
	if sel.Detected != schema.AgentCodex {
		t.Errorf("Detected = %q, want %q", sel.Detected, schema.AgentCodex)
	}
	if sel.Fallback {
		t.Error("Fallback = true, want false: codex's root exists and a provider is registered")
	}
}

func TestSelectDetectsCodexFromSessionIDAlone(t *testing.T) {
	resetSelection(t)
	withProviders(t, fakeProvider{agent: schema.AgentCodex, root: t.TempDir()})
	t.Setenv(envCodexSessionID, "session-123")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Detected != schema.AgentCodex {
		t.Errorf("Detected = %q, want %q", sel.Detected, schema.AgentCodex)
	}
	if sel.Fallback {
		t.Error("Fallback = true, want false: codex's root exists")
	}
}

func TestSelectDetectsClaudeCodeOnly(t *testing.T) {
	resetSelection(t)
	withProviders(t)
	t.Setenv(envClaudeCode, "1")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]", sel.Agents)
	}
	if sel.Detected != schema.AgentClaudeCode {
		t.Errorf("Detected = %q, want %q", sel.Detected, schema.AgentClaudeCode)
	}
	if sel.Fallback {
		t.Error("Fallback = true, want false: claude-code is the default, not a fallback")
	}
}

func TestSelectDefaultsToClaudeCodeWithNothingDetected(t *testing.T) {
	resetSelection(t)
	withProviders(t)

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]", sel.Agents)
	}
	if sel.Detected != "" {
		t.Errorf("Detected = %q, want empty: nothing was detected", sel.Detected)
	}
}

func TestSelectProbeOrderPrefersCodexOverCursor(t *testing.T) {
	resetSelection(t)
	root := t.TempDir()
	withProviders(t,
		fakeProvider{agent: schema.AgentCodex, root: root},
		fakeProvider{agent: schema.AgentCursor, root: root},
	)
	t.Setenv(envCodexThreadID, "thread-123")
	t.Setenv(envCursorAgent, "1")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Detected != schema.AgentCodex {
		t.Errorf("Detected = %q, want %q: codex is probed before cursor", sel.Detected, schema.AgentCodex)
	}
}

func TestSelectExplicitAgentWithRegisteredProvider(t *testing.T) {
	resetSelection(t)
	withProviders(t, fakeProvider{agent: schema.AgentCodex, root: t.TempDir()})
	t.Setenv(envRecallAgent, "codex")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentCodex {
		t.Errorf("Agents = %v, want [codex]", sel.Agents)
	}
	if sel.Fallback {
		t.Error("Fallback = true, want false for an explicit request that was honored")
	}
}

func TestSelectExplicitAgentIgnoresMissingRoot(t *testing.T) {
	resetSelection(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	withProviders(t, fakeProvider{agent: schema.AgentCodex, root: missing})
	t.Setenv(envRecallAgent, "codex")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentCodex {
		t.Errorf("Agents = %v, want [codex]: an explicit request is honored whatever its root looks like", sel.Agents)
	}
	for _, a := range sel.Agents {
		if a == schema.AgentClaudeCode {
			t.Error("Agents contains claude-code: an explicit request must never be silently swapped for a different agent's corpus")
		}
	}
}

func TestSelectExplicitSpecTakesPriorityOverEnv(t *testing.T) {
	resetSelection(t)
	withProviders(t,
		fakeProvider{agent: schema.AgentClaudeCode, root: t.TempDir()},
		fakeProvider{agent: schema.AgentCodex, root: t.TempDir()},
	)
	t.Setenv(envRecallAgent, "codex")
	if err := SetSelection("claude-code"); err != nil {
		t.Fatalf("SetSelection: %v", err)
	}

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]: SetSelection must win over RECALL_AGENT", sel.Agents)
	}
}

func TestSelectAutoSpecBehavesAsUnset(t *testing.T) {
	resetSelection(t)
	withProviders(t)
	t.Setenv(envRecallAgent, "auto")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]: auto falls through to detection", sel.Agents)
	}
}

func TestSelectAllPicksRegisteredAgentsWhoseRootExists(t *testing.T) {
	resetSelection(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	withProviders(t,
		fakeProvider{agent: schema.AgentClaudeCode, root: t.TempDir()},
		fakeProvider{agent: schema.AgentCodex, root: missing},
	)
	t.Setenv(envRecallAgent, "all")

	sel, err := Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(sel.Agents) != 1 || sel.Agents[0] != schema.AgentClaudeCode {
		t.Errorf("Agents = %v, want [claude-code]: codex's root does not exist", sel.Agents)
	}
}

func TestSetSelectionRejectsEmptySpec(t *testing.T) {
	resetSelection(t)
	if err := SetSelection(""); err == nil {
		t.Fatal("SetSelection(\"\") = nil error, want an argument error")
	}
}

func TestRegisterPanicsOnDuplicateAgent(t *testing.T) {
	withProviders(t)

	Register(fakeProvider{agent: schema.AgentCodex, root: t.TempDir()})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register did not panic on a duplicate agent registration")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "duplicate provider registration") || !strings.Contains(msg, "codex") {
			t.Errorf("panic value = %v, want a message naming duplicate provider registration and codex", r)
		}
	}()
	Register(fakeProvider{agent: schema.AgentCodex, root: t.TempDir()})
}

func TestRegisterPanicsOnNilProvider(t *testing.T) {
	withProviders(t)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register did not panic on a nil provider")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "nil provider") {
			t.Errorf("panic value = %v, want a message naming a nil provider", r)
		}
	}()
	Register(nil)
}

func TestRegisterPanicsOnEmptyAgent(t *testing.T) {
	withProviders(t)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register did not panic on a provider with no agent")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "no agent") {
			t.Errorf("panic value = %v, want a message naming a provider with no agent", r)
		}
	}()
	Register(fakeProvider{agent: ""})
}

func TestRegisteredIsSortedByAgent(t *testing.T) {
	withProviders(t,
		fakeProvider{agent: schema.AgentCodex},
		fakeProvider{agent: schema.AgentClaudeCode},
		fakeProvider{agent: schema.AgentCursor},
	)

	got := Registered()
	if len(got) != 3 {
		t.Fatalf("Registered() returned %d providers, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Agent() >= got[i].Agent() {
			t.Errorf("Registered() not sorted: %q before %q", got[i-1].Agent(), got[i].Agent())
		}
	}
}

func TestProviderForReportsAbsence(t *testing.T) {
	withProviders(t, fakeProvider{agent: schema.AgentClaudeCode})

	if _, ok := ProviderFor(schema.AgentCodex); ok {
		t.Error("ProviderFor(codex) = true, want false: no codex provider is registered")
	}
	if _, ok := ProviderFor(schema.AgentClaudeCode); !ok {
		t.Error("ProviderFor(claude-code) = false, want true")
	}
}
