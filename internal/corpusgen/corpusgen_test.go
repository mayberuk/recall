package corpusgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// testSpec is Small shrunk to what a unit test needs. Small itself is 5 MB and
// every property here holds at any size, so the default test path pays for the
// structure and not for the filler.
func testSpec() Spec {
	s := Small()
	s.TargetBytes = int64(s.sessions()) * minSessionBytes
	return s
}

func generate(t *testing.T, spec Spec) (string, Corpus) {
	t.Helper()
	dir := t.TempDir()
	c, err := Generate(dir, spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return dir, c
}

const rootToken = "{{ROOT}}"

// normalize substitutes the corpus root out of one file's name or contents,
// raw as it appears in every record's cwd and dash-encoded as it appears in
// every project directory name. The root is an argument to Generate rather than
// something the seed decides, so comparing two trees with it left in compares
// two inputs. Every other difference — an unseeded draw, a wall clock, map
// iteration order — survives this and still shows.
func normalize(b []byte, dir string) []byte {
	b = bytes.ReplaceAll(b, []byte(dir), []byte(rootToken))
	return bytes.ReplaceAll(b, []byte(encodePath(dir)), []byte(rootToken))
}

func treeHash(t *testing.T, dir string) (string, int) {
	t.Helper()
	h := sha256.New()
	files := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(normalize([]byte(rel), dir))
		h.Write([]byte{0})
		h.Write(normalize(body, dir))
		h.Write([]byte{0})
		files++
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil)), files
}

func TestGenerateIsReproducibleFromTheSeedAlone(t *testing.T) {
	first, _ := generate(t, testSpec())
	second, _ := generate(t, testSpec())

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

func TestADifferentSeedProducesADifferentCorpus(t *testing.T) {
	spec := testSpec()
	first, _ := generate(t, spec)
	spec.Seed++
	second, _ := generate(t, spec)

	a, _ := treeHash(t, first)
	b, _ := treeHash(t, second)
	if a == b {
		t.Error("changing the seed changed nothing, so the corpus is not drawn from it")
	}
}

func TestGeneratedBytesLandNearTheTarget(t *testing.T) {
	spec := Small()
	dir, c := generate(t, spec)
	t.Cleanup(func() { os.RemoveAll(dir) })

	var total int64
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += int64(len(body) - bytes.Count(body, []byte(dir))*len(dir))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", c.Root, err)
	}
	slack := spec.TargetBytes / 20
	if diff := total - spec.TargetBytes; diff > slack || diff < -slack {
		t.Errorf("generated %d bytes of JSONL discounting the root path, want %d ±5%% (±%d)",
			total, spec.TargetBytes, slack)
	}
}

// corpusFile is one generated transcript, read back the way recall reads it.
type corpusFile struct {
	rel     string
	records []jsonl.Record
	turns   []schema.Turn
}

func readCorpus(t *testing.T, root string) []corpusFile {
	t.Helper()
	var out []corpusFile
	st := strip.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f := corpusFile{rel: rel}
		r := jsonl.NewReader(path, bytes.NewReader(body), 0)
		for r.Next() {
			rec, ok := jsonl.ParseStrict(r.Line())
			if !ok {
				t.Errorf("%s: a generated line is not a valid JSON object", rel)
				continue
			}
			f.records = append(f.records, rec)
			turns, _ := st.Strip(rec)
			f.turns = append(f.turns, turns...)
		}
		if err := r.Err(); err != nil {
			return err
		}
		out = append(out, f)
		return nil
	})
	if err != nil {
		t.Fatalf("read corpus at %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no transcripts under %s", root)
	}
	return out
}

// TestEveryPathologyTheStoreCarriesIsPresent pins the record shapes strip has
// to survive. A generator that quietly stopped emitting one would make every
// test built on it weaker without failing anything.
func TestEveryPathologyTheStoreCarriesIsPresent(t *testing.T) {
	_, c := generate(t, testSpec())
	files := readCorpus(t, c.Root)

	shapes := map[string]bool{}
	uuidFiles := map[string]map[string]bool{}
	for _, f := range files {
		for _, rec := range f.records {
			h := rec.Header()
			if h.UUID != "" {
				if uuidFiles[h.UUID] == nil {
					uuidFiles[h.UUID] = map[string]bool{}
				}
				uuidFiles[h.UUID][f.rel] = true
			}
			switch {
			case h.Type == "relocated" && h.RelocatedCWD != "" && h.CWD == "":
				shapes["relocated"] = true
			case h.Type == "user" && h.PromptSource == "typed":
				shapes["typed"] = true
			case h.Type == "user" && !h.HasPromptSource:
				shapes["untyped-user"] = true
			}
			if h.IsSidechain && strings.Contains(f.rel, "subagents") {
				shapes["sidechain-sidecar"] = true
			}
			if strings.Contains(string(rec.Raw()), "<command-args>") {
				shapes["command-args"] = true
			}
			for i := 0; i < int(rec.Get("message.content.#").Int()); i++ {
				shapes[rec.Get("message.content."+strconv.Itoa(i)+".type").String()] = true
			}
		}
	}
	for _, uuids := range uuidFiles {
		if len(uuids) > 1 {
			shapes["duplicated-uuid"] = true
		}
	}

	for _, want := range []string{
		"relocated", "typed", "untyped-user", "command-args", "sidechain-sidecar",
		"text", "thinking", "tool_use", "tool_result", "duplicated-uuid",
	} {
		if !shapes[want] {
			t.Errorf("the generated corpus carries no %s record", want)
		}
	}
}

