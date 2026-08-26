package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/mcp"
	"github.com/mayberuk/recall/internal/render"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type mcpCmd struct {
	fs     *flag.FlagSet
	dryRun bool
	force  bool
}

func newMCPCmd() *mcpCmd {
	c := &mcpCmd{fs: newFlags("mcp")}
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "print every path an install would create or modify, and write none of them")
	c.fs.BoolVar(&c.force, "force", false, "replace an existing recall entry in a client's config that differs from this one")
	return c
}

func init() {
	Register("mcp", func(args []string) error { return runMCP(args, os.Stdout, os.Stderr) })
	Describe("mcp", "<serve|config|install|export> [client|dir]",
		"serve recall to an agent over MCP, and register it with a client",
		newMCPCmd().fs,
		"recall mcp serve",
		"recall mcp config claude-code",
		"recall mcp install cursor --dry-run",
		"recall mcp install cursor",
		"recall mcp export ./integrations")
}

// runMCP dispatches the four sub-commands. Only config, install and export
// write to out: under serve, stdout is the protocol stream, and a single
// stray byte on it is a parse error the client reports as somebody else's
// problem.
func runMCP(args []string, out, errOut io.Writer) error {
	c := newMCPCmd()
	words, err := parseArgs(c.fs, args)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return fperr.New(fperr.ArgError, "mcp needs a sub-command: %s", mcpSubcommands)
	}

	switch sub, rest := words[0], words[1:]; sub {
	case "serve":
		return mcpServe(errOut)
	case "config":
		id, err := clientArg(rest, sub)
		if err != nil {
			return err
		}
		return mcpConfig(id, out)
	case "install":
		id, err := clientArg(rest, sub)
		if err != nil {
			return err
		}
		return mcpInstall(id, out, mcp.InstallOptions{Force: c.force, DryRun: c.dryRun, Log: errOut})
	case "export":
		if len(rest) == 0 {
			return fperr.New(fperr.ArgError, "export needs a directory, e.g. `recall mcp export ./integrations`")
		}
		return mcpExport(rest[0], out)
	default:
		return fperr.New(fperr.ArgError, "unknown mcp sub-command %q — %s", sub, mcpSubcommands)
	}
}

const mcpSubcommands = "serve, config, install or export"

func clientArg(rest []string, sub string) (string, error) {
	if len(rest) == 0 {
		return "", fperr.New(fperr.ArgError, "%s needs a client: %s", sub, strings.Join(mcp.IDs(), ", "))
	}
	return rest[0], nil
}

func lookupClient(id string) (mcp.Client, error) {
	c, ok := mcp.Lookup(id)
	if !ok {
		return mcp.Client{}, fperr.New(fperr.ArgError, "unknown client %q — recall knows %s",
			id, strings.Join(mcp.IDs(), ", "))
	}
	return c, nil
}

// mcpServe runs the server until the client closes stdin. Diagnostics go to
// errOut, which the protocol revision blesses for a stdio server and tells
// clients not to read as a failure signal.
func mcpServe(errOut io.Writer) error {
	err := mcp.Serve(context.Background(), mcp.Options{
		Version:  version,
		Searcher: newVerbSearcher(errOut),
		Log:      errOut,
	})
	if endedCleanly(err) {
		return nil
	}
	return err
}

// The JSON-RPC codes the SDK's connection ends on when the stream closed
// rather than broke. A client that exits closes recall's stdin, which is how
// every session ends; reporting that as a failure would have every client log
// a crash at shutdown. The codes are the only structural handle on it — the
// sentinel errors themselves live in the SDK's internal jsonrpc2 package and
// are not importable.
const (
	codeServerClosing = -32004
	codeClientClosing = -32003
)

func endedCleanly(err error) bool {
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && (wire.Code == codeServerClosing || wire.Code == codeClientClosing)
}

// mcpConfig prints the entry and the file it belongs in, and writes nothing.
// It is the safe default: every path from here on is opt-in.
func mcpConfig(id string, out io.Writer) error {
	c, err := lookupClient(id)
	if err != nil {
		return err
	}
	bin, err := recallBinary()
	if err != nil {
		return err
	}
	return printConfig(out, c, bin)
}

