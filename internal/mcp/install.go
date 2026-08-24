package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mayberuk/recall/internal/atomicfile"
	"github.com/mayberuk/recall/internal/fperr"
)

// Action is what one step of a plan does.
type Action string

const (
	ActionRun    Action = "run"
	ActionMkdir  Action = "mkdir"
	ActionBackup Action = "copy"
	ActionWrite  Action = "write"
	ActionPrint  Action = "print"
	ActionSkip   Action = "skip"
)

// Step is one thing an install does. Content is captured while the plan is
// built rather than re-read while it runs, so the bytes a --dry-run listed are
// the bytes a real run writes.
type Step struct {
	Action  Action
	Path    string
	Argv    []string
	Detail  string
	Content []byte
}

// Plan is everything an install would do, in the order it would do it. It is
// what --dry-run prints and what Install executes, so a path absent from here
// is a path install does not touch.
type Plan struct {
	Client string
	Steps  []Step
	Notes  []string
}

// Paths is every path the plan creates or modifies, in order. A caller
// deciding whether to run this can read it as the whole blast radius.
func (p Plan) Paths() []string {
	var out []string
	for _, s := range p.Steps {
		switch s.Action {
		case ActionMkdir, ActionBackup, ActionWrite:
			out = append(out, s.Path)
		}
	}
	return out
}

// Text is the plan in one line per step, in the order the steps run.
func (p Plan) Text() string {
	var b strings.Builder
	for _, s := range p.Steps {
		switch s.Action {
		case ActionRun:
			fmt.Fprintf(&b, "  run    %s\n", strings.Join(s.Argv, " "))
		case ActionPrint:
			fmt.Fprintf(&b, "  print  %s\n", s.Detail)
		default:
			fmt.Fprintf(&b, "  %-6s %s", s.Action, s.Path)
			if s.Detail != "" {
				fmt.Fprintf(&b, "  (%s)", s.Detail)
			}
			b.WriteString("\n")
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "  note   %s\n", n)
	}
	return b.String()
}

// InstallOptions is how one install run differs from the default.
type InstallOptions struct {
	// Force replaces an existing recall entry that differs from this one.
	Force bool

	// DryRun builds the plan and executes none of it.
	DryRun bool

	// Run invokes a client's own registration CLI. It is injectable so a test
	// can assert the exact argv without that vendor's binary installed.
	Run func(argv []string) error

	// LookPath reports where a vendor binary is, if it is anywhere.
	LookPath func(name string) (string, error)

	// Log is where a vendor CLI's own output goes. It is never stdout by
	// default and never this package's business to choose: the caller passes
	// the stream it owns.
	Log io.Writer
}

func (o InstallOptions) lookPath(name string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath(name)
	}
	return exec.LookPath(name)
}

func (o InstallOptions) run(argv []string) error {
	if o.Run != nil {
		return o.Run(argv)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = o.log(), o.log()
	return cmd.Run()
}

func (o InstallOptions) log() io.Writer {
	if o.Log == nil {
		return io.Discard
	}
	return o.Log
}

// Install registers recall with one client and puts that client's instruction
// unit in place, or — under DryRun — works out what that would take and does
// none of it.
//
// It writes nothing itself for a client that ships its own registration CLI:
// the vendor owns its config format, and a merge recall writes into it can be
// wrong in ways only that vendor's own parser would notice.
func Install(c Client, bin Binary, opt InstallOptions) (Plan, error) {
	if err := bin.check(); err != nil {
		return Plan{}, err
	}
	p, err := planFor(c, bin, opt)
	if err != nil {
		return Plan{}, err
	}
	if opt.DryRun {
		return p, nil
	}
	return p, p.execute(opt)
}

func planFor(c Client, bin Binary, opt InstallOptions) (Plan, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Client: c.ID}

	switch c.registration {
	case byVendorCLI:
		argv := c.vendor(bin)
		if _, err := opt.lookPath(argv[0]); err != nil {
			return Plan{}, fperr.New(fperr.ToolMissing,
				"%s registers its own MCP servers and %q is not on PATH — install it, or add the entry `recall mcp config %s` prints to %s yourself",
				c.Name, argv[0], c.ID, path)
		}
		p.Steps = append(p.Steps, Step{
			Action: ActionRun,
			Argv:   argv,
			Detail: c.Name + " writes its own config",
		})

	case byMerge:
		steps, err := mergeSteps(c, bin, opt, path)
		if err != nil {
			return Plan{}, err
		}
		p.Steps = append(p.Steps, steps...)

	case byPrinting:
		snippet, err := c.Snippet(bin)
		if err != nil {
			return Plan{}, err
		}
		p.Steps = append(p.Steps, Step{
			Action:  ActionPrint,
			Path:    path,
			Content: []byte(snippet),
			Detail:  "the entry for " + path + ", which recall will not write: the vendor's own docs and its third-party ones disagree about this path",
		})
	}

	units, err := unitSteps(c)
	if err != nil {
		return Plan{}, err
	}
	p.Steps = append(p.Steps, units...)
	if len(c.units) == 0 {
		p.Notes = append(p.Notes, "recall ships no instruction file "+c.Name+" reads, so this is the server entry alone")
	}
	return p, nil
}

