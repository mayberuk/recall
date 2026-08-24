package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/mayberuk/recall/internal/atomicfile"
	"github.com/mayberuk/recall/internal/fperr"
)

// serverName is the key recall registers itself under in every client's
// config. It is also the only key an install is allowed to touch: recall owns
// this one entry and no part of the file around it.
const serverName = "recall"

// backupSuffix names the copy taken before a merge. The suffix is recall's own
// so a second tool's backup cannot be mistaken for this one's.
const backupSuffix = ".recall-backup"

// serveArgs is what a client runs to reach the server. Every recipe below
// spells the same invocation in that client's own syntax.
var serveArgs = []string{"mcp", "serve"}

// Binary is the recall executable a client's config will name.
type Binary struct {
	Path string
}

// NewBinary refuses anything but an absolute path. A relative command in
// another tool's config resolves against whatever directory that client
// happens to start in, which is a server that works from one shell and is
// missing from the next.
func NewBinary(path string) (Binary, error) {
	b := Binary{Path: path}
	return b, b.check()
}

func (b Binary) check() error {
	if b.Path == "" {
		return fperr.New(fperr.ArgError, "there is no recall binary path to write into a client's config")
	}
	if !filepath.IsAbs(b.Path) {
		return fperr.New(fperr.ArgError,
			"refusing to write the relative path %q into another tool's config: a relative command resolves against whatever directory the client starts in",
			b.Path)
	}
	return nil
}

// argv is the command line a client spawns to reach the server.
func (b Binary) argv() []string {
	return append([]string{b.Path}, serveArgs...)
}

// registration is how `recall mcp install` puts recall's entry in place.
type registration int

const (
	// byVendorCLI shells out to the client's own registration command. The
	// vendor owns its config format, and the research behind these recipes
	// found the ecosystem disagreeing with itself about one config path
	// already — a merge recall writes can be subtly and silently wrong.
	byVendorCLI registration = iota

	// byMerge inserts recall's own key into a config file recall owns no
	// schema for, after copying the original alongside it.
	byMerge

	// byPrinting writes nothing at all. Windsurf's documented config path is
	// contradicted by several third-party guides, and writing to the wrong one
	// is worse than printing the right snippet.
	byPrinting
)

// Client is one client's whole recipe: where its config lives, what recall's
// entry in it looks like, and how that entry gets there.
type Client struct {
	ID, Name   string
	ConfigPath func() (string, error)
	Snippet    func(Binary) (string, error)

	// note is a second place this client also reads the same entry from.
	// `recall mcp config` prints it; nothing ever writes there.
	note string

	registration registration

	// vendor is the client's own registration command, whose first element is
	// the binary looked up on PATH.
	vendor func(Binary) []string

	// merge inserts recall's entry into whatever the config file already
	// holds, and reports what it found where recall's key goes.
	merge merger

	// units are the instruction files this client reads: the SKILL.md, or a
	// rule file for a client that has no skill mechanism.
	units []unit
}

// merger returns the config file's new contents with only recall's own key
// inserted, plus what was already sitting under that key.
type merger func(path string, existing []byte, bin Binary) ([]byte, priorEntry, error)

// priorEntry is what a merge found where recall's key goes: nothing, the same
// entry it was about to write, or a different one.
type priorEntry struct {
	present bool
	same    bool
}

// unit is an instruction file an install puts in place.
type unit struct {
	asset string

	// at is where the file goes. A nil at means recall never writes this one
	// and only prints it.
	at func() (string, error)

	// where is the destination a printed unit names for the reader, since
	// there is no path recall is confident enough to write to.
	where string
}

const (
	skillAsset        = "assets/skills/recall/SKILL.md"
	cursorRuleAsset   = "assets/rules/recall.mdc"
	windsurfRuleAsset = "assets/rules/recall-windsurf.md"
)

