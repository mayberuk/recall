package archive

import (
	"fmt"
	"os"
	"strings"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/schema"
)

const (
	envRecallAgent    = "RECALL_AGENT"
	envCodexThreadID  = "CODEX_THREAD_ID"
	envCodexSessionID = "CODEX_SESSION_ID"
	envGeminiCLI      = "GEMINI_CLI"
	envCursorAgent    = "CURSOR_AGENT"
	envClaudeCode     = "CLAUDECODE"

	specAll  = "all"
	specAuto = "auto"
)

var (
	explicitSpec string
	haveExplicit bool
)

// Selection is the resolved agent scope for a run: which agent's corpus (or
// corpora) to read, and why. Detected and Fallback are populated only when
// the choice came from environment detection rather than an explicit
// request, so a caller can report exactly what happened without re-deriving
// it.
type Selection struct {
	Agents   []schema.Agent
	Reason   string
	Detected schema.Agent
	Fallback bool
}

// SetSelection records an explicit agent spec — the CLI flag's spelling of
// RECALL_AGENT. It takes priority over the environment variable when Select
// runs.
func SetSelection(spec string) error {
	if spec == "" {
		return fperr.New(fperr.ArgError, "archive: refusing to set an empty agent selection")
	}
	explicitSpec = spec
	haveExplicit = true
	return nil
}

// Select resolves which agent's corpus (or corpora) a run should read.
//
// First match wins: an explicit spec set by SetSelection, else RECALL_AGENT,
// else the caller's own environment (CODEX_THREAD_ID, CODEX_SESSION_ID,
// GEMINI_CLI, CURSOR_AGENT in that order), else claude-code.
//
// An explicit spec naming an agent with no registered provider is an error —
// answering from a different agent's corpus than the one asked for would be
// a silent wrong answer. A detected agent may fall back to claude-code
// instead, because nothing was promised about it.
func Select() (Selection, error) {
	if spec, ok := explicitInput(); ok && spec != specAuto {
		return selectExplicit(spec)
	}
	return selectDetected(), nil
}

func explicitInput() (spec string, ok bool) {
	if haveExplicit {
		return explicitSpec, true
	}
	if v := os.Getenv(envRecallAgent); v != "" {
		return v, true
	}
	return "", false
}

func selectExplicit(spec string) (Selection, error) {
	if spec == specAll {
		var agents []schema.Agent
		for _, p := range Registered() {
			if root, err := p.Root(); err == nil && dirExists(root) {
				agents = append(agents, p.Agent())
			}
		}
		return Selection{
			Agents: agents,
			Reason: "explicit selection: all registered agents whose root exists",
		}, nil
	}

	agent := schema.Agent(spec)
	if _, ok := ProviderFor(agent); !ok {
		return Selection{}, fperr.New(fperr.ArgError,
			"unknown agent %q; registered agents are %s", spec, registeredAgentNames())
	}
	return Selection{
		Agents: []schema.Agent{agent},
		Reason: fmt.Sprintf("explicit selection: %s", agent),
	}, nil
}

// selectDetected probes the caller's environment, cheapest and most certain
// first, and never fails: a detected agent with no usable provider falls
// back to claude-code rather than erroring, since nothing was promised about
// an agent nobody explicitly asked for.
func selectDetected() Selection {
	switch {
	case os.Getenv(envCodexThreadID) != "", os.Getenv(envCodexSessionID) != "":
		return detectedOrFallback(schema.AgentCodex)
	case os.Getenv(envGeminiCLI) != "":
		return detectedOrFallback(schema.AgentGemini)
	case os.Getenv(envCursorAgent) != "":
		return detectedOrFallback(schema.AgentCursor)
	default:
		// CLAUDECODE is read here only to populate Detected for reporting: the
		// fallback below is claude-code regardless, and treating CLAUDECODE as a
		// selector would misfire for a nested agent that Claude Code itself
		// spawned as a subprocess.
		if os.Getenv(envClaudeCode) != "" {
			return Selection{
				Agents:   []schema.Agent{schema.AgentClaudeCode},
				Detected: schema.AgentClaudeCode,
				Reason:   "detected claude-code from the environment",
			}
		}
		return Selection{
			Agents: []schema.Agent{schema.AgentClaudeCode},
			Reason: "no agent detected in the environment; defaulting to claude-code",
		}
	}
}

func detectedOrFallback(detected schema.Agent) Selection {
	p, ok := ProviderFor(detected)
	if !ok {
		return Selection{
			Agents:   []schema.Agent{schema.AgentClaudeCode},
			Detected: detected,
			Fallback: true,
			Reason:   fmt.Sprintf("detected %s but no provider is registered for it; using claude-code instead", detected),
		}
	}
	root, err := p.Root()
	if err != nil || !dirExists(root) {
		return Selection{
			Agents:   []schema.Agent{schema.AgentClaudeCode},
			Detected: detected,
			Fallback: true,
			Reason:   fmt.Sprintf("detected %s but its session root does not exist; using claude-code instead", detected),
		}
	}
	return Selection{
		Agents:   []schema.Agent{detected},
		Detected: detected,
		Reason:   fmt.Sprintf("detected %s from the environment", detected),
	}
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func registeredAgentNames() string {
	ps := Registered()
	if len(ps) == 0 {
		return "(none registered)"
	}
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = string(p.Agent())
	}
	return strings.Join(names, ", ")
}