// mergeSteps plans a write into a config file recall owns no schema for: the
// original copied alongside it first, then the whole file back with recall's
// own key inserted and nothing else touched.
func mergeSteps(c Client, bin Binary, opt InstallOptions, path string) ([]Step, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fperr.New(fperr.ArgError, "cannot read %s: %v", path, err)
	}
	exists := err == nil

	merged, found, err := c.merge(path, data, bin)
	if err != nil {
		return nil, err
	}
	if found.present && !found.same && !opt.Force {
		return nil, fperr.New(fperr.ArgError,
			"%s already has a %q entry in %s that differs from this one — compare it against `recall mcp config %s`, then re-run with --force to replace it",
			c.Name, serverName, path, c.ID)
	}
	if found.same {
		return []Step{{Action: ActionSkip, Path: path, Detail: "already registered, byte for byte the same entry"}}, nil
	}

	var steps []Step
	if dir := filepath.Dir(path); !isDir(dir) {
		steps = append(steps, Step{Action: ActionMkdir, Path: dir})
	}
	// Nothing to preserve when the file is not there yet, and a backup of an
	// absent file would be an empty file a later run could restore over a good
	// config.
	if exists {
		steps = append(steps, Step{
			Action:  ActionBackup,
			Path:    path + backupSuffix,
			Content: data,
			Detail:  "the file as it stands now",
		})
	}
	return append(steps, Step{
		Action:  ActionWrite,
		Path:    path,
		Content: merged,
		Detail:  "only the " + serverName + " key changes",
	}), nil
}

func unitSteps(c Client) ([]Step, error) {
	var steps []Step
	for _, u := range c.units {
		data, err := Assets.ReadFile(u.asset)
		if err != nil {
			return nil, fperr.New(fperr.Internal, "cannot read the embedded %s: %v", u.asset, err)
		}
		if u.at == nil {
			steps = append(steps, Step{
				Action:  ActionPrint,
				Content: data,
				Detail:  "the rule for " + u.where + ", which recall will not write",
			})
			continue
		}
		target, err := u.at()
		if err != nil {
			return nil, err
		}
		if same, err := fileHolds(target, data); err != nil {
			return nil, err
		} else if same {
			steps = append(steps, Step{Action: ActionSkip, Path: target, Detail: "already the current text"})
			continue
		}
		if dir := filepath.Dir(target); !isDir(dir) {
			steps = append(steps, Step{Action: ActionMkdir, Path: dir})
		}
		// No backup: this path holds recall's own instruction file and nothing
		// else, so there is no foreign content here to lose.
		steps = append(steps, Step{Action: ActionWrite, Path: target, Content: data, Detail: "recall's own instruction file"})
	}
	return steps, nil
}

func (p Plan) execute(opt InstallOptions) error {
	for _, s := range p.Steps {
		switch s.Action {
		case ActionRun:
			if err := opt.run(s.Argv); err != nil {
				return fperr.New(fperr.ToolMissing,
					"`%s` failed: %v — the entry `recall mcp config %s` prints can be added by hand instead",
					strings.Join(s.Argv, " "), err, p.Client)
			}
		case ActionMkdir:
			if err := os.MkdirAll(s.Path, 0o755); err != nil {
				return fperr.New(fperr.AtomicWriteFailed, "cannot create %s: %v", s.Path, err)
			}
		case ActionBackup, ActionWrite:
			if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
				return fperr.New(fperr.AtomicWriteFailed, "cannot create %s: %v", filepath.Dir(s.Path), err)
			}
			if err := atomicfile.Write(s.Path, s.Content); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileHolds reports whether path already holds exactly these bytes, which is
// what makes a second install a no-op rather than a rewrite.
func fileHolds(path string, want []byte) (bool, error) {
	got, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fperr.New(fperr.ArgError, "cannot read %s: %v", path, err)
	}
	return bytes.Equal(got, want), nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
