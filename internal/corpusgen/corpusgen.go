// Package corpusgen generates a synthetic session store shaped like Claude
// Code's ~/.claude/projects, from a seed alone.
//
// It exists so the tests that prove recall's headline behaviour — a needle in
// one checkout of a repo, found from another checkout of the same repo — run on
// a machine that does not hold the author's corpus. The same Spec always
// produces the same tree, so a benchmark taken today compares with one taken a
// year from now. Nothing here reads or writes the real session store.
package corpusgen

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mayberuk/recall/internal/schema"
)

// Plant kinds. A caller reads Plant.Kind to find the needle it needs; the terms
// themselves cannot be named here, because a term written into this file is in
// a transcript the moment an agent reads it, and from then on it is in the
// corpus this tool searches.
const (
	// KindCrossCheckout is carried only by a session recorded under the second
	// checkout of a repo whose first checkout is also in the corpus.
	KindCrossCheckout = "cross-checkout"
	// KindSingleSession is carried by exactly one session.
	KindSingleSession = "single-session"
	// KindResultOnly is carried only by tool output, never by conversation.
	KindResultOnly = "result-only"
	// KindPhrase is several words, each in a different turn of one session, so
	// no single turn carries the phrase in full.
	KindPhrase = "phrase"
)

// Spec describes a corpus to generate. The zero value is invalid; use Small,
// Medium or Large.
type Spec struct {
	Seed         int64
	Projects     int
	SessionsEach int
	// TargetBytes is the size of the generated JSONL, not counting what the
	// corpus root's own absolute path contributes: that path is an argument to
	// Generate rather than something the seed decides, and budgeting against it
	// would make the tree depend on where it was written.
	TargetBytes int64
}

// Corpus is a generated session store, shaped like ~/.claude/projects.
type Corpus struct {
	Root   string // the projects/ directory
	Plants []Plant
}

// Plant is a term written at a known location, so a test can assert on it.
type Plant struct {
	Term    string
	Kind    string
	Session string
	Cwd     string
	Tier    schema.Tier
	Author  schema.Author
	Count   int // turns carrying the term, or one of its words for a phrase

	// otherCwd is the sibling checkout a cross-checkout needle is absent from,
	// which is where a test searching for it must stand.
	otherCwd string
}

// Small is a corpus of about 5 MB, the only size cheap enough for the default
// test path.
func Small() Spec { return Spec{Seed: 1, Projects: 4, SessionsEach: 3, TargetBytes: 5 << 20} }

// Medium is a corpus of about 50 MB, for a benchmark rather than a test.
func Medium() Spec { return Spec{Seed: 2, Projects: 8, SessionsEach: 6, TargetBytes: 50 << 20} }

// Large is a corpus of about 500 MB, within an order of magnitude of the real
// store. Generating it takes seconds and gigabytes of disk.
func Large() Spec { return Spec{Seed: 3, Projects: 16, SessionsEach: 12, TargetBytes: 500 << 20} }

// Generate writes the corpus under dir and returns what it planted. Identical
// Specs produce identical trees: the only difference between two runs under
// different roots is the root path itself, in file names and in the cwd of
// every record.
func Generate(dir string, s Spec) (Corpus, error) {
	if err := s.check(); err != nil {
		return Corpus{}, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Corpus{}, fmt.Errorf("corpusgen: cannot resolve %s: %w", dir, err)
	}
	g := &generator{
		spec:      s,
		rnd:       rand.New(rand.NewSource(s.Seed)),
		root:      []byte(abs),
		projects:  filepath.Join(abs, "projects"),
		checkouts: filepath.Join(abs, "checkouts"),
	}
	if err := g.run(); err != nil {
		return Corpus{}, err
	}
	return Corpus{Root: g.projects, Plants: g.plants}, nil
}

// CrossCheckout returns the plant that exists only in the second checkout of a
// repo whose first checkout is also in the corpus, and that first checkout.
func (c Corpus) CrossCheckout() (needle Plant, otherCwd string, ok bool) {
	for _, p := range c.Plants {
		if p.Kind == KindCrossCheckout {
			return p, p.otherCwd, true
		}
	}
	return Plant{}, "", false
}

// minSessionBytes keeps the byte budget above what one session's fixed record
// shapes already cost, so filler is what closes the gap to TargetBytes rather
// than the structure overshooting it.
const minSessionBytes = 16 << 10

func (s Spec) check() error {
	switch {
	// Four repos is what it takes to give every planted needle a checkout of its
	// own; sharing one would let a test pass on the wrong needle.
	case s.Projects < 4:
		return fmt.Errorf("corpusgen: Projects must be at least 4, got %d", s.Projects)
	case s.SessionsEach < 1:
		return fmt.Errorf("corpusgen: SessionsEach must be at least 1, got %d", s.SessionsEach)
	case s.TargetBytes < int64(s.sessions())*minSessionBytes:
		return fmt.Errorf("corpusgen: TargetBytes %d is below %d for %d sessions",
			s.TargetBytes, int64(s.sessions())*minSessionBytes, s.sessions())
	}
	return nil
}

