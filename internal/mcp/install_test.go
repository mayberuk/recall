package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
)

// recorder stands in for the vendor's own registration CLI, so a test can
// assert the exact argv without claude, codex, gemini or copilot installed on
// the machine running it.
type recorder struct {
	ran     [][]string
	missing bool
	fail    error
}

func (r *recorder) options() InstallOptions {
	return InstallOptions{
		LookPath: func(name string) (string, error) {
			if r.missing {
				return "", exec.ErrNotFound
			}
			return filepath.Join("/usr/local/bin", name), nil
		},
		Run: func(argv []string) error {
			r.ran = append(r.ran, argv)
			return r.fail
		},
	}
}

func client(t *testing.T, id string) Client {
	t.Helper()
	c, ok := Lookup(id)
	if !ok {
		t.Fatalf("no client recipe for %q", id)
	}
	return c
}

func configPathOf(t *testing.T, c Client) string {
	t.Helper()
	path, err := c.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("does not parse as JSON: %v\n%s", err, data)
	}
	return got
}

func absent(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists, but %s", path, why)
	}
}

// cliBackedClients are the four that ship their own registration command.
var cliBackedClients = []string{"claude-code", "codex", "gemini", "copilot"}

// TestInstallShellsOutToTheVendorCLIAndWritesNoConfigFile is criterion 3. The
// claim is not that install succeeded — it is that the vendor's own command
// ran, with recall's absolute path in it, and that recall did not write that
// client's config file itself.
func TestInstallShellsOutToTheVendorCLIAndWritesNoConfigFile(t *testing.T) {
	for _, id := range cliBackedClients {
		t.Run(id, func(t *testing.T) {
			tempHome(t)
			c := client(t, id)
			path := configPathOf(t, c)
			vendor := &recorder{}

			plan, err := Install(c, testBinary, vendor.options())
			if err != nil {
				t.Fatalf("Install: %v", err)
			}
			if len(vendor.ran) != 1 {
				t.Fatalf("the vendor CLI ran %d times, want once: %v", len(vendor.ran), vendor.ran)
			}
			if want := c.Command(testBinary); !slices.Equal(vendor.ran[0], want) {
				t.Errorf("ran %v, want %v", vendor.ran[0], want)
			}
			absent(t, path, "the vendor CLI owns that file and recall must not write it")
			if slices.Contains(plan.Paths(), path) {
				t.Errorf("the plan lists %s among the paths it writes: %v", path, plan.Paths())
			}
		})
	}
}

// TestInstallRefusesAndWritesNothingWhenTheVendorCLIIsMissing is criterion 4:
// the fallback is a refusal, never recall writing the vendor's file itself.
func TestInstallRefusesAndWritesNothingWhenTheVendorCLIIsMissing(t *testing.T) {
	for _, id := range cliBackedClients {
		t.Run(id, func(t *testing.T) {
			home := tempHome(t)
			c := client(t, id)
			before := snapshot(t, home)
			vendor := &recorder{missing: true}

			_, err := Install(c, testBinary, vendor.options())
			if err == nil {
				t.Fatal("Install succeeded with the vendor CLI off PATH")
			}
			if got := codeOf(t, err); got != fperr.ToolMissing {
				t.Errorf("error code = %q, want %q", got, fperr.ToolMissing)
			}
			if !strings.Contains(err.Error(), configPathOf(t, c)) {
				t.Errorf("the refusal does not name the file to edit by hand: %v", err)
			}
			if len(vendor.ran) != 0 {
				t.Errorf("a missing binary was run anyway: %v", vendor.ran)
			}
			assertUnchanged(t, "install with the vendor CLI missing", before, snapshot(t, home))
		})
	}
}

// TestAFailingVendorCLIIsReportedAndNothingIsWrittenInstead keeps the fallback
// from quietly becoming "recall writes the file after all".
func TestAFailingVendorCLIIsReportedAndNothingIsWrittenInstead(t *testing.T) {
	tempHome(t)
	c := client(t, "claude-code")
	vendor := &recorder{fail: errors.New("exit status 1")}

	_, err := Install(c, testBinary, vendor.options())
	if err == nil {
		t.Fatal("Install reported success after its vendor command failed")
	}
	if !strings.Contains(err.Error(), "claude mcp add") {
		t.Errorf("the failure does not name the command that failed: %v", err)
	}
	absent(t, configPathOf(t, c), "a failing vendor command must not fall back to recall writing the config")
}

// mergeBackedClients are the two recall merges into itself, because neither
// ships a non-interactive registration command.
var mergeBackedClients = []string{"cursor", "opencode"}

