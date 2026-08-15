package bench

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mayberuk/recall/internal/corpusgen"
	"github.com/mayberuk/recall/internal/schema"
)

// Size names a generated corpus. Large exists for a deliberate one-off run and
// is never taken by the default measurement path: it is 500 MB on disk.
type Size string

const (
	SizeSmall  Size = "small"
	SizeMedium Size = "medium"
	SizeLarge  Size = "large"
)

// Spec is the generator input for a size.
func (s Size) Spec() (corpusgen.Spec, error) {
	switch s {
	case SizeSmall:
		return corpusgen.Small(), nil
	case SizeMedium:
		return corpusgen.Medium(), nil
	case SizeLarge:
		return corpusgen.Large(), nil
	}
	return corpusgen.Spec{}, fmt.Errorf("bench: unknown corpus size %q", s)
}

// Sizes is what a measurement run covers.
var Sizes = []Size{SizeSmall, SizeMedium}

// CorpusRootEnv names a directory holding one subdirectory per size, generated
// and owned by whoever set the variable. It is how `make bench` hands one
// corpus to the several `go test` processes it starts, instead of each of them
// writing 50 MB of its own.
const CorpusRootEnv = "RECALL_BENCH_CORPUS_ROOT"

// Generated is a corpus on disk plus the directory to point HOME at. Every
// location recall reads is derived from HOME, so a scenario that sets it is
// searching this corpus and provably not the operator's own session store.
type Generated struct {
	corpusgen.Corpus
	Size Size
	Home string
}

var corpora struct {
	sync.Mutex
	bySize map[Size]Generated
	owned  []string
}

// Corpus returns the generated corpus of the given size, writing it at most
// once per process. The generator is deterministic, so the cached tree is the
// tree a fresh call would write; regenerating it per benchmark function would
// spend more time laying down the corpus than measuring anything.
func Corpus(size Size) (Generated, error) {
	corpora.Lock()
	defer corpora.Unlock()
	if g, ok := corpora.bySize[size]; ok {
		return g, nil
	}
	spec, err := size.Spec()
	if err != nil {
		return Generated{}, err
	}

	home, shared := sharedHome(size)
	if !shared {
		dir, err := os.MkdirTemp(scratchRoot(), string(size)+"-")
		if err != nil {
			return Generated{}, fmt.Errorf("bench: cannot create a corpus directory: %w", err)
		}
		home = dir
		corpora.owned = append(corpora.owned, dir)
	}

	g, err := loadOrGenerate(home, size, spec)
	if err != nil {
		return Generated{}, err
	}
	if corpora.bySize == nil {
		corpora.bySize = map[Size]Generated{}
	}
	corpora.bySize[size] = g
	return g, nil
}

// Cleanup removes the corpora this process generated. A corpus handed over
// through CorpusRootEnv belongs to the process that set the variable and is
// left alone.
func Cleanup() {
	corpora.Lock()
	defer corpora.Unlock()
	for _, dir := range corpora.owned {
		_ = os.RemoveAll(dir)
	}
	corpora.owned = nil
	corpora.bySize = nil
}

// scratchRoot keeps every generated corpus under one named directory, so a run
// killed before Cleanup leaves something a reader can recognise and delete.
func scratchRoot() string {
	root := filepath.Join(os.TempDir(), "recall-bench")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return os.TempDir()
	}
	return root
}

func sharedHome(size Size) (string, bool) {
	root := os.Getenv(CorpusRootEnv)
	if root == "" {
		return "", false
	}
	home := filepath.Join(root, string(size))
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", false
	}
	return home, true
}

// plantsFile records what was planted where. The corpus itself is reproducible
// from the seed, but Generate is the only thing that reports the needles, and a
// second process reusing the tree needs them without rewriting it.
const plantsFile = "plants.json"

