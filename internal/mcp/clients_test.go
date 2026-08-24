package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
)

// testBinary is the absolute path every expected snippet below is written
// against.
var testBinary = Binary{Path: "/opt/bin/recall"}

// tempHome points every client's config path at a directory of this test's
// own. Nothing in this package may read or write a real client's config.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	return home
}

// wantSnippets is each client's entry as the research report describes it,
// written out here by hand rather than taken from the code under test. The
// seven differ at every level — container key, whether a type is named, and
// whether the command is a string or the whole argv as an array — so a shape
// that drifts onto a neighbour's spelling fails against the one it copied.
var wantSnippets = map[string]string{
	"claude-code": `{
  "mcpServers": {
    "recall": {
      "command": "/opt/bin/recall",
      "args": [
        "mcp",
        "serve"
      ],
      "env": {}
    }
  }
}
`,
	"codex": `[mcp_servers.recall]
command = "/opt/bin/recall"
args = ["mcp", "serve"]
`,
	"gemini": `{
  "mcpServers": {
    "recall": {
      "command": "/opt/bin/recall",
      "args": [
        "mcp",
        "serve"
      ]
    }
  }
}
`,
	"copilot": `{
  "mcpServers": {
    "recall": {
      "type": "local",
      "command": "/opt/bin/recall",
      "args": [
        "mcp",
        "serve"
      ]
    }
  }
}
`,
	"cursor": `{
  "mcpServers": {
    "recall": {
      "type": "stdio",
      "command": "/opt/bin/recall",
      "args": [
        "mcp",
        "serve"
      ]
    }
  }
}
`,
	"opencode": `{
  "mcp": {
    "recall": {
      "type": "local",
      "command": [
        "/opt/bin/recall",
        "mcp",
        "serve"
      ]
    }
  }
}
`,
	"windsurf": `{
  "mcpServers": {
    "recall": {
      "command": "/opt/bin/recall",
      "args": [
        "mcp",
        "serve"
      ],
      "env": {}
    }
  }
}
`,
}

func TestEachClientsSnippetIsItsOwnDocumentedShape(t *testing.T) {
	for _, c := range Clients() {
		t.Run(c.ID, func(t *testing.T) {
			want, ok := wantSnippets[c.ID]
			if !ok {
				t.Fatalf("no expected snippet is written down for %s", c.ID)
			}
			got, err := c.Snippet(testBinary)
			if err != nil {
				t.Fatalf("Snippet: %v", err)
			}
			if got != want {
				t.Errorf("snippet is\n%s\nwant\n%s", got, want)
			}
		})
	}
	if len(wantSnippets) != len(Clients()) {
		t.Errorf("%d clients but %d expected snippets — a new client shipped with nothing pinning its shape",
			len(Clients()), len(wantSnippets))
	}
}

// TestOpenCodeTakesTheWholeCommandAsAnArrayAndTheOthersDoNot is the
// ecosystem's odd one out, asserted against its neighbours rather than alone:
// the same array in Cursor's or Copilot's entry would be silently wrong.
func TestOpenCodeTakesTheWholeCommandAsAnArrayAndTheOthersDoNot(t *testing.T) {
	for _, c := range Clients() {
		if c.ID == "codex" {
			continue // TOML, and covered by its own pinned snippet above.
		}
		t.Run(c.ID, func(t *testing.T) {
			command := entryOf(t, c)["command"]
			if c.ID == "opencode" {
				argv, ok := command.([]any)
				if !ok {
					t.Fatalf("command is %T, want an array: OpenCode takes the whole argv in command", command)
				}
				if len(argv) != 3 || argv[0] != testBinary.Path || argv[1] != "mcp" || argv[2] != "serve" {
					t.Errorf("command = %v, want [%s mcp serve]", argv, testBinary.Path)
				}
				return
			}
			if _, ok := command.(string); !ok {
				t.Errorf("command is %T, want a string with args beside it", command)
			}
		})
	}
}

// entryOf is the recall entry inside a client's JSON snippet, whichever key
// that client keeps its servers under.
func entryOf(t *testing.T, c Client) map[string]any {
	t.Helper()
	snippet, err := c.Snippet(testBinary)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	var doc map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(snippet), &doc); err != nil {
		t.Fatalf("%s's snippet does not parse as JSON: %v\n%s", c.ID, err, snippet)
	}
	for _, servers := range doc {
		if entry, ok := servers[serverName]; ok {
			return entry
		}
	}
	t.Fatalf("%s's snippet carries no %q entry:\n%s", c.ID, serverName, snippet)
	return nil
}

// TestEveryClientRefusesARelativeBinary keeps a command that resolves against
// whatever directory the client happened to start in out of every config file
// recall prints or writes.
func TestEveryClientRefusesARelativeBinary(t *testing.T) {
	for _, c := range Clients() {
		t.Run(c.ID, func(t *testing.T) {
			_, err := c.Snippet(Binary{Path: "recall"})
			if err == nil {
				t.Fatal("a relative binary path was accepted")
			}
			if got := codeOf(t, err); got != fperr.ArgError {
				t.Errorf("error code = %q, want %q", got, fperr.ArgError)
			}
			if !strings.Contains(err.Error(), "relative") {
				t.Errorf("error %q does not say what is wrong with the path", err)
			}

			// The negative control: the same call with an absolute path is fine.
			if _, err := c.Snippet(testBinary); err != nil {
				t.Errorf("an absolute path was refused too: %v", err)
			}
		})
	}
}