// TestAMergeCopiesTheOriginalAndChangesOnlyItsOwnKey is criterion 5. The
// backup is asserted to hold the original bytes rather than merely to exist,
// and every foreign key in the file is compared before and after.
func TestAMergeCopiesTheOriginalAndChangesOnlyItsOwnKey(t *testing.T) {
	for _, id := range mergeBackedClients {
		t.Run(id, func(t *testing.T) {
			tempHome(t)
			c := client(t, id)
			path := configPathOf(t, c)
			container := containerKeyOf(t, c)

			original := `{
  "theme": "dark",
  "` + container + `": {
    "other": {"type": "local", "command": "/usr/bin/other"}
  }
}
`
			writeFile(t, path, original)

			if _, err := Install(c, testBinary, (&recorder{}).options()); err != nil {
				t.Fatalf("Install: %v", err)
			}

			backup := readFile(t, path+backupSuffix)
			if !bytes.Equal(backup, []byte(original)) {
				t.Errorf("the backup does not hold the original bytes:\ngot\n%s\nwant\n%s", backup, original)
			}

			before, after := decode(t, []byte(original)), decode(t, readFile(t, path))
			if !reflect.DeepEqual(before["theme"], after["theme"]) {
				t.Errorf("a key recall does not own changed: theme %v -> %v", before["theme"], after["theme"])
			}
			if len(before) != len(after) {
				t.Errorf("top-level keys went from %v to %v", keysOf(before), keysOf(after))
			}
			beforeServers, _ := before[container].(map[string]any)
			afterServers, _ := after[container].(map[string]any)
			if !reflect.DeepEqual(beforeServers["other"], afterServers["other"]) {
				t.Errorf("another server's entry changed: %v -> %v", beforeServers["other"], afterServers["other"])
			}
			if afterServers[serverName] == nil {
				t.Fatalf("recall's own entry is missing after the merge:\n%s", readFile(t, path))
			}
			if !reflect.DeepEqual(afterServers[serverName], entryOf(t, c)) {
				t.Errorf("recall's entry is %v, want the one `recall mcp config %s` prints: %v",
					afterServers[serverName], c.ID, entryOf(t, c))
			}
		})
	}
}

// containerKeyOf is the key this client keeps its servers under, read out of
// its own snippet so the test cannot disagree with the recipe.
func containerKeyOf(t *testing.T, c Client) string {
	t.Helper()
	snippet, err := c.Snippet(testBinary)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	for key := range decode(t, []byte(snippet)) {
		return key
	}
	t.Fatalf("%s's snippet has no container key", c.ID)
	return ""
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestAMergeIntoAFileThatIsNotThereYetCreatesItWithoutABackup keeps an empty
// backup from being written over a good config by a later restore.
func TestAMergeIntoAFileThatIsNotThereYetCreatesItWithoutABackup(t *testing.T) {
	tempHome(t)
	c := client(t, "cursor")
	path := configPathOf(t, c)

	if _, err := Install(c, testBinary, (&recorder{}).options()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the config file was not created: %v", err)
	}
	absent(t, path+backupSuffix, "there was no original to preserve")
}

// TestADifferingEntryIsRefusedUnlessForced is criterion 6, with the forced
// case beside it so the refusal is not just an install that never works.
func TestADifferingEntryIsRefusedUnlessForced(t *testing.T) {
	home := tempHome(t)
	c := client(t, "cursor")
	path := configPathOf(t, c)
	original := `{"mcpServers":{"recall":{"type":"stdio","command":"/somewhere/else/recall","args":["mcp","serve"]}}}` + "\n"
	writeFile(t, path, original)
	before := snapshot(t, home)

	_, err := Install(c, testBinary, (&recorder{}).options())
	if err == nil {
		t.Fatal("a differing recall entry was replaced without --force")
	}
	if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("error code = %q, want %q", got, fperr.ArgError)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("the refusal does not name the way past it: %v", err)
	}
	assertUnchanged(t, "a refused install", before, snapshot(t, home))

	opt := (&recorder{}).options()
	opt.Force = true
	if _, err := Install(c, testBinary, opt); err != nil {
		t.Fatalf("Install --force: %v", err)
	}
	if got := readFile(t, path); bytes.Equal(got, []byte(original)) {
		t.Error("--force left the differing entry in place")
	}
	if got := readFile(t, path+backupSuffix); !bytes.Equal(got, []byte(original)) {
		t.Errorf("the backup does not hold the entry that was replaced:\n%s", got)
	}
	if entry := decode(t, readFile(t, path))["mcpServers"].(map[string]any)[serverName]; !reflect.DeepEqual(entry, entryOf(t, c)) {
		t.Errorf("the replaced entry is %v, want %v", entry, entryOf(t, c))
	}
}

