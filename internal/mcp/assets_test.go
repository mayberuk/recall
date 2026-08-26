package mcp

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// triggerPhrasings are the exact question shapes the SKILL.md description
// must carry, verbatim, so an agent's own request text matches against them.
// Table-driven so a phrase that silently drops out of the description names
// itself in the failure, rather than a single "description looks wrong".
var triggerPhrasings = []string{
	"what did we decide about",
	"where did we leave off",
	"have we hit this before",
	"which session was that",
	"when did we first",
	"before re-deriving something a past session already settled",
}

// frontmatter is the parsed form of a YAML frontmatter block, good enough for
// the flat, single-line key/value pairs every asset in this package uses.
// It is not a general YAML parser: it exists so the tests can assert on the
// actual parsed value of a field rather than grepping raw bytes, without
// pulling in a YAML dependency this package doesn't otherwise need.
type frontmatter struct {
	values map[string]string
}

func parseFrontmatter(t *testing.T, data []byte) frontmatter {
	t.Helper()

	const delim = "---\n"
	if !bytes.HasPrefix(data, []byte(delim)) {
		t.Fatalf("frontmatter does not start at byte 0: first bytes %q", firstBytes(data, 8))
	}
	rest := data[len(delim):]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		t.Fatalf("frontmatter has no closing ---")
	}

	fm := frontmatter{values: map[string]string{}}
	for _, line := range strings.Split(string(rest[:end]), "\n") {
		key, val, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		fm.values[key] = val
	}
	return fm
}

func firstBytes(data []byte, n int) []byte {
	if len(data) < n {
		n = len(data)
	}
	return data[:n]
}

func readAsset(t *testing.T, path string) []byte {
	t.Helper()
	data, err := Assets.ReadFile(path)
	if err != nil {
		t.Fatalf("Assets.ReadFile(%q): %v", path, err)
	}
	return data
}

// TestSkillFrontmatterStartsAtByteZero guards the failure mode the standards
// file calls out by name: a blank line before the opening --- drops the whole
// frontmatter block silently, and the skill simply never loads.
func TestSkillFrontmatterStartsAtByteZero(t *testing.T) {
	data := readAsset(t, "assets/skills/recall/SKILL.md")
	if !bytes.HasPrefix(data, []byte("---\n")) {
		t.Fatalf("SKILL.md must open with --- at byte 0, got %q", firstBytes(data, 8))
	}
	if data[0] == '\n' || data[0] == ' ' || data[0] == '\t' {
		t.Fatalf("SKILL.md has leading whitespace before its frontmatter")
	}
	// A UTF-8 BOM is three bytes (EF BB BF) and would also displace byte 0.
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("SKILL.md carries a UTF-8 BOM before its frontmatter")
	}
}

func TestSkillFrontmatterDeclaresName(t *testing.T) {
	fm := parseFrontmatter(t, readAsset(t, "assets/skills/recall/SKILL.md"))
	if got := fm.values["name"]; got != "recall" {
		t.Fatalf(`name = %q, want "recall"`, got)
	}
}

func TestSkillDescriptionIsOneLineUnder1024NoColonSpace(t *testing.T) {
	fm := parseFrontmatter(t, readAsset(t, "assets/skills/recall/SKILL.md"))
	desc, ok := fm.values["description"]
	if !ok {
		t.Fatalf("SKILL.md frontmatter has no description field")
	}
	if strings.Contains(desc, "\n") {
		t.Fatalf("description spans more than one line: %q", desc)
	}
	if len(desc) == 0 {
		t.Fatalf("description is empty")
	}
	if len(desc) > 1024 {
		t.Fatalf("description is %d bytes, over the 1024 limit", len(desc))
	}
	if strings.Contains(desc, ": ") {
		t.Fatalf("description contains a colon-space sequence, which drops the whole frontmatter: %q", desc)
	}
}

func TestSkillDescriptionCarriesTriggerPhrasings(t *testing.T) {
	fm := parseFrontmatter(t, readAsset(t, "assets/skills/recall/SKILL.md"))
	desc := fm.values["description"]
	for _, phrase := range triggerPhrasings {
		phrase := phrase
		t.Run(phrase, func(t *testing.T) {
			if !strings.Contains(desc, phrase) {
				t.Fatalf("description does not contain the phrasing %q:\n%s", phrase, desc)
			}
		})
	}
}