func TestNewBinaryRefusesAnEmptyPath(t *testing.T) {
	if _, err := NewBinary(""); err == nil {
		t.Fatal("an empty binary path was accepted")
	}
	if b, err := NewBinary("/opt/bin/recall"); err != nil || b.Path != "/opt/bin/recall" {
		t.Errorf("NewBinary(absolute) = %+v, %v", b, err)
	}
}

func TestLookupKnowsEveryListedClientAndNothingElse(t *testing.T) {
	for _, c := range Clients() {
		got, ok := Lookup(c.ID)
		if !ok {
			t.Errorf("Lookup(%q) found nothing, but Clients() lists it", c.ID)
			continue
		}
		if got.Name != c.Name {
			t.Errorf("Lookup(%q).Name = %q, want %q", c.ID, got.Name, c.Name)
		}
	}
	if _, ok := Lookup("emacs"); ok {
		t.Error("Lookup found a client recall has no recipe for")
	}
	if got, want := IDs(), []string{"claude-code", "codex", "gemini", "copilot", "cursor", "opencode", "windsurf"}; !slices.Equal(got, want) {
		t.Errorf("IDs() = %v, want %v", got, want)
	}
}

// TestOnlyWindsurfIsPrintOnly pins the one decision that looks like an
// omission: recall refuses to write Windsurf's config because the vendor's own
// page and several third-party guides name different paths for it.
func TestOnlyWindsurfIsPrintOnly(t *testing.T) {
	for _, c := range Clients() {
		if got, want := c.PrintOnly(), c.ID == "windsurf"; got != want {
			t.Errorf("%s PrintOnly = %v, want %v", c.ID, got, want)
		}
	}
}

// TestTheRegistrationCommandIsTheVendorsOwnSyntax pins each `mcp add` against
// the research report, including Gemini CLI's missing -- separator, which is
// the one place the four disagree.
func TestTheRegistrationCommandIsTheVendorsOwnSyntax(t *testing.T) {
	want := map[string][]string{
		"claude-code": {"claude", "mcp", "add", "recall", "--", "/opt/bin/recall", "mcp", "serve"},
		"codex":       {"codex", "mcp", "add", "recall", "--", "/opt/bin/recall", "mcp", "serve"},
		"gemini":      {"gemini", "mcp", "add", "recall", "/opt/bin/recall", "mcp", "serve"},
		"copilot":     {"copilot", "mcp", "add", "recall", "--", "/opt/bin/recall", "mcp", "serve"},
	}
	for _, c := range Clients() {
		t.Run(c.ID, func(t *testing.T) {
			got := c.Command(testBinary)
			if w, ok := want[c.ID]; ok {
				if !slices.Equal(got, w) {
					t.Errorf("command = %v, want %v", got, w)
				}
				return
			}
			// Cursor, OpenCode and Windsurf ship no non-interactive
			// registration command, and claiming one would send install
			// shelling out to a binary that cannot do the job.
			if got != nil {
				t.Errorf("command = %v, want none for a client with no registration CLI", got)
			}
		})
	}
}

// TestConfigPathsLiveUnderTheCallersHomeAndNotInATranscriptStore is the
// out-of-scope rule, checked against the paths themselves: every agent's
// session store is read-only to recall, and a config path that landed inside
// one would put an install there too.
func TestConfigPathsLiveUnderTheCallersHomeAndNotInATranscriptStore(t *testing.T) {
	home := tempHome(t)
	stores := []string{
		filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".codex", "sessions"),
		filepath.Join(home, ".gemini", "tmp"),
	}
	for _, c := range Clients() {
		t.Run(c.ID, func(t *testing.T) {
			path, err := c.ConfigPath()
			if err != nil {
				t.Fatalf("ConfigPath: %v", err)
			}
			if !filepath.IsAbs(path) {
				t.Errorf("config path %q is relative", path)
			}
			if !strings.HasPrefix(path, home+string(filepath.Separator)) {
				t.Errorf("config path %q is outside this test's home %q", path, home)
			}
			for _, store := range stores {
				if strings.HasPrefix(path, store+string(filepath.Separator)) {
					t.Errorf("config path %q is inside the transcript store %q", path, store)
				}
			}
		})
	}
}