// TestAnIdenticalEntryIsANoOpNotAnError keeps a second install from being a
// refusal a caller has to force past for no reason.
func TestAnIdenticalEntryIsANoOpNotAnError(t *testing.T) {
	home := tempHome(t)
	c := client(t, "cursor")
	if _, err := Install(c, testBinary, (&recorder{}).options()); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	after := snapshot(t, home)

	plan, err := Install(c, testBinary, (&recorder{}).options())
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	assertUnchanged(t, "a second identical install", after, snapshot(t, home))
	for _, s := range plan.Steps {
		// Printing again is free and says nothing false; touching the disk
		// again is what a second identical install must not do.
		if s.Action != ActionSkip && s.Action != ActionPrint {
			t.Errorf("the second install still planned to %s %s", s.Action, s.Path)
		}
	}
}

// TestWindsurfPrintsAndWritesNoClientConfig is criterion 7.
func TestWindsurfPrintsAndWritesNoClientConfig(t *testing.T) {
	home := tempHome(t)
	c := client(t, "windsurf")
	path := configPathOf(t, c)
	before := snapshot(t, home)

	plan, err := Install(c, testBinary, (&recorder{}).options())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	assertUnchanged(t, "install windsurf", before, snapshot(t, home))
	absent(t, path, "recall never writes Windsurf's config")
	if paths := plan.Paths(); len(paths) != 0 {
		t.Errorf("the plan writes %v, want nothing", paths)
	}

	snippet, err := c.Snippet(testBinary)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	var printed []string
	for _, s := range plan.Steps {
		if s.Action != ActionPrint {
			t.Fatalf("windsurf planned a %s step on %s", s.Action, s.Path)
		}
		printed = append(printed, string(s.Content))
	}
	if !slices.Contains(printed, snippet) {
		t.Errorf("the entry itself is not among what install prints: %v", printed)
	}
	rule := string(readAsset(t, windsurfRuleAsset))
	if !slices.Contains(printed, rule) {
		t.Error("the Windsurf rule is not among what install prints")
	}
}

// TestDryRunListsEveryPathItWouldTouchAndTouchesNone is criterion 8. The
// listing is checked against what a real run then actually creates, so a plan
// that omits a path it later writes fails here rather than surprising someone.
func TestDryRunListsEveryPathItWouldTouchAndTouchesNone(t *testing.T) {
	for _, c := range Clients() {
		t.Run(c.ID, func(t *testing.T) {
			home := tempHome(t)
			before := snapshot(t, home)

			vendor := &recorder{}
			opt := vendor.options()
			opt.DryRun = true
			plan, err := Install(c, testBinary, opt)
			if err != nil {
				t.Fatalf("Install --dry-run: %v", err)
			}
			assertUnchanged(t, "a dry run", before, snapshot(t, home))
			if len(vendor.ran) != 0 {
				t.Errorf("a dry run ran %v", vendor.ran)
			}

			if _, err := Install(c, testBinary, (&recorder{}).options()); err != nil {
				t.Fatalf("Install: %v", err)
			}
			assertPlannedEverything(t, home, plan, before, snapshot(t, home))
		})
	}
}

// assertPlannedEverything compares what the dry run said it would touch with
// what the real run actually created: every new file has to have been listed,
// and every new directory has to lie on the way to a listed one.
func assertPlannedEverything(t *testing.T, home string, plan Plan, before, after map[string]string) {
	t.Helper()
	var files, dirs []string
	for _, s := range plan.Steps {
		rel, err := filepath.Rel(home, s.Path)
		if err != nil {
			continue
		}
		switch s.Action {
		case ActionWrite, ActionBackup:
			files = append(files, rel)
		case ActionMkdir:
			dirs = append(dirs, rel)
		}
	}

	for path := range after {
		if prev, ok := before[path]; ok && prev == after[path] {
			continue
		}
		if dir, ok := strings.CutSuffix(path, "/"); ok {
			if !onTheWayTo(dir, dirs) {
				t.Errorf("the real run created the directory %s, which no planned mkdir leads to: %v", dir, dirs)
			}
			continue
		}
		if !slices.Contains(files, path) {
			t.Errorf("the real run wrote %s, which the dry run did not list: %v", path, files)
		}
	}
	for _, f := range files {
		if _, ok := after[f]; !ok {
			t.Errorf("the dry run listed %s, which the real run never wrote", f)
		}
	}
}

