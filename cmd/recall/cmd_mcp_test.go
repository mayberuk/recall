package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/mcp"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/schema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// tempHome points every client config path at a directory of this test's own,
// so nothing here can read or write a real one.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	return home
}

// treeOf is every file under root, by relative path, with its contents hashed
// so a comparison names the file that changed rather than dumping it.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// assertSameTree names every path that appeared, vanished or changed.
func assertSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for path, sum := range after {
		switch prev, ok := before[path]; {
		case !ok:
			t.Errorf("%s: %s was created", what, path)
		case prev != sum:
			t.Errorf("%s: %s was modified", what, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("%s: %s was deleted", what, path)
		}
	}
}

// fileFact is enough of a real file on the developer's own machine to prove a
// test never touched it, without reading its contents.
type fileFact struct {
	path   string
	exists bool
	size   int64
	mod    time.Time
}

func factOf(t *testing.T, path string) fileFact {
	t.Helper()
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fileFact{path: path}
	}
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fileFact{path: path, exists: true, size: fi.Size(), mod: fi.ModTime()}
}

func assertUntouched(t *testing.T, before fileFact) {
	t.Helper()
	after := factOf(t, before.path)
	if after != before {
		t.Errorf("the real %s changed: %+v -> %+v", before.path, before, after)
	}
}

// realHomePath resolves a path under the developer's own home, before any of
// these tests relocate HOME. Asserting the real file is untouched is worth
// more than a comment promising it.
func realHomePath(t *testing.T, parts ...string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory to guard: %v", err)
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

// TestMcpConfigPrintsTheEntryAndItsPathAndWritesNothing is criterion 2, over
// every client: the entry and the file it belongs in, and not one byte
// written anywhere.
func TestMcpConfigPrintsTheEntryAndItsPathAndWritesNothing(t *testing.T) {
	realCursor := factOf(t, realHomePath(t, ".cursor", "mcp.json"))
	home := tempHome(t)
	before := treeOf(t, home)

	bin, err := recallBinary()
	if err != nil {
		t.Fatalf("recallBinary: %v", err)
	}
	for _, c := range mcp.Clients() {
		t.Run(c.ID, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := runMCP([]string{"config", c.ID}, &out, &errOut); err != nil {
				t.Fatalf("mcp config %s: %v", c.ID, err)
			}
			path, err := c.ConfigPath()
			if err != nil {
				t.Fatalf("ConfigPath: %v", err)
			}
			snippet, err := c.Snippet(bin)
			if err != nil {
				t.Fatalf("Snippet: %v", err)
			}
			if !strings.Contains(out.String(), path) {
				t.Errorf("the output does not name the file the entry belongs in (%s):\n%s", path, out.String())
			}
			if !strings.Contains(out.String(), snippet) {
				t.Errorf("the output does not carry the entry itself:\n%s\nwant:\n%s", out.String(), snippet)
			}
			if !strings.Contains(out.String(), bin.Path) {
				t.Errorf("the entry does not name recall's own absolute path %q:\n%s", bin.Path, out.String())
			}
			if errOut.Len() != 0 {
				t.Errorf("config wrote to stderr: %q", errOut.String())
			}
		})
	}

	assertSameTree(t, "mcp config", before, treeOf(t, home))
	assertUntouched(t, realCursor)
}

// TestMcpInstallPrintsTheEntryAndFailsWhenTheClientsOwnCLIIsMissing is
// criterion 4. The claim is not "it failed" — it is that the caller was left
// with the exact entry to add by hand and that recall did not write the
// client's file itself.
func TestMcpInstallPrintsTheEntryAndFailsWhenTheClientsOwnCLIIsMissing(t *testing.T) {
	home := tempHome(t)
	before := treeOf(t, home)

	for _, id := range []string{"claude-code", "codex", "gemini", "copilot"} {
		t.Run(id, func(t *testing.T) {
			var out, errOut bytes.Buffer
			opt := mcp.InstallOptions{
				Log:      &errOut,
				LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
				Run: func(argv []string) error {
					t.Errorf("ran %v even though the vendor CLI is not on PATH", argv)
					return nil
				},
			}
			err := mcpInstall(id, &out, opt)
			if err == nil {
				t.Fatal("install succeeded with the client's own CLI missing")
			}
			if got := codeOf(t, err); got != fperr.ToolMissing {
				t.Errorf("error code = %q, want %q", got, fperr.ToolMissing)
			}

			c, _ := mcp.Lookup(id)
			bin, _ := recallBinary()
			snippet, err := c.Snippet(bin)
			if err != nil {
				t.Fatalf("Snippet: %v", err)
			}
			if !strings.Contains(out.String(), snippet) {
				t.Errorf("the failure printed no entry to add by hand:\n%s", out.String())
			}
		})
	}
	assertSameTree(t, "install with the vendor CLI missing", before, treeOf(t, home))
}

// TestMcpInstallPrintsThePlanAndTheDryRunSaysNothingWasWritten is criterion 8
// at the level a caller reads it: the same paths in both listings, and files
// on disk only after the run that was not a dry one.
func TestMcpInstallPrintsThePlanAndTheDryRunSaysNothingWasWritten(t *testing.T) {
	home := tempHome(t)
	before := treeOf(t, home)

	var dry, errOut bytes.Buffer
	if err := runMCP([]string{"install", "cursor", "--dry-run"}, &dry, &errOut); err != nil {
		t.Fatalf("mcp install --dry-run: %v", err)
	}
	assertSameTree(t, "mcp install --dry-run", before, treeOf(t, home))
	if !strings.Contains(dry.String(), "nothing below was written") {
		t.Errorf("the dry run does not say it wrote nothing:\n%s", dry.String())
	}

	config := filepath.Join(home, ".cursor", "mcp.json")
	for _, path := range []string{config} {
		if !strings.Contains(dry.String(), path) {
			t.Errorf("the dry run does not list %s:\n%s", path, dry.String())
		}
		absent := func() bool { _, err := os.Stat(path); return err != nil }
		if !absent() {
			t.Errorf("the dry run created %s", path)
		}
	}

	var out bytes.Buffer
	if err := runMCP([]string{"install", "cursor"}, &out, &errOut); err != nil {
		t.Fatalf("mcp install: %v", err)
	}
	for _, path := range []string{config} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("install did not write %s: %v", path, err)
		}
		if !strings.Contains(out.String(), path) {
			t.Errorf("the install report does not name %s:\n%s", path, out.String())
		}
	}
	// Cursor documents .cursor/rules only inside a workspace, so the rule is
	// printed with that path rather than written to a guessed one in HOME.
	rule := filepath.Join(home, ".cursor", "rules", "recall.mdc")
	if _, err := os.Stat(rule); err == nil {
		t.Errorf("install wrote %s, a path Cursor is not documented to read", rule)
	}
	if !strings.Contains(out.String(), ".cursor/rules/recall.mdc in the workspace") {
		t.Errorf("the install report does not say where the rule belongs:\n%s", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("install wrote to stderr: %q", errOut.String())
	}
}