// TestCodexAndOpenCodeFollowTheirOwnRelocationVariables keeps the printed path
// the one the client actually reads on a machine that moved it.
func TestCodexAndOpenCodeFollowTheirOwnRelocationVariables(t *testing.T) {
	home := tempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "elsewhere", "codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "elsewhere", "config"))

	codex, _ := Lookup("codex")
	if got, err := codex.ConfigPath(); err != nil || got != filepath.Join(home, "elsewhere", "codex", "config.toml") {
		t.Errorf("codex config path = %q (%v), want the CODEX_HOME one", got, err)
	}
	opencode, _ := Lookup("opencode")
	if got, err := opencode.ConfigPath(); err != nil || got != filepath.Join(home, "elsewhere", "config", "opencode", "opencode.json") {
		t.Errorf("opencode config path = %q (%v), want the XDG_CONFIG_HOME one", got, err)
	}

	// The negative control: with neither variable set, both fall back under
	// the home directory itself.
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, _ := codex.ConfigPath(); got != filepath.Join(home, ".codex", "config.toml") {
		t.Errorf("codex config path = %q, want the default under home", got)
	}
	if got, _ := opencode.ConfigPath(); got != filepath.Join(home, ".config", "opencode", "opencode.json") {
		t.Errorf("opencode config path = %q, want the default under home", got)
	}
}

func TestSnippetPathsAreNeverWrittenByConfigItself(t *testing.T) {
	home := tempHome(t)
	before := snapshot(t, home)
	for _, c := range Clients() {
		if _, err := c.ConfigPath(); err != nil {
			t.Fatalf("%s ConfigPath: %v", c.ID, err)
		}
		if _, err := c.Snippet(testBinary); err != nil {
			t.Fatalf("%s Snippet: %v", c.ID, err)
		}
	}
	assertUnchanged(t, "building every snippet", before, snapshot(t, home))
}

func TestMergeRefusesAConfigFileItCannotUnderstand(t *testing.T) {
	cursor, _ := Lookup("cursor")
	for _, tc := range []struct{ name, body, want string }{
		{"not JSON at all", "{not json", "not valid JSON"},
		{"the servers key is not an object", `{"mcpServers": 5}`, "not an object of servers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := cursor.merge("/tmp/mcp.json", []byte(tc.body), testBinary)
			if err == nil {
				t.Fatal("a config file recall cannot read was merged into anyway")
			}
			if got := codeOf(t, err); got != fperr.ArgError {
				t.Errorf("error code = %q, want %q", got, fperr.ArgError)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "/tmp/mcp.json") {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}

	// The negative control: an empty file is not a parse failure, it is a
	// config recall is the first to write into.
	merged, found, err := cursor.merge("/tmp/mcp.json", []byte("  \n"), testBinary)
	if err != nil {
		t.Fatalf("merging into an empty file: %v", err)
	}
	if found.present {
		t.Error("an empty file was reported as already carrying an entry")
	}
	if !strings.Contains(string(merged), serverName) {
		t.Errorf("the merged file has no recall entry:\n%s", merged)
	}
}

// TestOnlyTheClientsWithASecondReadLocationCarryANote keeps `recall mcp
// config` from inventing an alternative path for a client that has none.
func TestOnlyTheClientsWithASecondReadLocationCarryANote(t *testing.T) {
	want := map[string]string{
		"claude-code": ".mcp.json",
		"codex":       ".codex/config.toml",
	}
	for _, c := range Clients() {
		if substr, ok := want[c.ID]; ok {
			if !strings.Contains(c.Note(), substr) {
				t.Errorf("%s's note does not mention %s: %q", c.ID, substr, c.Note())
			}
			continue
		}
		if c.Note() != "" {
			t.Errorf("%s carries a note about a second location it has none for: %q", c.ID, c.Note())
		}
	}
}

// TestAnEntryThatDiffersOnlyInKeyOrderIsTheSameEntry keeps a hand-written
// config from being reported as a conflict a caller has to force past.
func TestAnEntryThatDiffersOnlyInKeyOrderIsTheSameEntry(t *testing.T) {
	cursor, _ := Lookup("cursor")
	handWritten := []byte(`{"mcpServers":{"recall":{"args":["mcp","serve"],"command":"/opt/bin/recall","type":"stdio"}}}`)
	_, found, err := cursor.merge("/tmp/mcp.json", handWritten, testBinary)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !found.present || !found.same {
		t.Errorf("found = %+v, want an entry recognised as the same one", found)
	}

	// The negative control: a different command really is a different entry.
	other := []byte(`{"mcpServers":{"recall":{"type":"stdio","command":"/usr/local/bin/recall","args":["mcp","serve"]}}}`)
	if _, found, err := cursor.merge("/tmp/mcp.json", other, testBinary); err != nil {
		t.Fatalf("merge: %v", err)
	} else if !found.present || found.same {
		t.Errorf("found = %+v, want a differing entry", found)
	}
}

func TestOsUserHomeDirFailureIsReported(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := homePath(".cursor"); err == nil {
		t.Skip("this platform resolves a home directory without HOME")
	} else if got := codeOf(t, err); got != fperr.Internal {
		t.Errorf("error code = %q, want %q", got, fperr.Internal)
	}
}

// snapshot is every file under root, hashed by content, so a comparison names
// the path that changed.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func assertUnchanged(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for path, data := range after {
		switch prev, ok := before[path]; {
		case !ok:
			t.Errorf("%s: %s was created", what, path)
		case prev != data:
			t.Errorf("%s: %s was modified", what, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("%s: %s was deleted", what, path)
		}
	}
}