func onTheWayTo(dir string, planned []string) bool {
	for _, p := range planned {
		if p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// TestInstallWritesTheInstructionUnitEachClientActuallyReads pins the
// destinations by hand: a skill written where its client does not look for it
// is an install that silently does nothing.
func TestInstallWritesTheInstructionUnitEachClientActuallyReads(t *testing.T) {
	want := map[string][]string{
		"claude-code": {".claude/skills/recall/SKILL.md"},
		"codex":       {".agents/skills/recall/SKILL.md"},
		"gemini":      {".gemini/skills/recall/SKILL.md"},
		"opencode":    {".agents/skills/recall/SKILL.md"},
		// Cursor and Windsurf write nothing for different reasons: Cursor
		// documents .cursor/rules only inside a workspace, and Windsurf's own
		// docs and its third-party guides disagree about its config path.
		"cursor":   nil,
		"copilot":  nil,
		"windsurf": nil,
	}
	for _, c := range Clients() {
		t.Run(c.ID, func(t *testing.T) {
			home := tempHome(t)
			if _, err := Install(c, testBinary, (&recorder{}).options()); err != nil {
				t.Fatalf("Install: %v", err)
			}
			targets, ok := want[c.ID]
			if !ok {
				t.Fatalf("no expected instruction unit is written down for %s", c.ID)
			}
			for _, rel := range targets {
				got := readFile(t, filepath.Join(home, rel))
				asset := skillAsset
				if strings.HasSuffix(rel, ".mdc") {
					asset = cursorRuleAsset
				}
				if !bytes.Equal(got, readAsset(t, asset)) {
					t.Errorf("%s does not hold the embedded %s", rel, asset)
				}
			}
			if len(targets) == 0 {
				for _, rel := range []string{
					".claude/skills/recall/SKILL.md",
					".agents/skills/recall/SKILL.md",
					".gemini/skills/recall/SKILL.md",
					".cursor/rules/recall.mdc",
				} {
					absent(t, filepath.Join(home, rel), c.ID+" reads no instruction unit recall can place")
				}
			}
		})
	}
}

// TestNoInstallEverWritesUnderATranscriptStore is criterion 11. The stores are
// planted with content first, so this is a comparison of real bytes rather
// than a check that two empty directories are still empty.
func TestNoInstallEverWritesUnderATranscriptStore(t *testing.T) {
	home := tempHome(t)
	stores := []string{".claude/projects", ".codex/sessions", ".gemini/tmp"}
	for _, rel := range stores {
		writeFile(t, filepath.Join(home, rel, "session.jsonl"), `{"type":"user","text":"a past session"}`+"\n")
	}
	planted := map[string]map[string]string{}
	for _, rel := range stores {
		planted[rel] = snapshot(t, filepath.Join(home, rel))
	}

	for _, c := range Clients() {
		opt := (&recorder{}).options()
		opt.Force = true
		if _, err := Install(c, testBinary, opt); err != nil {
			t.Fatalf("Install %s: %v", c.ID, err)
		}
	}
	for _, rel := range stores {
		assertUnchanged(t, "installing every client, against "+rel, planted[rel], snapshot(t, filepath.Join(home, rel)))
	}
}

func TestInstallRefusesARelativeBinary(t *testing.T) {
	home := tempHome(t)
	before := snapshot(t, home)
	for _, c := range Clients() {
		if _, err := Install(c, Binary{Path: "recall"}, (&recorder{}).options()); err == nil {
			t.Errorf("%s accepted a relative binary path", c.ID)
		}
	}
	assertUnchanged(t, "install with a relative binary", before, snapshot(t, home))
}

// TestPlanTextNamesEveryPathAndCommand keeps --dry-run's own output honest:
// the reader decides from this text, so every path in the plan has to be in
// it.
func TestPlanTextNamesEveryPathAndCommand(t *testing.T) {
	tempHome(t)
	c := client(t, "cursor")
	opt := (&recorder{}).options()
	opt.DryRun = true
	plan, err := Install(c, testBinary, opt)
	if err != nil {
		t.Fatalf("Install --dry-run: %v", err)
	}
	text := plan.Text()
	for _, path := range plan.Paths() {
		if !strings.Contains(text, path) {
			t.Errorf("the plan's text does not name %s:\n%s", path, text)
		}
	}

	claude := client(t, "claude-code")
	plan, err = Install(claude, testBinary, opt)
	if err != nil {
		t.Fatalf("Install --dry-run: %v", err)
	}
	if want := strings.Join(claude.Command(testBinary), " "); !strings.Contains(plan.Text(), want) {
		t.Errorf("the plan's text does not name the command it would run (%s):\n%s", want, plan.Text())
	}
}

// TestPlanTextDeclaresWhatIsOnlyPrintedAndWhatIsNotInstalledAtAll keeps the
// two silent outcomes visible in the same listing as the writes.
func TestPlanTextDeclaresWhatIsOnlyPrintedAndWhatIsNotInstalledAtAll(t *testing.T) {
	tempHome(t)
	opt := (&recorder{}).options()
	opt.DryRun = true

	windsurf, err := Install(client(t, "windsurf"), testBinary, opt)
	if err != nil {
		t.Fatalf("Install windsurf: %v", err)
	}
	if !strings.Contains(windsurf.Text(), "print") {
		t.Errorf("the windsurf plan does not say its steps are only printed:\n%s", windsurf.Text())
	}

	copilot, err := Install(client(t, "copilot"), testBinary, opt)
	if err != nil {
		t.Fatalf("Install copilot: %v", err)
	}
	if !strings.Contains(copilot.Text(), "no instruction file") {
		t.Errorf("the copilot plan does not say no instruction file is installed:\n%s", copilot.Text())
	}
}

// TestTheDefaultRunnerShellsOutForReal proves the seam these tests inject at
// is a seam and not the implementation: with nothing injected, a vendor
// command really is executed and its output reaches the caller's own stream.
func TestTheDefaultRunnerShellsOutForReal(t *testing.T) {
	var log bytes.Buffer
	opt := InstallOptions{Log: &log}
	if err := opt.run([]string{"echo", "recall-was-here"}); err != nil {
		t.Fatalf("running a real command: %v", err)
	}
	if !strings.Contains(log.String(), "recall-was-here") {
		t.Errorf("the command's own output did not reach the log: %q", log.String())
	}
	if _, err := opt.lookPath("go"); err != nil {
		t.Errorf("looking up a binary that is certainly on PATH: %v", err)
	}
	if _, err := opt.lookPath("recall-no-such-binary"); err == nil {
		t.Error("looking up a binary that does not exist reported success")
	}

	// A nil Log discards rather than writing to a stream this package does not
	// own — under serve, one of them is the protocol.
	if err := (InstallOptions{}).run([]string{"echo", "quiet"}); err != nil {
		t.Fatalf("running with no log: %v", err)
	}
}

// TestInstallFailsCleanlyWhenAPathCannotBeUsed covers the filesystem going
// against the plan. Each case is a refusal with a code, never a partial
// install and never a panic.
func TestInstallFailsCleanlyWhenAPathCannotBeUsed(t *testing.T) {
	t.Run("the config file is a directory", func(t *testing.T) {
		home := tempHome(t)
		c := client(t, "cursor")
		if err := os.MkdirAll(configPathOf(t, c), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		before := snapshot(t, home)
		if _, err := Install(c, testBinary, (&recorder{}).options()); err == nil {
			t.Fatal("Install succeeded against a config path that is a directory")
		}
		assertUnchanged(t, "a config path that cannot be read", before, snapshot(t, home))
	})

	t.Run("the config file's parent is a file", func(t *testing.T) {
		home := tempHome(t)
		c := client(t, "cursor")
		writeFile(t, filepath.Dir(configPathOf(t, c)), "not a directory")
		_, err := Install(c, testBinary, (&recorder{}).options())
		if err == nil {
			t.Fatal("Install succeeded with a file where the config directory goes")
		}
		if got := codeOf(t, err); got != fperr.ArgError {
			t.Errorf("error code = %q, want %q", got, fperr.ArgError)
		}
		absent(t, filepath.Join(home, ".cursor", "rules", "recall.mdc"),
			"a failed install must not go on to write the rest of its plan")
	})

	t.Run("the instruction unit's path is a directory", func(t *testing.T) {
		home := tempHome(t)
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "recall", "SKILL.md"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := Install(client(t, "claude-code"), testBinary, (&recorder{}).options()); err == nil {
			t.Fatal("Install succeeded with a directory where SKILL.md goes")
		}
	})

	t.Run("there is no home directory", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("CODEX_HOME", "")
		if _, err := Install(client(t, "cursor"), testBinary, (&recorder{}).options()); err == nil {
			t.Skip("this platform resolves a home directory without HOME")
		}
	})

	t.Run("the config path resolves but the skill path does not", func(t *testing.T) {
		t.Setenv("CODEX_HOME", t.TempDir())
		t.Setenv("HOME", "")
		c := client(t, "codex")
		if _, err := c.ConfigPath(); err != nil {
			t.Skipf("this platform cannot resolve a config path without HOME: %v", err)
		}
		if _, err := Install(c, testBinary, (&recorder{}).options()); err == nil {
			t.Fatal("Install succeeded with nowhere to put the instruction file")
		}
	})
}