func printConfig(out io.Writer, c mcp.Client, bin mcp.Binary) error {
	path, err := c.ConfigPath()
	if err != nil {
		return err
	}
	snippet, err := c.Snippet(bin)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", c.ID, c.Name)
	fmt.Fprintf(&b, "  file   %s\n", path)
	if note := c.Note(); note != "" {
		fmt.Fprintf(&b, "  also   %s\n", note)
	}
	if argv := c.Command(bin); len(argv) > 0 {
		fmt.Fprintf(&b, "  add    %s\n", strings.Join(argv, " "))
	}
	fmt.Fprintf(&b, "\n%s\n", snippet)
	switch {
	case c.PrintOnly():
		fmt.Fprintf(&b, "nothing was written, and `recall mcp install %s` writes nothing either: it prints this same entry.\n", c.ID)
	default:
		fmt.Fprintf(&b, "nothing was written. `recall mcp install %s` is the opt-in that puts it in place.\n", c.ID)
	}
	_, err = io.WriteString(out, b.String())
	return err
}

func mcpInstall(id string, out io.Writer, opt mcp.InstallOptions) error {
	c, err := lookupClient(id)
	if err != nil {
		return err
	}
	bin, err := recallBinary()
	if err != nil {
		return err
	}

	plan, err := mcp.Install(c, bin, opt)
	if err != nil {
		// A client whose own registration CLI is missing gets the entry
		// printed, so the next move is on the screen rather than in the docs.
		var coded *fperr.Error
		if errors.As(err, &coded) && coded.Code == fperr.ToolMissing {
			if perr := printConfig(out, c, bin); perr != nil {
				return perr
			}
		}
		return err
	}
	return printPlan(out, c, plan, opt.DryRun)
}

func printPlan(out io.Writer, c mcp.Client, p mcp.Plan, dry bool) error {
	var b strings.Builder
	if dry {
		fmt.Fprintf(&b, "recall mcp install %s --dry-run — %s, nothing below was written\n", c.ID, c.Name)
	} else {
		fmt.Fprintf(&b, "recall mcp install %s — %s\n", c.ID, c.Name)
	}
	b.WriteString(p.Text())
	for _, s := range p.Steps {
		if s.Action != mcp.ActionPrint {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n\n%s", s.Detail, s.Content)
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func mcpExport(dir string, out io.Writer) error {
	if err := outsideEveryCorpus(dir); err != nil {
		return err
	}
	if err := mcp.ExportAssets(dir); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "wrote recall's skill, rules and plugin manifests to %s\n", dir)
	return err
}

// outsideEveryCorpus refuses a destination inside a session store recall
// reads. Every store is read-only to recall, and a tree written into one
// would be walked as transcripts from the next search onwards. It can only
// speak for the agents this build has providers for, which are the only
// corpora it reads at all.
func outsideEveryCorpus(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fperr.New(fperr.ArgError, "cannot resolve %s: %v", dir, err)
	}
	for _, p := range archive.Registered() {
		root, err := p.Root()
		if err != nil {
			continue
		}
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return fperr.New(fperr.ArgError,
				"refusing to write into %s's session store at %s — recall only ever reads a transcript store", p.Agent(), root)
		}
	}
	return nil
}

// recallBinary is the path a client's config will name. os.Executable is the
// only honest source for it: a client spawns this server itself, from its own
// working directory, so the path has to be one that resolves from anywhere.
func recallBinary() (mcp.Binary, error) {
	exe, err := os.Executable()
	if err != nil {
		return mcp.Binary{}, fperr.New(fperr.Internal, "cannot resolve recall's own path: %v", err)
	}
	return mcp.NewBinary(exe)
}

// recallAgentEnv is archive.Select's environment spelling of --provider. It is
// named here rather than imported because internal/archive keeps its own
// constant private.
const recallAgentEnv = "RECALL_AGENT"