// TestEveryPlantLandsWhereItSaysItDoes is what makes the plants usable as
// assertions: a needle whose recorded coordinates are wrong turns every test
// built on it into a test of nothing.
func TestEveryPlantLandsWhereItSaysItDoes(t *testing.T) {
	_, c := generate(t, testSpec())
	files := readCorpus(t, c.Root)
	if len(c.Plants) != 4 {
		t.Fatalf("%d plants, want one of each kind", len(c.Plants))
	}

	for _, p := range c.Plants {
		t.Run(p.Kind, func(t *testing.T) {
			if p.Kind == KindPhrase {
				assertPhrase(t, files, p)
				return
			}
			var carrying []schema.Turn
			for _, f := range files {
				for _, turn := range f.turns {
					if strings.Contains(turn.Text, p.Term) {
						carrying = append(carrying, turn)
					}
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

// assertPhrase holds the property the relaxed-match path needs: every word is
// somewhere in the session and no single turn carries them all, so a query for
// the whole phrase can only be answered by degrading to a partial match.
func assertPhrase(t *testing.T, files []corpusFile, p Plant) {
	t.Helper()
	words := strings.Fields(p.Term)
	if len(words) != p.Count {
		t.Fatalf("phrase has %d words but Count is %d", len(words), p.Count)
	}
	found := map[string]int{}
	for _, f := range files {
		for _, turn := range f.turns {
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
	}
	for _, w := range words {
		if found[w] != 1 {
			t.Errorf("a phrase word appears in %d turns, want exactly 1", found[w])
		}
	}
}

// TestTheCrossCheckoutNeedleIsAbsentFromItsSiblingCheckout is the generator's
// half of the property the whole tool exists for. If the needle were reachable
// from the checkout the search is run in, an integration test standing there
// would pass without the repo-wide scope ever being exercised.
func TestTheCrossCheckoutNeedleIsAbsentFromItsSiblingCheckout(t *testing.T) {
	_, c := generate(t, testSpec())
	needle, otherCwd, ok := c.CrossCheckout()
	if !ok {
		t.Fatal("the corpus carries no cross-checkout needle")
	}
	if otherCwd == "" || otherCwd == needle.Cwd {
		t.Fatalf("sibling checkout %q is not a second directory", otherCwd)
	}

	rr := repo.New()
	here, there := rr.Resolve(otherCwd), rr.Resolve(needle.Cwd)
	if here.Kind != repo.KindRemote {
		t.Fatalf("checkout %s resolved to %s, want a remote identity", otherCwd, here.Kind)
	}
	if here.ID != there.ID {
		t.Fatalf("the two checkouts resolve to different repos: %s vs %s", here.ID, there.ID)
	}

	for _, f := range readCorpus(t, c.Root) {
		for _, turn := range f.turns {
			if strings.Contains(turn.Text, needle.Term) && turn.CWD == otherCwd {
				t.Fatalf("%s carries the needle in the checkout the search runs from", f.rel)
			}
		}
	}
}

// TestEveryPresetIsGenerable checks the sizes without writing them. Medium and
// Large are 50 MB and 500 MB and belong to a benchmark, but a preset that no
// test ever validates can ship with a Spec Generate refuses.
func TestEveryPresetIsGenerable(t *testing.T) {
	for name, spec := range map[string]Spec{"Small": Small(), "Medium": Medium(), "Large": Large()} {
		if err := spec.check(); err != nil {
			t.Errorf("%s() is not a Spec Generate accepts: %v", name, err)
		}
	}
}

func TestGenerateRejectsASpecItCannotHonour(t *testing.T) {
	for name, spec := range map[string]Spec{
		"too few projects": {Seed: 1, Projects: 3, SessionsEach: 2, TargetBytes: 1 << 30},
		"no sessions":      {Seed: 1, Projects: 4, SessionsEach: 0, TargetBytes: 1 << 30},
		"target too small": {Seed: 1, Projects: 4, SessionsEach: 2, TargetBytes: 1024},
		"zero value":       {},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Generate(dir, spec); err == nil {
				t.Error("Generate accepted a Spec it cannot honour")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read %s: %v", dir, err)
			}
			if len(entries) != 0 {
				t.Errorf("a rejected Spec still wrote %d entries", len(entries))
			}
		})
	}
}