// skillContentClaims are substrings the shipped SKILL.md must carry so its prose
// cannot silently fall out of step with what internal/scan actually does. Table-
// driven so a claim that drops out of the file names itself in the failure,
// the same shape TestSkillDescriptionCarriesTriggerPhrasings already uses.
var skillContentClaims = map[string]string{
	"identifier-boundary ranking":                                 "camelCase or acronym boundary ranks that match as a whole word",
	"near-neighbor correction on a miss, stated as one-edit-only": "corrected to a one-edit neighbor",
	"the shipped, curated synonym table":                          "shipped synonym table",
	"the MCP default token budget":                                "4000 tokens by default",
}

func TestSkillDescribesTheNewMatchingBehavior(t *testing.T) {
	text := string(readAsset(t, "assets/skills/recall/SKILL.md"))
	for what, claim := range skillContentClaims {
		what, claim := what, claim
		t.Run(what, func(t *testing.T) {
			if !strings.Contains(text, claim) {
				t.Fatalf("SKILL.md does not describe %s (looked for %q)", what, claim)
			}
		})
	}
}

// TestSkillDoesNotClaimIdentifierMatchRanksBelowWholeWord is the negative
// control for TestSkillDescribesTheNewMatchingBehavior's identifier-ranking
// claim: classify (internal/scan/match.go) now ranks a case-boundary match AS
// a whole word, so the pre-change claim that it ranks below one is false
// wherever it survives.
func TestSkillDoesNotClaimIdentifierMatchRanksBelowWholeWord(t *testing.T) {
	text := string(readAsset(t, "assets/skills/recall/SKILL.md"))
	if strings.Contains(text, "ranked below a whole-word match") {
		t.Fatalf("SKILL.md still claims an identifier match ranks below a whole-word match; classify no longer does that")
	}
}

func TestCursorRuleDeclaresDescriptionAndNotAlwaysApply(t *testing.T) {
	fm := parseFrontmatter(t, readAsset(t, "assets/rules/recall.mdc"))
	if desc, ok := fm.values["description"]; !ok || desc == "" {
		t.Fatalf("Cursor rule has no description, got %q (present=%v)", desc, ok)
	}
	if got, ok := fm.values["alwaysApply"]; ok && got == "true" {
		t.Fatalf("Cursor rule declares alwaysApply: true, which spends context on every turn")
	}
}

// TestFrontmatterParserDistinguishesAlwaysApplyTrueFromFalse is the negative
// control for the assertion above: it proves the parser can actually see
// alwaysApply: true when it is present, so the passing case above is not
// passing merely because the parser never looks at the field.
func TestFrontmatterParserDistinguishesAlwaysApplyTrueFromFalse(t *testing.T) {
	trueDoc := []byte("---\ndescription: x\nalwaysApply: true\n---\nbody\n")
	falseDoc := []byte("---\ndescription: x\nalwaysApply: false\n---\nbody\n")

	fmTrue := parseFrontmatter(t, trueDoc)
	if fmTrue.values["alwaysApply"] != "true" {
		t.Fatalf("parser failed to read alwaysApply: true")
	}
	fmFalse := parseFrontmatter(t, falseDoc)
	if fmFalse.values["alwaysApply"] != "false" {
		t.Fatalf("parser failed to read alwaysApply: false")
	}
}

func TestWindsurfRuleDeclaresModelDecisionTrigger(t *testing.T) {
	fm := parseFrontmatter(t, readAsset(t, "assets/rules/recall-windsurf.md"))
	if got := fm.values["trigger"]; got != "model_decision" {
		t.Fatalf(`trigger = %q, want "model_decision"`, got)
	}
}

// pluginJSONFiles is every manifest that must parse as JSON, named
// explicitly rather than discovered by a walk-and-count: a manifest that
// silently stops existing must fail this test by name, not by a changed
// count that could mean anything.
var pluginJSONFiles = []string{
	"assets/plugins/claude-code/.claude-plugin/plugin.json",
	"assets/plugins/claude-code/.claude-plugin/marketplace.json",
	"assets/plugins/claude-code/.mcp.json",
	"assets/plugins/codex/.codex-plugin/plugin.json",
	"assets/plugins/gemini/gemini-extension.json",
}

func TestPluginManifestsParseAsJSON(t *testing.T) {
	for _, path := range pluginJSONFiles {
		path := path
		t.Run(path, func(t *testing.T) {
			data := readAsset(t, path)
			var v map[string]any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Fatalf("%s does not parse as JSON: %v", path, err)
			}
		})
	}
}