// verbSearcher answers every tool call by running the CLI verb the tool is
// named after, with --format json, and reading the render view back out of
// the buffer it wrote.
//
// Going through the verb rather than the search layer is the point: every
// answer then passes the same openCorpus and coverageOf funnel the command
// line uses, so `recall find` and recall_find cannot disagree about what the
// corpus holds or about what the coverage footer declares.
type verbSearcher struct {
	mu  sync.Mutex
	log io.Writer

	// launchAgent is RECALL_AGENT as this process was started with it, and
	// launchAgentSet records whether it was there at all. A call that asks for
	// no particular provider is answered from this, not from whatever the
	// previous call asked for.
	launchAgent    string
	launchAgentSet bool
}

func newVerbSearcher(log io.Writer) *verbSearcher {
	if log == nil {
		log = io.Discard
	}
	agent, ok := os.LookupEnv(recallAgentEnv)
	return &verbSearcher{log: log, launchAgent: agent, launchAgentSet: ok}
}

func (s *verbSearcher) Find(_ context.Context, a mcp.FindArgs) (render.Find, error) {
	return answer[render.Find](s, a.Provider, func(out *bytes.Buffer) error {
		return find(append(searchArgv(a.SearchArgs), a.Query), out, s.log)
	})
}

func (s *verbSearcher) Turns(_ context.Context, a mcp.TurnsArgs) (render.Turns, error) {
	return answer[render.Turns](s, a.Provider, func(out *bytes.Buffer) error {
		argv := intPtrArg(searchArgv(a.SearchArgs), a.Chars, "--chars")
		return turns(append(argv, a.Query), out)
	})
}

func (s *verbSearcher) When(_ context.Context, a mcp.WhenArgs) (render.When, error) {
	return answer[render.When](s, a.Provider, func(out *bytes.Buffer) error {
		return when(append(searchArgv(a.SearchArgs), a.Query), out)
	})
}

func (s *verbSearcher) Show(_ context.Context, a mcp.ShowArgs) (render.Show, error) {
	return answer[render.Show](s, a.Provider, func(out *bytes.Buffer) error {
		return show(showArgv(a), out)
	})
}

