package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
)

// resumeArgv is the resume invocation for each agent's own CLI, keyed on the
// store a session's turns were decoded from (schema.Turn.Origin). It lives
// here rather than in internal/archive/provider.go, which is the provider
// registry, a different part's ground to edit.
var resumeArgv = map[schema.Agent][]string{
	schema.AgentClaudeCode: {"claude", "--resume"},
	schema.AgentCodex:      {"codex", "resume"},
}

type resumeCmd struct {
	fs *flag.FlagSet
	g  *Globals
}

func newResumeCmd() *resumeCmd {
	c := &resumeCmd{fs: newFlags("resume"), g: NewGlobals()}
	c.g.Bind(c.fs)
	return c
}

func init() {
	Register("resume", func(args []string) error { return resume(args, os.Stdout, os.Stderr) })
	Describe("resume", "<session>", "print the shell command that reopens a session in its own agent",
		newResumeCmd().fs,
		"recall resume 5fd86b00",
		`eval "$(recall resume 5fd86b00)"`)
}

func resume(args []string, out, errw io.Writer) error {
	cmd := newResumeCmd()
	fs, g := cmd.fs, cmd.g
	words, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if err := g.Check(); err != nil {
		return err
	}
	if len(words) == 0 {
		return fperr.New(fperr.ArgError, "resume needs a session id, e.g. `recall resume b5ddc1af`")
	}

	c, err := openCorpus(false, []schema.Tier{schema.TierConversation})
	if err != nil {
		return err
	}
	view, err := resumeView(c.turns, words[0])
	if err != nil {
		return err
	}

	// Stdout stays one eval-safe line; notes go to stderr instead. --json and
	// --jsonl already carry Notes in the body.
	if g.Format == FormatText {
		for _, n := range view.Notes {
			fmt.Fprintf(errw, "recall: %s\n", n)
		}
	}

	body := render.RenderResume(view)
	return emit(out, g, body, view)
}

func resumeView(turns []schema.Turn, prefix string) (render.Resume, error) {
	id, err := resolveSession(turns, prefix)
	if err != nil {
		return render.Resume{}, err
	}
	session := sessionTurns(turns, id)
	agent, cwd := resumeOrigin(session)

	base, ok := resumeArgv[agent]
	if !ok {
		return render.Resume{}, fperr.New(fperr.ArgError,
			"no resume command is known for agent %q — recall resume supports %s",
			agent, strings.Join(knownResumeAgents(), ", "))
	}
	argv := append(append([]string{}, base...), id)

	view := render.Resume{Session: id, Agent: string(agent), CWD: cwd, Argv: argv}
	switch {
	case cwd == "":
		view.Notes = append(view.Notes, "no working directory was recorded for this session")
	default:
		if _, err := os.Stat(cwd); err != nil {
			view.Notes = append(view.Notes, fmt.Sprintf("%s no longer exists", cwd))
		}
	}
	return view, nil
}

// resumeOrigin returns the first non-empty CWD in the session, which is the
// one the provider keyed the session under — not necessarily the last.
func resumeOrigin(session []schema.Turn) (schema.Agent, string) {
	var agent schema.Agent
	var cwd string
	for _, t := range session {
		if agent == "" {
			agent = t.Origin
		}
		if cwd == "" && t.CWD != "" {
			cwd = t.CWD
		}
	}
	return agent, cwd
}

func knownResumeAgents() []string {
	out := make([]string, 0, len(resumeArgv))
	for a := range resumeArgv {
		out = append(out, string(a))
	}
	sort.Strings(out)
	return out
}