func TestClaudeCodePluginSkillIsByteIdenticalToCanonical(t *testing.T) {
	canonical := readAsset(t, "assets/skills/recall/SKILL.md")
	bundled := readAsset(t, "assets/plugins/claude-code/skills/recall/SKILL.md")
	if !bytes.Equal(canonical, bundled) {
		t.Fatalf("Claude Code plugin's SKILL.md diverges from the canonical one")
	}
}

func TestCodexPluginSkillIsByteIdenticalToCanonical(t *testing.T) {
	canonical := readAsset(t, "assets/skills/recall/SKILL.md")
	bundled := readAsset(t, "assets/plugins/codex/skills/recall/SKILL.md")
	if !bytes.Equal(canonical, bundled) {
		t.Fatalf("Codex plugin's SKILL.md diverges from the canonical one")
	}
}

// TestEmbedIncludesDotPrefixedVendorPaths guards the //go:embed directive
// itself: Go's embed silently drops any file or directory named with a
// leading "." or "_" unless the pattern carries the "all:" prefix, and three
// of the paths a client vendor requires (.claude-plugin/, .codex-plugin/,
// .mcp.json) are dot-prefixed. Dropping "all:" from the directive would make
// this test fail by way of a read error, not a content mismatch.
func TestEmbedIncludesDotPrefixedVendorPaths(t *testing.T) {
	dotPaths := []string{
		"assets/plugins/claude-code/.claude-plugin/plugin.json",
		"assets/plugins/claude-code/.claude-plugin/marketplace.json",
		"assets/plugins/claude-code/.mcp.json",
		"assets/plugins/codex/.codex-plugin/plugin.json",
	}
	for _, path := range dotPaths {
		if _, err := Assets.ReadFile(path); err != nil {
			t.Fatalf("Assets.ReadFile(%q): %v (embed directive likely missing its all: prefix)", path, err)
		}
	}
}

func TestWithinDir(t *testing.T) {
	cases := []struct {
		name        string
		dir, target string
		wantWithin  bool
	}{
		{"same dir", "/a/b", "/a/b", true},
		{"direct child", "/a/b", "/a/b/c", true},
		{"nested descendant", "/a/b", "/a/b/c/d", true},
		{"sibling with shared prefix", "/a/b", "/a/bc", false},
		{"parent of dir", "/a/b", "/a", false},
		{"unrelated sibling", "/a/b", "/a/c", false},
		{"escape via dotdot-cleaned path", "/a/b", "/a/b/../c", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withinDir(c.dir, c.target); got != c.wantWithin {
				t.Fatalf("withinDir(%q, %q) = %v, want %v", c.dir, c.target, got, c.wantWithin)
			}
		})
	}
}

// TestExportAssetsReproducesCheckedInIntegrations is the test the whole
// integrations/ tree exists to satisfy: a fresh export must be byte-for-byte
// what is checked in, in both directions, so a file present in one tree and
// missing from the other fails here rather than only a content diff on files
// that happen to exist in both.
func TestExportAssetsReproducesCheckedInIntegrations(t *testing.T) {
	tmp := t.TempDir()
	if err := ExportAssets(tmp); err != nil {
		t.Fatalf("ExportAssets: %v", err)
	}

	checkedIn := filepath.Join("..", "..", "integrations")
	if _, err := os.Stat(checkedIn); err != nil {
		t.Fatalf("checked-in integrations/ tree not found at %s: %v", checkedIn, err)
	}

	checkedInFiles := collectFiles(t, checkedIn)
	exportedFiles := collectFiles(t, tmp)

	for rel := range checkedInFiles {
		if _, ok := exportedFiles[rel]; !ok {
			t.Errorf("integrations/%s is checked in but ExportAssets did not produce it", rel)
		}
	}
	for rel := range exportedFiles {
		if _, ok := checkedInFiles[rel]; !ok {
			t.Errorf("ExportAssets produced %s but it is not checked in under integrations/", rel)
		}
	}
	for rel, wantData := range checkedInFiles {
		gotData, ok := exportedFiles[rel]
		if !ok {
			continue // already reported above
		}
		if !bytes.Equal(wantData, gotData) {
			t.Errorf("integrations/%s differs from the fresh export", rel)
		}
	}
}

func collectFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}