// TestMcpInstallWindsurfPrintsTheEntryAndTheRuleAndWritesNoConfig is
// criterion 7 as the caller sees it.
func TestMcpInstallWindsurfPrintsTheEntryAndTheRuleAndWritesNoConfig(t *testing.T) {
	home := tempHome(t)
	var out, errOut bytes.Buffer
	if err := runMCP([]string{"install", "windsurf"}, &out, &errOut); err != nil {
		t.Fatalf("mcp install windsurf: %v", err)
	}

	c, _ := mcp.Lookup("windsurf")
	bin, _ := recallBinary()
	snippet, err := c.Snippet(bin)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	if !strings.Contains(out.String(), snippet) {
		t.Errorf("install windsurf printed no entry:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "trigger: model_decision") {
		t.Errorf("install windsurf printed no rule for the caller to place:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".codeium", "mcp_config.json")); err == nil {
		t.Error("install windsurf wrote the client's config file")
	}
}

// TestSelectAgentRestoresTheLaunchEnvironmentExactly is the per-call half of
// the provider rule, checked at the environment rather than at the corpus:
// the corpus assertions live in the fresh-process scenarios below, and this
// pins the three branches that get it there.
func TestSelectAgentRestoresTheLaunchEnvironmentExactly(t *testing.T) {
	t.Run("launched with the variable unset", func(t *testing.T) {
		t.Setenv(recallAgentEnv, "")
		if err := os.Unsetenv(recallAgentEnv); err != nil {
			t.Fatalf("unset: %v", err)
		}
		s := newVerbSearcher(io.Discard)

		if err := s.selectAgent("codex"); err != nil {
			t.Fatalf("selectAgent: %v", err)
		}
		if got := os.Getenv(recallAgentEnv); got != "codex" {
			t.Errorf("%s = %q, want %q", recallAgentEnv, got, "codex")
		}
		for _, auto := range []string{"", ProviderAuto} {
			if err := s.selectAgent(auto); err != nil {
				t.Fatalf("selectAgent(%q): %v", auto, err)
			}
			if v, ok := os.LookupEnv(recallAgentEnv); ok {
				t.Errorf("selectAgent(%q) left %s set to %q; it was unset at launch", auto, recallAgentEnv, v)
			}
		}
	})

	t.Run("launched with the variable set", func(t *testing.T) {
		t.Setenv(recallAgentEnv, "codex")
		s := newVerbSearcher(io.Discard)

		if err := s.selectAgent("claude-code"); err != nil {
			t.Fatalf("selectAgent: %v", err)
		}
		if err := s.selectAgent(ProviderAuto); err != nil {
			t.Fatalf("selectAgent(auto): %v", err)
		}
		if got := os.Getenv(recallAgentEnv); got != "codex" {
			t.Errorf("%s = %q after an auto call, want the launch-time %q", recallAgentEnv, got, "codex")
		}
	})
}

func TestMcpRefusesAnUnknownSubcommandAndAnUnknownClient(t *testing.T) {
	tempHome(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no sub-command", []string{}, "serve, config, install or export"},
		{"unknown sub-command", []string{"start"}, `unknown mcp sub-command "start"`},
		{"config with no client", []string{"config"}, "config needs a client"},
		{"unknown client", []string{"config", "emacs"}, `unknown client "emacs"`},
		{"install unknown client", []string{"install", "emacs"}, `unknown client "emacs"`},
		{"export with no directory", []string{"export"}, "export needs a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := runMCP(tc.args, &out, &errOut)
			if err == nil {
				t.Fatalf("no error; stdout: %s", out.String())
			}
			if got := codeOf(t, err); got != fperr.ArgError {
				t.Errorf("error code = %q, want %q", got, fperr.ArgError)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("a usage failure still printed to stdout: %q", out.String())
			}
		})
	}
}

func TestMcpExportWritesTheEmbeddedTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "integrations")
	var out, errOut bytes.Buffer
	if err := runMCP([]string{"export", dir}, &out, &errOut); err != nil {
		t.Fatalf("mcp export: %v", err)
	}
	for _, want := range []string{
		filepath.Join("skills", "recall", "SKILL.md"),
		filepath.Join("rules", "recall.mdc"),
		filepath.Join("plugins", "claude-code", ".claude-plugin", "plugin.json"),
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("export did not write %s: %v", want, err)
		}
	}
	if !strings.Contains(out.String(), dir) {
		t.Errorf("export did not say where it wrote: %q", out.String())
	}
}

// TestMcpExportRefusesToWriteIntoATranscriptStore is criterion 11 for the one
// sub-command whose destination the caller chooses. The store is planted with
// a file first, so this compares real bytes rather than two empty directories.
func TestMcpExportRefusesToWriteIntoATranscriptStore(t *testing.T) {
	corpus := harness(t)
	before := treeOf(t, corpus.Root)

	var out, errOut bytes.Buffer
	err := runMCP([]string{"export", filepath.Join(corpus.Root, "integrations")}, &out, &errOut)
	if err == nil {
		t.Fatal("export wrote into the claude-code session store")
	}
	if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("error code = %q, want %q", got, fperr.ArgError)
	}
	if !strings.Contains(err.Error(), corpus.Root) {
		t.Errorf("the refusal does not name the store: %v", err)
	}
	assertSameTree(t, "a refused export", before, treeOf(t, corpus.Root))

	// The negative control: the same export into a directory of the caller's
	// own is exactly what the command is for.
	if err := runMCP([]string{"export", filepath.Join(t.TempDir(), "integrations")}, &out, &errOut); err != nil {
		t.Errorf("export into an ordinary directory was refused too: %v", err)
	}
}

// syncBuffer collects the server's stdout while the client reads it, on the
// client's own goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// teeCloser hands the client every byte the server wrote while keeping a copy
// for the assertions.
type teeCloser struct {
	io.Reader
	io.Closer
}

