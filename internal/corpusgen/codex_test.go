package corpusgen

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

func generateCodex(t *testing.T, spec Spec) (string, Corpus) {
	t.Helper()
	dir := t.TempDir()
	c, err := GenerateCodex(spec, dir)
	if err != nil {
		t.Fatalf("GenerateCodex: %v", err)
	}
	return dir, c
}

// codexTurn is one text-carrying item read back out of a generated rollout,
// enough to check a Plant landed where it says it did.
type codexTurn struct {
	Session string
	CWD     string
	Tier    schema.Tier
	Author  schema.Author
	Text    string
}

// readCodexCorpus parses every rollout structurally — response_item message,
// function_call and function_call_output — with no decoder involved:
// internal/strip has no Codex reader yet, and this package must not depend on
// one while it is under concurrent, temporary mutation.
func readCodexCorpus(t *testing.T, root string) []codexTurn {
	t.Helper()
	var out []codexTurn
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var session, cwd string
		r := jsonl.NewReader(path, bytes.NewReader(body), 0)
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				t.Errorf("%s: a generated line is not a valid JSON object", path)
				continue
			}
			payload := rec.Get("payload")
			switch rec.Get("type").String() {
			case "session_meta":
				session = payload.Get("id").String()
				cwd = payload.Get("cwd").String()
			case "response_item":
				switch payload.Get("type").String() {
				case "message":
					author := schema.AuthorHuman
					if payload.Get("role").String() == "assistant" {
						author = schema.AuthorAssistant
					}
					out = append(out, codexTurn{
						Session: session, CWD: cwd, Tier: schema.TierConversation,
						Author: author, Text: payload.Get("content.0.text").String(),
					})
				case "function_call":
					out = append(out, codexTurn{
						Session: session, CWD: cwd, Tier: schema.TierInvocation,
						Author: schema.AuthorAssistant, Text: payload.Get("arguments").String(),
					})
				case "function_call_output":
					out = append(out, codexTurn{
						Session: session, CWD: cwd, Tier: schema.TierResult,
						Author: schema.AuthorSystem, Text: payload.Get("output").String(),
					})
				}
			}
		}
		if err := r.Err(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read codex corpus at %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no turns under %s", root)
	}
	return out
}

func TestGenerateCodexIsReproducibleFromTheSeedAlone(t *testing.T) {
	first, _ := generateCodex(t, testSpec())
	second, _ := generateCodex(t, testSpec())

	wantHash, wantFiles := treeHash(t, first)
	gotHash, gotFiles := treeHash(t, second)

	if wantFiles == 0 {
		t.Fatal("the generator wrote no files, so comparing two trees proves nothing")
	}
	if gotFiles != wantFiles {
		t.Errorf("file count = %d, want %d", gotFiles, wantFiles)
	}
	if gotHash != wantHash {
		t.Errorf("two runs of one Spec differ: %s vs %s", gotHash, wantHash)
	}
}

func TestADifferentSeedProducesADifferentCodexCorpus(t *testing.T) {
	spec := testSpec()
	first, _ := generateCodex(t, spec)
	spec.Seed++
	second, _ := generateCodex(t, spec)

	a, _ := treeHash(t, first)
	b, _ := treeHash(t, second)
	if a == b {
		t.Error("changing the seed changed nothing, so the corpus is not drawn from it")
	}
}

func TestGenerateCodexRejectsASpecItCannotHonour(t *testing.T) {
	dir := t.TempDir()
	if _, err := GenerateCodex(Spec{}, dir); err == nil {
		t.Fatal("GenerateCodex accepted the zero Spec")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected Spec still wrote %d entries", len(entries))
	}
}

// codexRolloutName matches sessions/YYYY/MM/DD/rollout-<ISO>-<thread>.jsonl,
// the layout GenerateCodex must produce.
var codexRolloutName = regexp.MustCompile(
	`sessions/\d{4}/\d{2}/\d{2}/rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-[0-9a-f-]+\.jsonl$`)