// Clients is every client recall knows how to register itself with, in a fixed
// order so a listing and a table-driven test agree run to run.
func Clients() []Client {
	return []Client{
		{
			ID:           "claude-code",
			Name:         "Claude Code",
			ConfigPath:   func() (string, error) { return homePath(".claude.json") },
			Snippet:      jsonSnippet("mcpServers", envEntry),
			note:         "or .mcp.json at a repo root, which shares the entry with everyone who checks that repo out",
			registration: byVendorCLI,
			vendor:       vendorAdd("claude", true),
			units:        []unit{{asset: skillAsset, at: claudeSkillPath}},
		},
		{
			ID:           "codex",
			Name:         "Codex CLI",
			ConfigPath:   codexConfigPath,
			Snippet:      codexSnippet,
			note:         "or .codex/config.toml in a project Codex trusts, edited by hand — `codex mcp add` always writes the global file",
			registration: byVendorCLI,
			vendor:       vendorAdd("codex", true),
			units:        []unit{{asset: skillAsset, at: agentsSkillPath}},
		},
		{
			ID:         "gemini",
			Name:       "Gemini CLI",
			ConfigPath: func() (string, error) { return homePath(".gemini", "settings.json") },
			Snippet:    jsonSnippet("mcpServers", plainEntry),
			// Gemini CLI's mcp add takes the command as bare positional
			// arguments, with no -- separator between the name and it.
			registration: byVendorCLI,
			vendor:       vendorAdd("gemini", false),
			units:        []unit{{asset: skillAsset, at: geminiSkillPath}},
		},
		{
			ID:           "copilot",
			Name:         "GitHub Copilot CLI",
			ConfigPath:   func() (string, error) { return homePath(".copilot", "mcp-config.json") },
			Snippet:      jsonSnippet("mcpServers", localEntry),
			registration: byVendorCLI,
			vendor:       vendorAdd("copilot", true),
			// Copilot CLI has custom agents rather than skills, in a shape
			// this repo ships no asset for. It reads AGENTS.md and CLAUDE.md
			// from the repo instead, so an install writes it no instruction
			// file rather than inventing one.
		},
		{
			ID:           "cursor",
			Name:         "Cursor",
			ConfigPath:   func() (string, error) { return homePath(".cursor", "mcp.json") },
			Snippet:      jsonSnippet("mcpServers", stdioEntry),
			registration: byMerge,
			merge:        jsonMerge("mcpServers", stdioEntry),
			// Cursor documents .cursor/rules only at the project level, and
			// its global User Rules live in settings rather than in a
			// directory it scans. A file written to a guessed home path would
			// be read by nothing, so this one is printed with the path that is
			// documented and left for the reader to place.
			units: []unit{{
				asset: cursorRuleAsset,
				where: ".cursor/rules/recall.mdc in the workspace",
			}},
		},
		{
			ID:         "opencode",
			Name:       "OpenCode",
			ConfigPath: openCodeConfigPath,
			Snippet:    jsonSnippet("mcp", openCodeEntry),
			// `opencode mcp add` is an interactive wizard with no documented
			// flag syntax, so there is no command to shell out to.
			registration: byMerge,
			merge:        jsonMerge("mcp", openCodeEntry),
			// OpenCode discovers skills from ~/.agents/skills and
			// ~/.claude/skills natively, so the copy Codex's install already
			// writes is the one it reads: no third location.
			units: []unit{{asset: skillAsset, at: agentsSkillPath}},
		},
		{
			ID:           "windsurf",
			Name:         "Windsurf",
			ConfigPath:   func() (string, error) { return homePath(".codeium", "mcp_config.json") },
			Snippet:      jsonSnippet("mcpServers", envEntry),
			registration: byPrinting,
			units: []unit{{
				asset: windsurfRuleAsset,
				where: ".windsurf/rules/recall.md in the workspace (current builds prefer .devin/rules/)",
			}},
		},
	}
}