// TestMcpServeWritesOnlyJSONRPCToStdoutAndEveryDiagnosticToStderr is
// criterion 1, driven by a real client over real pipes attached to the
// process's own os.Stdin and os.Stdout — the same handles a client would
// attach to.
//
// The negative control is the archive-building notice: a cold archive prints
// it on every first search, so this run provably produces a diagnostic, and
// the assertion is that it came out on stderr and not in the protocol stream.
// It also proves the tool call is answered out of the fixture corpus, which
// is what makes "the diagnostic happened" more than an assumption.
func TestMcpServeWritesOnlyJSONRPCToStdoutAndEveryDiagnosticToStderr(t *testing.T) {
	corpus := harness(t)
	storeBefore := treeOf(t, corpus.Root)

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("stderr file: %v", err)
	}

	realIn, realOut, realErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errFile
	defer func() { os.Stdin, os.Stdout, os.Stderr = realIn, realOut, realErr }()

	var verbOut bytes.Buffer
	served := make(chan error, 1)
	go func() { served <- runMCP([]string{"serve"}, &verbOut, os.Stderr) }()

	protocol := &syncBuffer{}
	ctx := context.Background()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "serve-test", Version: "0"}, nil).
		Connect(ctx, &mcpsdk.IOTransport{
			Reader: teeCloser{Reader: io.TeeReader(outR, protocol), Closer: outR},
			Writer: inW,
		}, nil)
	if err != nil {
		t.Fatalf("connecting a client to `recall mcp serve`: %v", err)
	}

	res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "recall_find",
		Arguments: map[string]any{"query": fixtures.NeedleMultiTierText, "all": true},
	})
	if err != nil {
		t.Fatalf("recall_find over the protocol: %v", err)
	}
	if res.IsError {
		t.Fatalf("recall_find failed: %+v", res.Content)
	}
	answer, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("re-encoding the answer: %v", err)
	}
	if ids := answeredSessionIDs(t, string(answer)); !slices.Contains(ids, fixtures.SessNeedle) {
		t.Errorf("recall_find answered %v, want the fixture session %q — the call did not reach the corpus",
			ids, fixtures.SessNeedle)
	}

	_ = cs.Close()
	_ = inW.Close()
	if err := <-served; err != nil {
		t.Errorf("`recall mcp serve` returned %v after the client disconnected, want a clean exit", err)
	}
	os.Stdin, os.Stdout, os.Stderr = realIn, realOut, realErr

	for i, line := range strings.Split(strings.TrimRight(protocol.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout line %d is not JSON, so the protocol stream is corrupt: %v\n%q", i+1, err, line)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("stdout line %d is not a JSON-RPC message: %q", i+1, line)
		}
	}

	diagnostics, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatalf("read the captured stderr: %v", err)
	}
	const notice = "building the archive"
	if !strings.Contains(string(diagnostics), notice) {
		t.Errorf("the archive-building notice is not on stderr, so this run proves nothing about where diagnostics go:\n%s", diagnostics)
	}
	if strings.Contains(protocol.String(), notice) {
		t.Errorf("the archive-building notice was written into the protocol stream:\n%s", protocol.String())
	}
	if verbOut.Len() != 0 {
		t.Errorf("serve wrote to the verb's own output stream: %q", verbOut.String())
	}

	// Criterion 11 for the one sub-command that reads the corpus at all.
	assertSameTree(t, "mcp serve", storeBefore, treeOf(t, corpus.Root))
}

// TestTheAdapterAnswersTheSameCorpusTheCLIDoes is the seam this package
// exists to keep honest: recall_find and `recall find` run the same verb, so
// they must name the same sessions and declare the same coverage.
func TestTheAdapterAnswersTheSameCorpusTheCLIDoes(t *testing.T) {
	harness(t)
	s := newVerbSearcher(io.Discard)

	view, err := s.Find(context.Background(), mcp.FindArgs{
		SearchArgs: mcp.SearchArgs{Query: fixtures.NeedleMultiTierText, All: true},
	})
	if err != nil {
		t.Fatalf("adapter find: %v", err)
	}
	var ids []string
	for _, sess := range view.Sessions {
		ids = append(ids, sess.ID)
	}
	if !slices.Contains(ids, fixtures.SessNeedle) {
		t.Errorf("the adapter answered %v, want the fixture session %q", ids, fixtures.SessNeedle)
	}

	cli, _, err := callFind(t, fixtures.NeedleMultiTierText, "--all", "--json")
	if err != nil {
		t.Fatalf("cli find: %v", err)
	}
	if want := answeredSessionIDs(t, cli); !slices.Equal(ids, want) {
		t.Errorf("the adapter answered %v and `recall find` answered %v", ids, want)
	}
	if view.Coverage.Sessions != decodeCoverage(t, cli).Sessions {
		t.Errorf("the adapter's coverage counts %d sessions, `recall find` counts %d",
			view.Coverage.Sessions, decodeCoverage(t, cli).Sessions)
	}
}