func (s *verbSearcher) Guide(_ context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var buf bytes.Buffer
	if err := guide(nil, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// answer runs one verb and reads its own JSON back.
//
// A search that matched nothing arrives here as fperr.NoHits with a complete
// report already in the buffer, and is a successful empty answer rather than
// an error: the coverage block it carries holds the terms-nearby survey that
// turns a dead end into the next query, and failing the call would throw that
// away. Every searching verb returns it, not only find.
func answer[V any](s *verbSearcher, provider string, run func(out *bytes.Buffer) error) (V, error) {
	var view V

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.selectAgent(provider); err != nil {
		return view, err
	}
	var buf bytes.Buffer
	if err := run(&buf); err != nil && !isNoHits(err) {
		return view, err
	}
	if err := json.Unmarshal(buf.Bytes(), &view); err != nil {
		return view, fperr.New(fperr.Internal, "cannot read back the %T this call just rendered: %v", view, err)
	}
	return view, nil
}

// selectAgent points this call at the requested agent's corpus, through
// RECALL_AGENT rather than --provider.
//
// --provider is unusable here: Globals.Check hands it to archive.SetSelection,
// which writes a process-global that nothing can unset, so one call naming an
// agent would silently decide the corpus for every later call — including the
// ones that asked for auto, which never call SetSelection and so never
// overwrite it. Answering under another agent's name out of the wrong agent's
// corpus is the confident wrong answer this whole tool exists to avoid.
//
// RECALL_AGENT resolves through the identical path in archive.Select and can
// be restored exactly, including back to unset. Rewriting the process
// environment is only safe because s.mu serialises calls; nothing here may
// run two at once.
func (s *verbSearcher) selectAgent(provider string) error {
	if provider != "" && provider != ProviderAuto {
		return os.Setenv(recallAgentEnv, provider)
	}
	if !s.launchAgentSet {
		return os.Unsetenv(recallAgentEnv)
	}
	return os.Setenv(recallAgentEnv, s.launchAgent)
}

func isNoHits(err error) bool {
	var coded *fperr.Error
	return errors.As(err, &coded) && coded.Code == fperr.NoHits
}

// searchArgv is every field the searching tools share, in the CLI's own
// spelling. Provider is deliberately absent — selectAgent has already put it
// in the environment — and the query is positional, appended by the caller
// after any flag only one verb accepts.
func searchArgv(a mcp.SearchArgs) []string {
	argv := []string{"--format", FormatJSON}

	argv = stringArg(argv, a.Repo, "--repo")
	argv = boolArg(argv, a.All, "--all")
	argv = boolArg(argv, a.Results, "--results")
	argv = boolArg(argv, a.Tools, "--tools")

	argv = boolArg(argv, a.Exact, "--exact")
	argv = boolArg(argv, a.AllTerms, "--all-terms")
	for _, not := range a.Not {
		argv = append(argv, "--not", not)
	}

	argv = intArg(argv, a.Limit, "--limit")
	argv = intPtrArg(argv, a.Hits, "--hits")
	argv = stringArg(argv, a.Sort, "--sort")

	argv = stringArg(argv, a.Author, "--author")
	argv = stringArg(argv, a.Branch, "--branch")
	argv = stringArg(argv, a.Agent, "--agent")
	argv = stringArg(argv, a.Session, "--session")
	argv = stringArg(argv, a.Since, "--since")
	argv = stringArg(argv, a.Until, "--until")
	argv = boolArg(argv, a.Mine, "--mine")

	argv = boolArg(argv, a.IncludeSelf, "--include-self")
	argv = boolArg(argv, a.IncludeRecall, "--include-recall")

	argv = boolArg(argv, a.Brief, "--brief")
	argv = boolArg(argv, a.NoUpdate, "--no-update")

	// A caller that names no budget still gets one: the CLI's own default is
	// zero (refuse rather than shape), but an agent silent on the question has
	// no such intent to preserve, and the alternative is the full refusal cap
	// landing in its context on every call. An explicit budget, including one
	// larger than the default, always wins.
	budget := a.Budget
	if budget == 0 {
		budget = mcp.DefaultBudget
	}
	return intArg(argv, budget, "--budget")
}

// showArgv is show's own field list. Its two positional arguments are the
// session and then the query anchoring the windows inside it.
func showArgv(a mcp.ShowArgs) []string {
	argv := []string{"--format", FormatJSON}
	argv = stringArg(argv, a.Turn, "--turn")
	argv = boolArg(argv, a.Full, "--full")
	argv = intPtrArg(argv, a.Around, "--around")
	argv = intArg(argv, a.Chars, "--chars")
	argv = boolArg(argv, a.Results, "--results")
	argv = boolArg(argv, a.Tools, "--tools")
	argv = boolArg(argv, a.NoUpdate, "--no-update")

	argv = append(argv, a.Session)
	if a.Query != "" {
		argv = append(argv, a.Query)
	}
	return argv
}

func boolArg(argv []string, on bool, flag string) []string {
	if !on {
		return argv
	}
	return append(argv, flag)
}

func stringArg(argv []string, v, flag string) []string {
	if v == "" {
		return argv
	}
	return append(argv, flag, v)
}

// intArg passes a number only when it is positive. Every numeric field in the
// schema is omitempty, so a zero that arrives is indistinguishable from a
// field the caller never set, and the verb's own default is the honest
// reading of an absent one.
func intArg(argv []string, v int, flag string) []string {
	if v <= 0 {
		return argv
	}
	return append(argv, flag, strconv.Itoa(v))
}

// intPtrArg passes a number through even when it is zero. omitempty makes a
// zero indistinguishable from an absence on the wire, so the fields whose zero
// a caller actually means — no matched turns per session, no context turns
// either side, the whole turn rather than a slice of it — are pointers, and
// nil is the only thing that leaves the verb's own default standing.
func intPtrArg(argv []string, v *int, flag string) []string {
	if v == nil || *v < 0 {
		return argv
	}
	return append(argv, flag, strconv.Itoa(*v))
}