// Lookup finds a client by id.
func Lookup(id string) (Client, bool) {
	for _, c := range Clients() {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// IDs is every client id, for a usage message that cannot go stale.
func IDs() []string {
	cs := Clients()
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// vendorAdd builds `<tool> mcp add recall [--] <bin> mcp serve`. Every client
// with a registration CLI takes that shape; only Gemini CLI omits the --
// separator, which is why it is a parameter rather than a constant.
func vendorAdd(tool string, separator bool) func(Binary) []string {
	return func(b Binary) []string {
		argv := []string{tool, "mcp", "add", serverName}
		if separator {
			argv = append(argv, "--")
		}
		return append(argv, b.argv()...)
	}
}

// The per-client entry shapes. There is no shared one even at the JSON level:
// Cursor names a stdio type, Copilot CLI a local one, OpenCode takes the whole
// command as an array rather than a command plus args, and Codex is TOML.

func plainEntry(b Binary) any {
	return struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{b.Path, serveArgs}
}

// envEntry carries an empty env object, which is the documented shape for
// Claude Code and Windsurf and the place a reader adds a variable.
func envEntry(b Binary) any {
	return struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}{b.Path, serveArgs, map[string]string{}}
}

func stdioEntry(b Binary) any { return typedEntry("stdio", b) }

func localEntry(b Binary) any { return typedEntry("local", b) }

func typedEntry(kind string, b Binary) any {
	return struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{kind, b.Path, serveArgs}
}

// openCodeEntry is the ecosystem's odd one out: command is the whole argv as
// an array, not a command string with args beside it.
func openCodeEntry(b Binary) any {
	return struct {
		Type    string   `json:"type"`
		Command []string `json:"command"`
	}{"local", b.argv()}
}

// jsonSnippet renders the whole file a client would hold if recall were its
// only server, so a reader can paste it into an empty config or read off the
// one key to merge by hand.
func jsonSnippet(container string, entry func(Binary) any) func(Binary) (string, error) {
	return func(b Binary) (string, error) {
		if err := b.check(); err != nil {
			return "", err
		}
		doc := map[string]any{container: map[string]any{serverName: entry(b)}}
		out, err := atomicfile.Marshal(doc)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
}

// codexSnippet is TOML, which nothing else in recall emits and which is not
// worth a dependency for four lines whose every value is a quoted string.
func codexSnippet(b Binary) (string, error) {
	if err := b.check(); err != nil {
		return "", err
	}
	args := make([]string, len(serveArgs))
	for i, a := range serveArgs {
		args[i] = strconv.Quote(a)
	}
	return fmt.Sprintf("[mcp_servers.%s]\ncommand = %s\nargs = [%s]\n",
		serverName, strconv.Quote(b.Path), strings.Join(args, ", ")), nil
}

// jsonMerge inserts recall's entry into a config file recall owns no schema
// for. Every other key, and every other server beside recall, is carried
// across as the raw bytes it arrived as, so nothing outside recall's own entry
// is re-encoded by a struct this package invented.
func jsonMerge(container string, entry func(Binary) any) merger {
	return func(path string, data []byte, bin Binary) ([]byte, priorEntry, error) {
		var found priorEntry
		doc := map[string]json.RawMessage{}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &doc); err != nil {
				return nil, found, fperr.New(fperr.ArgError,
					"%s is not valid JSON, so recall cannot merge into it safely: %v", path, err)
			}
		}
		servers := map[string]json.RawMessage{}
		if raw, ok := doc[container]; ok {
			if err := json.Unmarshal(raw, &servers); err != nil {
				return nil, found, fperr.New(fperr.ArgError,
					"%s has a %q that is not an object of servers: %v", path, container, err)
			}
		}

		ours, err := json.Marshal(entry(bin))
		if err != nil {
			return nil, found, fperr.New(fperr.Internal, "cannot encode recall's own entry: %v", err)
		}
		if prev, ok := servers[serverName]; ok {
			found.present = true
			found.same = sameJSON(prev, ours)
		}
		servers[serverName] = ours

		encoded, err := json.Marshal(servers)
		if err != nil {
			return nil, found, fperr.New(fperr.Internal, "cannot encode the %q object: %v", container, err)
		}
		doc[container] = encoded

		out, err := atomicfile.Marshal(doc)
		return out, found, err
	}
}

// sameJSON compares two encodings by what they mean rather than by their
// bytes, so a hand-written entry that differs only in key order or spacing is
// the no-op it actually is rather than a conflict a caller has to force past.
func sameJSON(a, b []byte) bool {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func homePath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fperr.New(fperr.Internal, "cannot locate the home directory: %v", err)
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// codexConfigPath follows CODEX_HOME the same way internal/strip's Codex
// provider follows it for the rollout store, so a relocated Codex is told
// about the file it actually reads.
func codexConfigPath() (string, error) {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "config.toml"), nil
	}
	return homePath(".codex", "config.toml")
}

func openCodeConfigPath() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "opencode", "opencode.json"), nil
	}
	return homePath(".config", "opencode", "opencode.json")
}

// The instruction-unit destinations. Each client is written only to the
// directories that client itself reads: SKILL.md under ~/.claude/skills for
// Claude Code, under ~/.agents/skills for Codex and OpenCode — which reads
// both of those natively and so needs no copy of its own — and under
// ~/.gemini/skills for Gemini CLI.

func claudeSkillPath() (string, error) { return homePath(".claude", "skills", serverName, "SKILL.md") }

func agentsSkillPath() (string, error) { return homePath(".agents", "skills", serverName, "SKILL.md") }

func geminiSkillPath() (string, error) { return homePath(".gemini", "skills", serverName, "SKILL.md") }

// Command is the client's own registration command for this binary, or nil
// for a client that ships none. `recall mcp install` runs it rather than
// writing that client's config itself.
func (c Client) Command(b Binary) []string {
	if c.vendor == nil {
		return nil
	}
	return c.vendor(b)
}

// Note is the second place this client also reads the same entry from, where
// it has one.
func (c Client) Note() string { return c.note }

// PrintOnly reports a client recall refuses to write a config file for at all.
// Windsurf is the one: its vendor page and several third-party guides name
// different config paths, and writing to the wrong one is worse than printing
// the right entry.
func (c Client) PrintOnly() bool { return c.registration == byPrinting }