func decodeCoverage(t *testing.T, blob string) struct{ Sessions int } {
	t.Helper()
	var got struct {
		Coverage struct{ Sessions int } `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(blob), &got); err != nil {
		t.Fatalf("decoding the CLI's answer: %v", err)
	}
	return got.Coverage
}

// TestASearchThatMatchesNothingIsAnEmptyAnswerNotAFailure keeps the
// terms-nearby survey a caller's next query is built from: every searching
// verb reports a miss as fperr.NoHits after writing a complete report, and
// failing the call would throw the report away.
func TestASearchThatMatchesNothingIsAnEmptyAnswerNotAFailure(t *testing.T) {
	harness(t)
	s := newVerbSearcher(io.Discard)
	ctx := context.Background()
	const miss = "zzzznotinanycorpus"

	find, err := s.Find(ctx, mcp.FindArgs{SearchArgs: mcp.SearchArgs{Query: miss, All: true}})
	if err != nil {
		t.Fatalf("find with no hits returned an error: %v", err)
	}
	if len(find.Sessions) != 0 {
		t.Errorf("find matched %d sessions on a token planted nowhere", len(find.Sessions))
	}
	if find.Coverage.Query.Terms == nil {
		t.Error("the empty answer carries no coverage block, which is the whole point of returning it")
	}

	turns, err := s.Turns(ctx, mcp.TurnsArgs{SearchArgs: mcp.SearchArgs{Query: miss, All: true}})
	if err != nil {
		t.Fatalf("turns with no hits returned an error: %v", err)
	}
	if len(turns.Passages) != 0 {
		t.Errorf("turns matched %d passages on a token planted nowhere", len(turns.Passages))
	}

	when, err := s.When(ctx, mcp.WhenArgs{SearchArgs: mcp.SearchArgs{Query: miss, All: true}})
	if err != nil {
		t.Fatalf("when with no hits returned an error: %v", err)
	}
	if len(when.Sessions) != 0 {
		t.Errorf("when matched %d sessions on a token planted nowhere", len(when.Sessions))
	}
}

func TestTheAdapterAnswersShowAndGuideToo(t *testing.T) {
	harness(t)
	s := newVerbSearcher(io.Discard)
	ctx := context.Background()

	show, err := s.Show(ctx, mcp.ShowArgs{Session: fixtures.SessNeedle, Query: fixtures.NeedleMultiTierText})
	if err != nil {
		t.Fatalf("adapter show: %v", err)
	}
	if show.Session != fixtures.SessNeedle {
		t.Errorf("show answered session %q, want %q", show.Session, fixtures.SessNeedle)
	}
	if len(show.Windows) == 0 {
		t.Error("show returned no windows around a query that is in that session")
	}

	// A session that is not in the corpus is a failure, not an empty answer:
	// nothing was searched and there is no report to carry back.
	if _, err := s.Show(ctx, mcp.ShowArgs{Session: "0000dead"}); err == nil {
		t.Error("show resolved a session id that is not in the corpus")
	} else if got := codeOf(t, err); got != fperr.NotFound {
		t.Errorf("error code = %q, want %q", got, fperr.NotFound)
	}

	guide, err := s.Guide(ctx)
	if err != nil {
		t.Fatalf("adapter guide: %v", err)
	}
	if guide != guideText {
		t.Error("the guide tool does not return the same page `recall guide` prints")
	}
}

// TestEveryFieldTheToolSchemaAdvertisesReachesTheVerb walks the argument
// structs by reflection rather than by a hand-kept list, so a field added to
// internal/mcp's schema later cannot be silently dropped here. A field the
// schema advertises and the adapter drops is a lie the caller has no way to
// detect.
func TestEveryFieldTheToolSchemaAdvertisesReachesTheVerb(t *testing.T) {
	t.Run("search", func(t *testing.T) {
		eachField(t, reflect.TypeOf(mcp.SearchArgs{}), func(t *testing.T, name string, set func(any)) {
			var args mcp.SearchArgs
			set(&args)
			assertReaches(t, name, []string{"query"}, newFindCmd().fs.Lookup, searchArgv(args))
		})
	})
	t.Run("turns chars", func(t *testing.T) {
		argv := searchArgv(mcp.SearchArgs{})
		argv = intArg(argv, 42, "--chars")
		assertHasFlagValue(t, argv, "--chars", "42")
	})
	t.Run("show", func(t *testing.T) {
		eachField(t, reflect.TypeOf(mcp.ShowArgs{}), func(t *testing.T, name string, set func(any)) {
			var args mcp.ShowArgs
			set(&args)
			assertReaches(t, name, []string{"session", "query"}, newShowCmd().fs.Lookup, showArgv(args))
		})
	})
}

// eachField calls fn once per JSON field of rt, having set that one field to a
// value distinguishable from its zero.
func eachField(t *testing.T, rt reflect.Type, fn func(t *testing.T, name string, set func(any))) {
	t.Helper()
	for i := range rt.NumField() {
		field := rt.Field(i)
		if field.Anonymous {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" {
			t.Fatalf("%s.%s carries no json tag, so no flag can be derived from it", rt, field.Name)
		}
		i := i
		t.Run(name, func(t *testing.T) {
			fn(t, name, func(into any) {
				v := reflect.ValueOf(into).Elem().Field(i)
				switch v.Kind() {
				case reflect.String:
					v.SetString(fieldValueFor(name))
				case reflect.Bool:
					v.SetBool(true)
				case reflect.Int:
					v.SetInt(7)
				case reflect.Pointer:
					// A pointer number is one whose zero a caller means. Seven
					// still proves the field reaches the verb; zero has its own
					// test, because zero is what a presence check cannot see.
					n := 7
					v.Set(reflect.ValueOf(&n))
				case reflect.Slice:
					v.Set(reflect.ValueOf([]string{"alpha", "beta"}))
				default:
					t.Fatalf("no test value for %s of kind %s", name, v.Kind())
				}
			})
		})
	}
}

// fieldValueFor keeps a value the verb would reject out of the argv: --sort
// takes a fixed vocabulary, and the point here is which flag carries the
// value, not whether the verb accepts it.
func fieldValueFor(name string) string {
	if name == "sort" {
		return "recent"
	}
	return "alpha"
}

// assertReaches checks one schema field against the argv the adapter built.
// positional names the fields this verb takes as bare words rather than as
// flags, which differs between the verbs: --session narrows a search, while
// show's session is the thing being shown.
func assertReaches(t *testing.T, name string, positional []string, lookup func(string) *flag.Flag, argv []string) {
	t.Helper()
	switch {
	case slices.Contains(positional, name):
		if slices.Contains(argv, "--"+name) {
			t.Errorf("%s is positional on the command line but was passed as a flag: %v", name, argv)
		}
		return
	case name == "provider":
		// Deliberately absent: --provider writes a process-global selection
		// that nothing can unset, so the adapter routes it through
		// RECALL_AGENT instead, per call.
		if slices.Contains(argv, "--provider") {
			t.Errorf("the adapter passed --provider, which would pin the corpus for every later call: %v", argv)
		}
		return
	}

	flag := "--" + strings.ReplaceAll(name, "_", "-")
	if lookup(strings.TrimPrefix(flag, "--")) == nil {
		t.Fatalf("the schema advertises %q but the verb has no %s flag", name, flag)
	}
	if !slices.Contains(argv, flag) {
		t.Errorf("the schema advertises %q and the adapter did not pass %s: %v", name, flag, argv)
	}
}

func assertHasFlagValue(t *testing.T, argv []string, flag, want string) {
	t.Helper()
	for i, a := range argv {
		if a == flag {
			if i+1 >= len(argv) || argv[i+1] != want {
				t.Errorf("%s is not followed by %q in %v", flag, want, argv)
			}
			return
		}
	}
	t.Errorf("%s is absent from %v", flag, argv)
}

// TestTheAdapterPassesEveryValueAndNotJustTheFlag guards the shape a
// presence-only check would miss: a flag that arrives with the wrong value,
// or a repeatable one collapsed to a single occurrence.
func TestTheAdapterPassesEveryValueAndNotJustTheFlag(t *testing.T) {
	argv := searchArgv(mcp.SearchArgs{
		Repo:    "api-server",
		Not:     []string{"alpha", "beta"},
		Limit:   3,
		Hits:    ptrTo(9),
		Sort:    "recent",
		Author:  "human",
		Branch:  "main",
		Agent:   "reviewer",
		Session: "5fd86b00",
		Since:   "2w",
		Until:   "1d",
		Budget:  400,
	})
	for _, want := range []struct{ flag, value string }{
		{"--repo", "api-server"},
		{"--limit", "3"},
		{"--hits", "9"},
		{"--sort", "recent"},
		{"--author", "human"},
		{"--branch", "main"},
		{"--agent", "reviewer"},
		{"--session", "5fd86b00"},
		{"--since", "2w"},
		{"--until", "1d"},
		{"--budget", "400"},
	} {
		assertHasFlagValue(t, argv, want.flag, want.value)
	}
	if got := strings.Count(strings.Join(argv, " "), "--not "); got != 2 {
		t.Errorf("--not appears %d times for two excluded terms: %v", got, argv)
	}
	if !slices.Contains(argv, "beta") {
		t.Errorf("the second excluded term was dropped: %v", argv)
	}
	if !slices.Contains(argv, "json") {
		t.Errorf("the adapter did not ask the verb for JSON: %v", argv)
	}
}

// TestAZeroNumberIsLeftToTheVerbsOwnDefault pins the reading of an absent
// number: a plain numeric field is omitempty, so a zero arriving there cannot
// be told from a field the caller never set, and the verb's own default is the
// only honest reading of it. Budget is the exception, covered separately by
// TestABudgetOfZeroGetsTheMCPDefault.
func TestAZeroNumberIsLeftToTheVerbsOwnDefault(t *testing.T) {
	argv := searchArgv(mcp.SearchArgs{Query: "agvtool"})
	if slices.Contains(argv, "--limit") {
		t.Errorf("--limit was passed for a field the caller never set: %v", argv)
	}
	if slices.Contains(argv, "--all") {
		t.Errorf("a false boolean was passed as a flag: %v", argv)
	}
}

// An MCP caller silent on --budget still gets one — the alternative is the
// full refusal cap landing in its context on every call. An explicit
// --budget, however small, must never be overridden by the default.
func TestABudgetOfZeroGetsTheMCPDefault(t *testing.T) {
	argv := searchArgv(mcp.SearchArgs{Query: "agvtool"})
	assertHasFlagValue(t, argv, "--budget", strconv.Itoa(mcp.DefaultBudget))

	argv = searchArgv(mcp.SearchArgs{Query: "agvtool", Budget: 400})
	assertHasFlagValue(t, argv, "--budget", "400")
	if slices.Contains(argv, strconv.Itoa(mcp.DefaultBudget)) {
		t.Errorf("an explicit --budget of 400 was overridden by the default %d: %v", mcp.DefaultBudget, argv)
	}
}

// TestAZeroACallerMeantReachesTheVerb is what three of the numbers being
// pointers buys. Zero is a value for each of them — no matched turns per
// session, the whole turn rather than a slice of it, the matched turn with no
// context around it — and a plain int with omitempty cannot carry it: the wire
// form is identical to a field nobody set, so the verb's default would answer
// a question the caller did not ask.
func TestAZeroACallerMeantReachesTheVerb(t *testing.T) {
	zero := 0

	assertHasFlagValue(t, searchArgv(mcp.SearchArgs{Query: "agvtool", Hits: &zero}), "--hits", "0")
	assertHasFlagValue(t, intPtrArg(searchArgv(mcp.SearchArgs{Query: "agvtool"}), &zero, "--chars"), "--chars", "0")
	assertHasFlagValue(t, showArgv(mcp.ShowArgs{Session: "5fd86b00", Around: &zero}), "--around", "0")

	// The negative control. Without it this test would pass on an adapter that
	// passed those flags unconditionally, which is the opposite bug.
	for _, argv := range [][]string{
		searchArgv(mcp.SearchArgs{Query: "agvtool"}),
		showArgv(mcp.ShowArgs{Session: "5fd86b00"}),
	} {
		for _, flag := range []string{"--hits", "--chars", "--around"} {
			if slices.Contains(argv, flag) {
				t.Errorf("%s was passed for a field the caller never set: %v", flag, argv)
			}
		}
	}
}

// Guards against a version that stamps --budget onto every call whenever
// g.Budget > 0, regardless of whether the answer needed shaping; the
// unshaped case below is what would catch that.
func TestFindNamesBudgetInTheFooterOnlyWhenItActuallyShapedTheAnswer(t *testing.T) {
	harness(t)

	shaped, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--budget", "1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(shaped, "(--budget)") {
		t.Errorf("a --budget of 1 forced every fallback attempt but the footer names no --budget cap:\n%s", shaped)
	}
	if want := "── showing 1 of 1 sessions (--budget)"; !strings.Contains(shaped, want) {
		t.Errorf("footer does not name what the budget cap actually shaped: want a line containing %q, got:\n%s", want, shaped)
	}

	unshaped, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--budget", "100000")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if strings.Contains(unshaped, "(--budget)") {
		t.Errorf("a --budget that needed no shaping still named a --budget cap:\n%s", unshaped)
	}

	noBudget, _, err := callFind(t, fixtures.NeedleConversation, "--all")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if strings.Contains(noBudget, "(--budget)") {
		t.Errorf("a call with no --budget at all named a --budget cap:\n%s", noBudget)
	}
}

// Mirrors TestFindNamesBudgetInTheFooterOnlyWhenItActuallyShapedTheAnswer:
// turns implements its own retry loop separately from find's fitToBudget.
func TestTurnsNamesBudgetInTheFooterOnlyWhenItActuallyShapedTheAnswer(t *testing.T) {
	harness(t)

	shaped, err := callTurns(t, fixtures.NeedleConversation, "--all", "--budget", "1")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if !strings.Contains(shaped, "(--budget)") {
		t.Errorf("a --budget of 1 forced every retry but the footer names no --budget cap:\n%s", shaped)
	}
	if want := "── showing 1 of 1 matched turns (--budget)"; !strings.Contains(shaped, want) {
		t.Errorf("footer does not name what the budget cap actually shaped: want a line containing %q, got:\n%s", want, shaped)
	}

	unshaped, err := callTurns(t, fixtures.NeedleConversation, "--all", "--budget", "100000")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if strings.Contains(unshaped, "(--budget)") {
		t.Errorf("a --budget that needed no shaping still named a --budget cap:\n%s", unshaped)
	}

	noBudget, err := callTurns(t, fixtures.NeedleConversation, "--all")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	if strings.Contains(noBudget, "(--budget)") {
		t.Errorf("a call with no --budget at all named a --budget cap:\n%s", noBudget)
	}
}

// The provider scenarios run in a child process because archive's agent
// selection is a process-global with no way to unset it: any test that pins
// one leaves it pinned, and what a call with no selection of its own actually
// reads can only be observed where nothing has ever set one.
const providerScenarioEnv = "RECALL_TEST_MCP_PROVIDER_SCENARIO"

// TestAToolCallsProviderLeavesNoResidueForTheNextCall is criteria 9 and 10,
// run in a fresh process. Each scenario asserts which corpus answered, since
// a count or a bare success cannot tell one corpus from another.
func TestAToolCallsProviderLeavesNoResidueForTheNextCall(t *testing.T) {
	for _, scenario := range []string{"launched-without-an-agent", "launched-with-an-agent", "unreadable-agent"} {
		t.Run(scenario, func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=^TestProviderScenarioInAFreshProcess$", "-test.count=1", "-test.v")
			child.Env = append(os.Environ(), providerScenarioEnv+"="+scenario)
			report, err := child.CombinedOutput()
			if err != nil {
				t.Fatalf("the %s scenario failed in a fresh process: %v\n%s", scenario, err, report)
			}
		})
	}
}

// TestProviderScenarioInAFreshProcess is the child of the test above, and is
// skipped in every other run.
func TestProviderScenarioInAFreshProcess(t *testing.T) {
	scenario := os.Getenv(providerScenarioEnv)
	if scenario == "" {
		t.Skip("runs only as the child of TestAToolCallsProviderLeavesNoResidueForTheNextCall")
	}
	harness(t)
	codex := fixtures.MaterializeCodex(t)
	clearAgentDetection(t)
	// clearAgentDetection empties the variables rather than removing them, and
	// the difference is the point here: a launch with RECALL_AGENT genuinely
	// absent is the state a restore has to be able to put back.
	if err := os.Unsetenv(recallAgentEnv); err != nil {
		t.Fatalf("unset %s: %v", recallAgentEnv, err)
	}
	ctx := context.Background()

	claudeCode := mcp.FindArgs{SearchArgs: mcp.SearchArgs{Query: fixtures.NeedleMultiTierText, All: true}}
	codexOnly := mcp.FindArgs{SearchArgs: mcp.SearchArgs{Query: fixtures.NeedleCodexConversation, All: true}}
	thread := codexNeedleThread(t, codex)

	switch scenario {
	case "launched-without-an-agent":
		s := newVerbSearcher(io.Discard)

		first := codexOnly
		first.Provider = "codex"
		assertAnswered(t, s, first, thread, "the codex rollout")

		// The second call names no provider at all. It has to resolve by the
		// environment this process was launched with — where nothing was
		// selected, so claude-code — and not by what the call before it asked
		// for.
		assertAnswered(t, s, claudeCode, fixtures.SessNeedle, "the claude-code session")
		if v, ok := os.LookupEnv(recallAgentEnv); ok {
			t.Errorf("RECALL_AGENT is set to %q after a call that asked for no provider; it was unset at launch", v)
		}

	case "launched-with-an-agent":
		// The negative control for the case above: restoring the launch
		// environment is not the same as clearing it. A server launched under
		// RECALL_AGENT=codex must fall back to codex, not to the default.
		t.Setenv(recallAgentEnv, "codex")
		s := newVerbSearcher(io.Discard)

		first := claudeCode
		first.Provider = "claude-code"
		assertAnswered(t, s, first, fixtures.SessNeedle, "the claude-code session")

		auto := codexOnly
		auto.Provider = ProviderAuto
		assertAnswered(t, s, auto, thread, "the codex rollout")
		if got := os.Getenv(recallAgentEnv); got != "codex" {
			t.Errorf("RECALL_AGENT = %q after an auto call, want the launch-time %q", got, "codex")
		}

	case "unreadable-agent":
		s := newVerbSearcher(io.Discard)

		unknown := claudeCode
		unknown.Provider = "kestrel"
		view, err := s.Find(ctx, unknown)
		if err == nil {
			t.Fatalf("a provider recall cannot read answered anyway, with %d sessions", len(view.Sessions))
		}
		if !strings.Contains(err.Error(), "kestrel") {
			t.Errorf("the failure does not name the agent asked for: %v", err)
		}
		if len(view.Sessions) != 0 {
			t.Errorf("the failed call still carried %d sessions from the default corpus", len(view.Sessions))
		}

		// The failure must not leave the refused name behind either.
		assertAnswered(t, s, claudeCode, fixtures.SessNeedle, "the claude-code session")
	}
}

// assertAnswered names the session the answer has to carry, which is the only
// assertion that can tell one corpus from another.
func assertAnswered(t *testing.T, s *verbSearcher, args mcp.FindArgs, want, what string) {
	t.Helper()
	view, err := s.Find(context.Background(), args)
	if err != nil {
		t.Fatalf("find %q under provider %q: %v", args.Query, args.Provider, err)
	}
	var ids []string
	for _, sess := range view.Sessions {
		ids = append(ids, sess.ID)
	}
	if !slices.Contains(ids, want) {
		t.Fatalf("find %q under provider %q answered %v, want %s (%s)",
			args.Query, args.Provider, ids, what, want)
	}
}

func ptrTo[T any](v T) *T { return &v }

// The five tool names, restated here because internal/mcp keeps its own copies
// unexported: this file drives an adapter and a hand-rolled subprocess by name.
const (
	toolFind  = "recall_find"
	toolGuide = "recall_guide"
	toolShow  = "recall_show"
	toolTurns = "recall_turns"
	toolWhen  = "recall_when"
)

// connectInProcess wires a server to a client over the SDK's own in-memory
// transport (a real net.Pipe carrying newline-delimited JSON, not a shared Go
// value), so structuredContent and outputSchema below are what the wire form
// actually produced rather than what this process happened to hold in memory.
func connectInProcess(t *testing.T, s *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverSide, clientSide := mcpsdk.NewInMemoryTransports()
	ss, err := s.Connect(ctx, serverSide, nil)
	if err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "schema-conformance-test", Version: "0"}, nil).
		Connect(ctx, clientSide, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// A tool answering successfully is the schema-conformance assertion, and the
// proof that it is one is TestTheSDKRejectsAnAnswerThatBreaksItsOwnSchema
// below. The SDK marshals a typed handler's output and validates those bytes
// against the schema it inferred from the Go type — deliberately validating
// the JSON rather than the value, so a custom MarshalJSON cannot slip a key
// past it. jsonschema-go stamps additionalProperties:false on every struct, so
// an unknown key fails the call rather than reaching the caller.
//
// Validating a second time in this package would mean importing jsonschema-go
// directly, and a third direct dependency is exactly what scripts/deps-gate.sh
// exists to refuse.

func TestEveryToolsStructuredContentValidatesAgainstItsOwnOutputSchema(t *testing.T) {
	harness(t)
	searcher := newVerbSearcher(io.Discard)
	server, err := mcp.NewServer(mcp.Options{Version: "test", Searcher: searcher, Log: io.Discard})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cs := connectInProcess(t, server)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 5 {
		t.Fatalf("ListTools returned %d tools, want 5", len(res.Tools))
	}

	// One real call per tool, against the fixture corpus. The find, turns and
	// when calls carry All so the answer does not depend on this test's own
	// working directory.
	calls := map[string]any{
		toolFind:  mcp.FindArgs{SearchArgs: mcp.SearchArgs{Query: fixtures.NeedleConversation, All: true}},
		toolTurns: mcp.TurnsArgs{SearchArgs: mcp.SearchArgs{Query: fixtures.NeedleConversation, All: true}},
		toolShow:  mcp.ShowArgs{Session: fixtures.SessNeedle},
		toolWhen:  mcp.WhenArgs{SearchArgs: mcp.SearchArgs{Query: fixtures.NeedleConversation, All: true}},
		toolGuide: mcp.GuideArgs{},
	}

	for _, tool := range res.Tools {
		args, ok := calls[tool.Name]
		if !ok {
			t.Fatalf("no call planned for tool %s — every tool must be exercised here", tool.Name)
		}
		t.Run(tool.Name, func(t *testing.T) {
			result, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tool.Name, Arguments: args})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if result.IsError {
				// A schema mismatch surfaces here, as the SDK's own
				// "validating tool output" failure, before the answer ever
				// reaches a caller.
				t.Fatalf("IsError = true against the real adapter: %v", result.Content)
			}
			if result.StructuredContent == nil {
				t.Fatalf("%s returned no structuredContent", tool.Name)
			}
			// Not merely non-empty: the property only this tool's answer
			// carries, so a schema or a view copied from the wrong type fails
			// here rather than passing an emptiness check.
			carried := map[string]string{
				toolFind:  "sessions",
				toolGuide: "text",
				toolShow:  "windows",
				toolTurns: "passages",
				toolWhen:  "buckets",
			}[tool.Name]
			fields, ok := result.StructuredContent.(map[string]any)
			if !ok {
				var decoded map[string]any
				raw, err := json.Marshal(result.StructuredContent)
				if err != nil {
					t.Fatalf("re-encoding structuredContent: %v", err)
				}
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatalf("decoding structuredContent as an object: %v", err)
				}
				fields = decoded
			}
			if _, held := fields[carried]; !held {
				t.Errorf("%s's answer carries no %q; it carries %v", tool.Name, carried, keysOf(fields))
			}

			// Measured, not enforced: the byte cap belongs to internal/render, and
			// a breach there would already have surfaced above as IsError true.
			if tool.Name == toolFind || tool.Name == toolTurns {
				b, err := json.Marshal(result.StructuredContent)
				if err != nil {
					t.Fatalf("re-encoding structuredContent to measure it: %v", err)
				}
				t.Logf("%s: structuredContent against the fixture corpus is %d bytes (64 KiB cap, refused = %v)",
					tool.Name, len(b), result.IsError)
			}
		})
	}
}

// coverageWithDistinctStats is the round-trip fixture for
// TestCoverageMarshalsAndUnmarshalsEveryFieldIncludingStats: every field
// carries its own distinct value, so a bug that shuffled two fields would
// still fail even though every value it emitted came from the struct
// somewhere.
func coverageWithDistinctStats() render.Coverage {
	return render.Coverage{
		Sessions:         11,
		SessionsSearched: 9,
		Turns:            271,
		TurnsSearched:    204,
		Searched:         []schema.Tier{schema.TierConversation},
		Unsearched:       []schema.Tier{schema.TierResult},
		ArchiveReaches:   true,
		Refreshed:        true,
		RefreshedAgo:     "3m ago",
		Query:            render.Query{Terms: []string{"agvtool"}, Required: 1, Total: 1},
		Limits:           []render.Limit{{Flag: "--limit", What: "sessions", Shown: 3, Total: 11}},
		Notes:            []string{"one session skipped: unreadable"},
		LiveFromAt:       "2026-01-02T03:04:05Z",
		ContentFromAt:    "2025-06-07T08:09:10Z",
		ContentToAt:      "2026-02-03T04:05:06Z",
		Stats: &render.Stats{
			Bytes:      123456,
			Lines:      789,
			LinesKnown: true,
			Words:      4321,
			WordsKnown: true,
			Turns:      55,
			Passes:     3,
			ElapsedMS:  12.75,
		},
	}
}

// TestCoverageMarshalsAndUnmarshalsEveryFieldIncludingStats is criterion 2:
// the adapter runs a verb with --format json and unmarshals the result back
// into the render type, so a field whose wire name only a marshaller knows is
// dropped silently on the way in — the symptom would be a plausible answer
// with the stats quietly zeroed, not a failure.
func TestCoverageMarshalsAndUnmarshalsEveryFieldIncludingStats(t *testing.T) {
	want := coverageWithDistinctStats()

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling Coverage: %v", err)
	}
	var got render.Coverage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshalling Coverage: %v\n%s", err, b)
	}

	// The three coverage boundaries.
	if got.LiveFromAt != want.LiveFromAt {
		t.Errorf("live_from round-tripped to %q, want %q", got.LiveFromAt, want.LiveFromAt)
	}
	if got.ContentFromAt != want.ContentFromAt {
		t.Errorf("content_from round-tripped to %q, want %q", got.ContentFromAt, want.ContentFromAt)
	}
	if got.ContentToAt != want.ContentToAt {
		t.Errorf("content_to round-tripped to %q, want %q", got.ContentToAt, want.ContentToAt)
	}

	// Every Stats field, including elapsed_ms.
	if got.Stats == nil {
		t.Fatal("stats round-tripped to nil")
	}
	if *got.Stats != *want.Stats {
		t.Errorf("stats round-tripped to %+v, want %+v", *got.Stats, *want.Stats)
	}

	// The rest of Coverage, so a field outside Stats and the three boundaries
	// cannot silently drop either.
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Coverage round-tripped to %+v, want %+v", got, want)
	}
}

// rpcReply is a hand-decoded JSON-RPC 2.0 response. Result and Error are kept
// raw so a test can assert which one is present before decoding either.
type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// wantToolNamesOverTheWire is tools/list's own sorted name list, restated here
// because internal/mcp's is unexported: this file drives the subprocess by
// hand and cannot import it.
var wantToolNamesOverTheWire = []string{toolFind, toolGuide, toolShow, toolTurns, toolWhen}

// buildRecallForWireTest compiles the CLI this package's own test binary is
// built from, into a directory nothing else in this suite writes to.
func buildRecallForWireTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "recall")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build -o %s .: %v\n%s", bin, err, out)
	}
	return bin
}

// rpcTimeout bounds every wait on the subprocess below, so a hung server
// fails this test rather than the suite.
const rpcTimeout = 20 * time.Second

// TestSubprocessAnswersHandRolledJSONRPCOverStdio is criteria 3, 4, 5 and 6:
// a third party speaking the wire protocol by hand, over a real subprocess's
// real stdin and stdout, rather than the SDK's own client writing the framing
// for it.
func TestSubprocessAnswersHandRolledJSONRPCOverStdio(t *testing.T) {
	bin := buildRecallForWireTest(t)
	corpus := fixtures.Materialize(t)
	home := t.TempDir()
	recallHome := t.TempDir()

	cmd := exec.Command(bin, "mcp", "serve")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"RECALL_HOME="+recallHome,
		"CLAUDE_PROJECTS_DIR="+corpus.Root,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting `%s mcp serve`: %v", bin, err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var mu sync.Mutex
	var lines []string
	replies := make(chan string, 8)
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		defer close(replies)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
			if strings.TrimSpace(line) != "" {
				replies <- line
			}
		}
	}()

	nextReply := func() rpcReply {
		t.Helper()
		select {
		case line, ok := <-replies:
			if !ok {
				t.Fatalf("the subprocess's stdout closed with no reply; stderr:\n%s", stderr.String())
			}
			var r rpcReply
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				t.Fatalf("a reply is not JSON: %v\n%q", err, line)
			}
			return r
		case <-time.After(rpcTimeout):
			t.Fatalf("timed out after %s waiting for a reply; stderr so far:\n%s", rpcTimeout, stderr.String())
			return rpcReply{}
		}
	}

	send := func(id int, method string, meta map[string]any) {
		t.Helper()
		req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
		if meta != nil {
			req["params"] = map[string]any{"_meta": meta}
		}
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshalling the %s request: %v", method, err)
		}
		if _, err := stdin.Write(append(b, '\n')); err != nil {
			t.Fatalf("writing the %s request: %v", method, err)
		}
	}

	// Criterion 6, the negative control, sent first: a request naming
	// protocolVersion but leaving clientCapabilities out of _meta must fail
	// rather than answer, or every assertion below would pass whether the two
	// keys did anything at all.
	send(1, "tools/list", map[string]any{mcpsdk.MetaKeyProtocolVersion: "2026-07-28"})
	negControl := nextReply()
	if negControl.Error == nil {
		t.Fatalf("a request naming protocolVersion but omitting clientCapabilities got a result, want a JSON-RPC error: %s", negControl.Result)
	}
	if negControl.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602 (invalid params)", negControl.Error.Code)
	}
	if !strings.Contains(negControl.Error.Message, mcpsdk.MetaKeyClientCapabilities) {
		t.Errorf("error message %q does not name the missing field %q", negControl.Error.Message, mcpsdk.MetaKeyClientCapabilities)
	}

	meta := map[string]any{
		mcpsdk.MetaKeyProtocolVersion:    "2026-07-28",
		mcpsdk.MetaKeyClientCapabilities: map[string]any{},
	}

	send(2, "server/discover", meta)
	discover := nextReply()
	if discover.Error != nil {
		t.Fatalf("server/discover returned an error: %+v", discover.Error)
	}
	var discoverResult struct {
		SupportedVersions []string                   `json:"supportedVersions"`
		Capabilities      map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(discover.Result, &discoverResult); err != nil {
		t.Fatalf("decoding server/discover's result: %v\n%s", err, discover.Result)
	}
	if !slices.Contains(discoverResult.SupportedVersions, "2026-07-28") {
		t.Errorf("supportedVersions = %v, want 2026-07-28 among them", discoverResult.SupportedVersions)
	}
	if _, ok := discoverResult.Capabilities["logging"]; ok {
		t.Errorf("capabilities carries a logging key, which the deprecation dropped: %v", discoverResult.Capabilities)
	}
	// The positive control for the absence above: an object missing every key
	// would also pass a logging-absent-only check.
	if _, ok := discoverResult.Capabilities["tools"]; !ok {
		t.Errorf("capabilities carries no tools key: %v", discoverResult.Capabilities)
	}

	send(3, "tools/list", meta)
	list := nextReply()
	if list.Error != nil {
		t.Fatalf("tools/list returned an error: %+v", list.Error)
	}
	var listResult struct {
		ResultType string `json:"resultType"`
		TTLMs      int    `json:"ttlMs"`
		CacheScope string `json:"cacheScope"`
		Tools      []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(list.Result, &listResult); err != nil {
		t.Fatalf("decoding tools/list's result: %v\n%s", err, list.Result)
	}
	if listResult.ResultType != "complete" {
		t.Errorf("resultType = %q, want complete", listResult.ResultType)
	}
	if listResult.TTLMs != 3600000 {
		t.Errorf("ttlMs = %d, want 3600000", listResult.TTLMs)
	}
	if listResult.CacheScope != "private" {
		t.Errorf("cacheScope = %q, want private", listResult.CacheScope)
	}
	var names []string
	for _, tool := range listResult.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Equal(names, wantToolNamesOverTheWire) {
		t.Errorf("tools/list named %v, want %v", names, wantToolNamesOverTheWire)
	}

	if err := stdin.Close(); err != nil {
		t.Fatalf("closing the subprocess's stdin: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Errorf("the subprocess did not exit cleanly once stdin closed: %v\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(rpcTimeout):
		t.Fatalf("the subprocess did not exit within %s of stdin closing", rpcTimeout)
	}

	<-scanDone
	if err := sc.Err(); err != nil {
		t.Errorf("reading the subprocess's stdout: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Errorf("stdout line %d is not JSON, so the protocol stream is corrupt: %v\n%q", i+1, err, line)
			continue
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("stdout line %d is not a JSON-RPC 2.0 message: %q", i+1, line)
		}
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// strayKeyAnswer marshals a key its own Go type does not declare, which is the
// shape a hand-written MarshalJSON on a view type would take.
type strayKeyAnswer struct {
	Sessions int `json:"sessions"`
}

func (strayKeyAnswer) MarshalJSON() ([]byte, error) {
	return []byte(`{"sessions":1,"undeclared":true}`), nil
}

// TestTheSDKRejectsAnAnswerThatBreaksItsOwnSchema is what makes IsError false
// an assertion about schema conformance rather than an assertion about
// nothing. render.Coverage is embedded in all four view types, so a single
// custom marshaller emitting one undeclared key would break four tools at
// once — and this proves that break is caught at the wire rather than handed
// to a caller.
func TestTheSDKRejectsAnAnswerThatBreaksItsOwnSchema(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "stray", Version: "0"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "stray"},
		func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, strayKeyAnswer, error) {
			return nil, strayKeyAnswer{Sessions: 1}, nil
		})
	cs := connectInProcess(t, server)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "stray", Arguments: struct{}{}})
	if err == nil {
		t.Fatalf("an answer carrying a key its own schema does not declare was accepted: %+v", res.StructuredContent)
	}
	// The call fails outright rather than coming back as an error result,
	// which is why a CallTool that returns no error is itself the conformance
	// assertion in the test above.
	if !strings.Contains(err.Error(), "validating tool output") {
		t.Errorf("the failure does not name output validation: %v", err)
	}
	if !strings.Contains(err.Error(), "undeclared") {
		t.Errorf("the failure does not name the offending key: %v", err)
	}
}