// sessions counts the whole corpus. One repo has two checkouts, which is what
// makes the cross-checkout property exist at all.
func (s Spec) sessions() int { return (s.Projects + 1) * s.SessionsEach }

// checkout is one working directory a session was recorded from.
type checkout struct {
	repo string
	cwd  string
}

type generator struct {
	spec      Spec
	rnd       *rand.Rand
	root      []byte
	projects  string
	checkouts string
	plants    []Plant
	files     []file
}

type file struct {
	path string
	body []byte
}

func (g *generator) run() error {
	cos := g.plan()
	terms := g.terms()

	for _, co := range cos {
		if err := g.writeCheckout(co); err != nil {
			return err
		}
	}

	budget := g.spec.TargetBytes / int64(g.spec.sessions())
	ordinal := 0
	for i, co := range cos {
		for n := 0; n < g.spec.SessionsEach; n++ {
			g.session(co, cos, g.spotsFor(i, n, len(cos), terms), ordinal, n, budget)
			ordinal++
		}
	}
	return g.flush()
}

// spots decides what one session carries beyond ordinary conversation.
type spots struct {
	cross     string
	single    string
	result    string
	phrase    []string
	dup       bool
	subagent  bool
	relocated bool
}

// spotsFor places each needle on a checkout of its own. The cross-checkout
// needle goes to checkout index 1, the second checkout of the first repo, and
// nowhere else — that placement is the property the whole tool exists for.
func (g *generator) spotsFor(i, n, total int, t plantTerms) spots {
	var s spots
	if n == 0 {
		switch i {
		case 0:
			s.dup, s.subagent = true, true
		case 1:
			s.cross = t.cross
		case 2:
			s.single = t.single
		case 3:
			s.result = t.result
		case 4:
			s.phrase = t.phrase
		}
	}
	s.relocated = i == total-1 && n == g.spec.SessionsEach-1
	return s
}

// plan lists the checkouts in the order sessions are written for them. The
// first repo gets two, whose paths differ only by their numeric suffix, exactly
// as two clones of one repo do on a real machine.
func (g *generator) plan() []checkout {
	var out []checkout
	for r := 0; r < g.spec.Projects; r++ {
		repo := fmt.Sprintf("repo%02d", r)
		n := 1
		if r == 0 {
			n = 2
		}
		for i := 1; i <= n; i++ {
			out = append(out, checkout{
				repo: repo,
				cwd:  filepath.Join(g.checkouts, fmt.Sprintf("%s-%d", repo, i)),
			})
		}
	}
	return out
}

// writeCheckout lays down the git config the checkout is identified by.
// internal/repo reads remote.origin.url out of the file and never runs git, so
// two checkouts sharing an origin resolve to one identity with no git process
// and no network.
func (g *generator) writeCheckout(co checkout) error {
	if err := os.MkdirAll(filepath.Join(co.cwd, ".git"), 0o755); err != nil {
		return fmt.Errorf("corpusgen: cannot create %s: %w", co.cwd, err)
	}
	config := "[core]\n\trepositoryformatversion = 0\n" +
		"[remote \"origin\"]\n\turl = https://git.invalid/corpusgen/" + co.repo + ".git\n"
	path := filepath.Join(co.cwd, ".git", "config")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		return fmt.Errorf("corpusgen: cannot write %s: %w", path, err)
	}
	return nil
}

// plantTerms are the needles, keyed by kind. Every one is drawn from the seeded
// stream in a fixed order.
type plantTerms struct {
	cross  string
	single string
	result string
	phrase []string
}

func (g *generator) terms() plantTerms {
	t := plantTerms{cross: g.term(), single: g.term(), result: g.term()}
	for i := 0; i < 3; i++ {
		t.phrase = append(t.phrase, g.term())
	}
	return t
}

// term derives one needle from the seeded stream. Deriving rather than naming
// it is the point: this tool searches Claude Code transcripts, so a literal
// needle in this file is in the corpus as soon as an agent reads the file, and
// a needle with hits it was not planted at proves nothing.
func (g *generator) term() string {
	return "zq" + strconv.FormatUint(g.rnd.Uint64(), 36)
}

// flush writes every generated file, parents first and paths in sorted order,
// so two runs of one Spec write the same bytes in the same sequence.
func (g *generator) flush() error {
	sort.Slice(g.files, func(i, j int) bool { return g.files[i].path < g.files[j].path })
	for _, f := range g.files {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return fmt.Errorf("corpusgen: cannot create %s: %w", filepath.Dir(f.path), err)
		}
		if err := os.WriteFile(f.path, f.body, 0o644); err != nil {
			return fmt.Errorf("corpusgen: cannot write %s: %w", f.path, err)
		}
	}
	return nil
}

func (g *generator) add(path string, body []byte) { g.files = append(g.files, file{path, body}) }

func (g *generator) projectDir(cwd string) string {
	return filepath.Join(g.projects, encodePath(cwd))
}

// encodePath is Claude Code's project-directory name for a checkout path: every
// character that is not a letter or digit becomes a dash.
func encodePath(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