func loadOrGenerate(home string, size Size, spec corpusgen.Spec) (Generated, error) {
	root := filepath.Join(home, ".claude", "projects")
	if plants, err := readPlants(filepath.Join(home, plantsFile)); err == nil {
		if _, err := os.Stat(root); err == nil {
			return Generated{Corpus: corpusgen.Corpus{Root: root, Plants: plants}, Size: size, Home: home}, nil
		}
	}
	c, err := corpusgen.Generate(filepath.Join(home, ".claude"), spec)
	if err != nil {
		return Generated{}, fmt.Errorf("bench: cannot generate the %s corpus: %w", size, err)
	}
	if err := writePlants(filepath.Join(home, plantsFile), c.Plants); err != nil {
		return Generated{}, err
	}
	return Generated{Corpus: c, Size: size, Home: home}, nil
}

func readPlants(path string) ([]corpusgen.Plant, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plants []corpusgen.Plant
	if err := json.Unmarshal(body, &plants); err != nil {
		return nil, err
	}
	if len(plants) == 0 {
		return nil, fmt.Errorf("bench: %s lists no plants", path)
	}
	return plants, nil
}

func writePlants(path string, plants []corpusgen.Plant) error {
	body, err := json.MarshalIndent(plants, "", "  ")
	if err != nil {
		return fmt.Errorf("bench: cannot encode the plant list: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("bench: cannot write %s: %w", path, err)
	}
	return nil
}

// Plant returns the needle of a kind, which is what a benchmark queries for: a
// planted term has a hit count fixed by the seed, where an English word picked
// by hand has whatever count this run of the generator happened to give it.
func (g Generated) Plant(kind string) (corpusgen.Plant, error) {
	for _, p := range g.Plants {
		if p.Kind == kind {
			return p, nil
		}
	}
	return corpusgen.Plant{}, fmt.Errorf("bench: the %s corpus carries no %s plant", g.Size, kind)
}

// Share is a tier's fraction of the corpus's turns and of its stripped bytes.
func (c CorpusFacts) Share(tier schema.Tier) (turns, bytes float64) {
	var allTurns int
	var allBytes int64
	for _, t := range c.Tiers {
		allTurns += t.Turns
		allBytes += t.Bytes
	}
	for _, t := range c.Tiers {
		if t.Tier != tier {
			continue
		}
		if allTurns > 0 {
			turns = float64(t.Turns) / float64(allTurns)
		}
		if allBytes > 0 {
			bytes = float64(t.Bytes) / float64(allBytes)
		}
	}
	return turns, bytes
}

// CorpusShape states how the measured corpora compare with the session store
// corpusgen sized itself against: the reference split, and the widest miss
// across every size and tier. The report says it in prose because a reader
// deciding whether to trust an all-tier number is really asking how much of
// this corpus is tool output.
func CorpusShape(corpora []CorpusFacts) string {
	real := corpusgen.RealStore()
	turnRef := make([]string, 0, len(real))
	byteRef := make([]string, 0, len(real))
	widest := 0.0
	for _, s := range real {
		turnRef = append(turnRef, fmt.Sprintf("%.1f%%", 100*s.TurnShare))
		byteRef = append(byteRef, fmt.Sprintf("%.1f%%", 100*s.ByteShare))
		for _, c := range corpora {
			turns, bytes := c.Share(s.Tier)
			widest = math.Max(widest, math.Max(miss(turns, s.TurnShare), miss(bytes, s.ByteShare)))
		}
	}
	return fmt.Sprintf(
		"conversation, invocation and result hold %s of the turns and %s of the bytes in the "+
			"store internal/corpusgen measured, and every corpus above lands within %.0f%% of "+
			"each of those six shares",
		strings.Join(turnRef, " / "), strings.Join(byteRef, " / "), math.Ceil(100*widest))
}

func miss(got, want float64) float64 {
	if want == 0 {
		return 0
	}
	return math.Abs(got-want) / want
}

// Files is every transcript in the corpus, in a fixed order so two runs strip
// the same bytes in the same sequence.
func Files(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("bench: cannot walk %s: %w", root, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("bench: no transcripts under %s", root)
	}
	sort.Strings(files)
	return files, nil
}