func TestCodexRolloutsAreDatedAndWellFormed(t *testing.T) {
	_, c := generateCodex(t, testSpec())

	shapes := map[string]bool{}
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(filepath.Dir(c.Root), path)
		if relErr != nil {
			return relErr
		}
		if !codexRolloutName.MatchString(filepath.ToSlash(rel)) {
			t.Errorf("%s is not a dated rollout path", rel)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		r := jsonl.NewReader(path, bytes.NewReader(body), 0)
		first := true
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				t.Errorf("%s: a generated line is not a valid JSON object", rel)
				continue
			}
			if first {
				if rec.Get("type").String() != "session_meta" {
					t.Errorf("%s does not open with session_meta", rel)
				}
				first = false
			}
			typ := rec.Get("type").String()
			shapes[typ] = true
			if typ == "response_item" {
				shapes["response_item/"+rec.Get("payload.type").String()] = true
			}
		}
		return r.Err()
	})
	if err != nil {
		t.Fatalf("walk %s: %v", c.Root, err)
	}
	for _, want := range []string{
		"session_meta", "response_item/message", "response_item/function_call", "response_item/function_call_output",
	} {
		if !shapes[want] {
			t.Errorf("the generated corpus carries no %s record", want)
		}
	}
}

// TestEveryCodexPlantLandsWhereItSaysItDoes mirrors
// TestEveryPlantLandsWhereItSaysItDoes: a needle whose recorded coordinates
// are wrong turns every test built on it into a test of nothing.
func TestEveryCodexPlantLandsWhereItSaysItDoes(t *testing.T) {
	_, c := generateCodex(t, testSpec())
	turns := readCodexCorpus(t, c.Root)
	if len(c.Plants) != 4 {
		t.Fatalf("%d plants, want one of each kind", len(c.Plants))
	}

	for _, p := range c.Plants {
		t.Run(p.Kind, func(t *testing.T) {
			if p.Kind == KindPhrase {
				assertCodexPhrase(t, turns, p)
				return
			}
			var carrying []codexTurn
			for _, turn := range turns {
				if strings.Contains(turn.Text, p.Term) {
					carrying = append(carrying, turn)
				}
			}
			if len(carrying) != p.Count {
				t.Fatalf("%d turns carry the term, want Count = %d", len(carrying), p.Count)
			}
			for _, turn := range carrying {
				if turn.Session != p.Session {
					t.Errorf("term found in session %s, want only %s", turn.Session, p.Session)
				}
				if turn.CWD != p.Cwd {
					t.Errorf("term found under cwd %s, want only %s", turn.CWD, p.Cwd)
				}
				if turn.Tier != p.Tier {
					t.Errorf("term found in the %s tier, want %s", turn.Tier, p.Tier)
				}
				if turn.Author != p.Author {
					t.Errorf("term attributed to %s, want %s", turn.Author, p.Author)
				}
			}
		})
	}
}

func assertCodexPhrase(t *testing.T, turns []codexTurn, p Plant) {
	t.Helper()
	words := strings.Fields(p.Term)
	if len(words) != p.Count {
		t.Fatalf("phrase has %d words but Count is %d", len(words), p.Count)
	}
	found := map[string]int{}
	for _, turn := range turns {
		carried := 0
		for _, w := range words {
			if strings.Contains(turn.Text, w) {
				found[w]++
				carried++
			}
		}
		if carried > 1 {
			t.Errorf("one turn carries %d of the phrase's words; no turn may carry more than one", carried)
		}
		if carried > 0 && turn.Session != p.Session {
			t.Errorf("a phrase word is in session %s, want only %s", turn.Session, p.Session)
		}
	}
	for _, w := range words {
		if found[w] != 1 {
			t.Errorf("a phrase word appears in %d turns, want exactly 1", found[w])
		}
	}
}

// TestCodexCrossCheckoutNeedleIsAbsentFromItsSiblingCheckout is the Codex
// generator's half of the property Generate's cross-checkout needle exists
// for: a needle reachable from the checkout a search runs in would make the
// repo-wide scope untested.
func TestCodexCrossCheckoutNeedleIsAbsentFromItsSiblingCheckout(t *testing.T) {
	_, c := generateCodex(t, testSpec())
	needle, otherCwd, ok := c.CrossCheckout()
	if !ok {
		t.Fatal("the corpus carries no cross-checkout needle")
	}
	if otherCwd == "" || otherCwd == needle.Cwd {
		t.Fatalf("sibling checkout %q is not a second directory", otherCwd)
	}

	for _, turn := range readCodexCorpus(t, c.Root) {
		if strings.Contains(turn.Text, needle.Term) && turn.CWD == otherCwd {
			t.Fatalf("the needle is reachable from the checkout the search runs from (session %s)", turn.Session)
		}
	}
}
