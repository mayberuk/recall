// Package differential holds recall's optimization safety net: the binary under
// test must produce byte-identical output to a known-good baseline over a fixed
// query battery.
//
// The tool's whole value is that a search does not silently miss anything, so an
// optimization that moves one hit, one offset, or one coverage figure has broken
// the product even when every unit test still passes. Unit tests check the
// behaviour someone thought to assert; this checks the behaviour that shipped.
// The bar is exact bytes, not "equivalent", because a diff that needs judgment
// is a diff that gets waved through.
package differential

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mayberuk/recall/internal/corpusgen"
	"github.com/mayberuk/recall/internal/scan"
)

// baselineEnv names the git ref the current tree is compared against. The
// default is the tag the performance work started from, so a contributor who
// clones and runs the suite compares against the same point every recorded
// measurement was taken at.
const baselineEnv = "RECALL_DIFF_BASE"

const defaultBaseline = "perf-baseline"

// archivePlaceholder is the one normalization applied before comparing. Each
// binary gets its own archive directory so that neither run's output can depend
// on the other's writes, which means any verb that prints where the archive
// lives — `doctor` does — would differ on the directory name alone. Nothing else
// is rewritten: a difference anywhere outside this path is a real difference.
const archivePlaceholder = "«ARCHIVE»"

// headTurnsPerRange makes the binary under test cut the corpus into concurrent
// ranges that this corpus is otherwise far too small to reach: 5 MB strips to
// about 3,000 turns against a default floor of 2,048 per range, and the
// conversation tier most of the battery searches is a fifth of that. Without it
// every case compares two single-pass scans, and the concurrent path — the thing
// the performance work actually changed — is never run here at all.
//
// It is set for the head binary alone. The baseline predates sharding, so its
// output is the single-pass answer by construction and is what head must match.
const headTurnsPerRange = "64"

// pair is the two binaries under comparison, plus the corpus they both read.
type pair struct {
	base, head string // binary paths
	ref        string // the git ref base was built from
	home       string
	corpus     corpusgen.Corpus
	baseHome   string // archive directory for base
	headHome   string // archive directory for head
	checkout   string // a checkout inside the corpus, used as the working directory
}

var (
	setupOnce sync.Once
	shared    *pair
	setupSkip string
	setupErr  error
)

// battery is one invocation run against both binaries.
type battery struct {
	name string
	args []string

	// dir, when set, overrides the working directory. A search's scope is the
	// repo the process stands in, so the directory is part of the query.
	dir string
}

// cases is the query battery. It covers every shape the matcher distinguishes
// and every flag that changes which turns are examined or how they are counted,
// because those are the flags an optimization can break.
//
// Nothing here uses --no-update: that path prints how stale the archive is in
// words, which is a function of wall-clock time and would make two runs of the
// same query differ for reasons that are not the code's fault.
func cases(p *pair) []battery {
	needle := p.corpus.Plants[0]
	single := p.corpus.Plants[1]
	resultOnly := p.corpus.Plants[2]
	phrase := p.corpus.Plants[3]
	phraseWords := strings.Fields(phrase.Term)

	// A term no generated turn carries. Built from a plant's term so it cannot
	// collide with corpus text, then altered past any stem of it.
	miss := needle.Term + "qqzz"

	return []battery{
		{name: "single-term hit", args: []string{"find", needle.Term, "--all"}},
		{name: "single-term hit, scoped to the sibling checkout", args: []string{"find", needle.Term}, dir: p.checkout},
		{name: "total miss", args: []string{"find", miss, "--all"}},
		{name: "two terms, both present", args: []string{"find", phraseWords[0] + " " + phraseWords[1], "--all"}},
		{name: "two terms, relaxed to the best partial", args: []string{"find", needle.Term + " " + single.Term, "--all"}},
		{name: "two terms, all-terms refuses the partial", args: []string{"find", needle.Term + " " + single.Term, "--all", "--all-terms"}},
		{name: "quoted phrase", args: []string{"find", `"` + phrase.Term + `"`, "--all"}},
		{name: "quoted phrase, words reordered so it cannot match", args: []string{"find", `"` + phraseWords[1] + " " + phraseWords[0] + `"`, "--all"}},
		{name: "exact, no stem expansion", args: []string{"find", needle.Term, "--all", "--exact"}},
		{name: "excluded term", args: []string{"find", phraseWords[0], "--all", "--not", phraseWords[1]}},
		{name: "two excluded terms", args: []string{"find", phraseWords[0], "--all", "--not", phraseWords[1], "--not", phraseWords[2]}},
		{name: "stopwords dropped from a long query", args: []string{"find", "the " + needle.Term + " of the path", "--all"}},

		{name: "result tier searched", args: []string{"find", resultOnly.Term, "--all", "--results"}},
		{name: "result tier not searched", args: []string{"find", resultOnly.Term, "--all"}},
		{name: "every tier", args: []string{"find", resultOnly.Term, "--all", "--results", "--tools"}},

		{name: "brief", args: []string{"find", needle.Term, "--all", "--brief"}},
		{name: "ids only", args: []string{"find", needle.Term, "--all", "--ids"}},
		{name: "json", args: []string{"find", needle.Term, "--all", "--json"}},
		{name: "jsonl", args: []string{"find", needle.Term, "--all", "--format", "jsonl"}},
		{name: "budget", args: []string{"find", needle.Term, "--all", "--budget", "400"}},
		{name: "limit and hits", args: []string{"find", phraseWords[0], "--all", "--limit", "2", "--hits", "1"}},
		{name: "author", args: []string{"find", needle.Term, "--all", "--author", "assistant"}},
		{name: "author with no match", args: []string{"find", needle.Term, "--all", "--author", "human"}},
		{name: "sorted by recency", args: []string{"find", phraseWords[0], "--all", "--sort", "recent"}},
		{name: "since", args: []string{"find", needle.Term, "--all", "--since", "5000d"}},
		{name: "until", args: []string{"find", needle.Term, "--all", "--until", "5000d"}},
		{name: "scoped by repo identity", args: []string{"find", needle.Term, "--repo", repoIdentity(needle.Cwd)}},
		{name: "scoped by session", args: []string{"find", needle.Term, "--all", "--session", needle.Session}},

		// The cases above query planted needles, which each appear once. That
		// makes them precise and makes them blind: a matcher that mishandles a
		// second occurrence inside one turn agrees with the baseline on every one
		// of them. These query the corpus's own filler vocabulary instead, where a
		// term recurs several times per turn and across most turns, so the
		// occurrence walk, the per-turn hit cap and the ranking of dense turns are
		// all exercised.
		// "timeout" and "worker" are here for a specific reason: the substring
		// search anchors itself on the rarest byte of the needle, and for these two
		// that is not the first byte. Every other term in this battery — the
		// planted needles, and "value" — happens to open with a byte rare enough
		// that the search hands off to the standard library, so without these the
		// anchored path would go unexercised and a battery-wide pass would mean
		// nothing about it.
		{name: "term anchored past its first byte", args: []string{"find", "timeout", "--all"}},
		{name: "term anchored past its first byte, every tier", args: []string{"find", "timeout", "--all", "--results", "--tools"}},
		{name: "two anchored terms", args: []string{"find", "timeout worker", "--all", "--all-terms"}},
		{name: "anchored term as a phrase", args: []string{"find", `"worker timeout"`, "--all"}},
		{name: "anchored term excluded", args: []string{"find", "worker", "--all", "--not", "timeout"}},
		{name: "anchored term, json", args: []string{"find", "timeout", "--all", "--json"}},
		{name: "anchored term, turns", args: []string{"turns", "timeout", "--all", "--limit", "3"}},

		{name: "common term, many hits per turn", args: []string{"find", "value", "--all"}},
		{name: "common term, every tier", args: []string{"find", "value", "--all", "--results", "--tools"}},
		{name: "common term, stem expanded", args: []string{"find", "values", "--all"}},
		{name: "common term, exact so the stem does not apply", args: []string{"find", "values", "--all", "--exact"}},
		{name: "three common terms", args: []string{"find", "value return path", "--all"}},
		{name: "three common terms, all required", args: []string{"find", "value return path", "--all", "--all-terms"}},
		{name: "common terms as a phrase", args: []string{"find", `"value return"`, "--all"}},
		{name: "common term excluding another", args: []string{"find", "value", "--all", "--not", "return"}},
		{name: "common term, brief", args: []string{"find", "value", "--all", "--brief"}},
		{name: "common term, json", args: []string{"find", "value", "--all", "--json"}},
		{name: "common term, hit cap per session", args: []string{"find", "value", "--all", "--hits", "2"}},
		{name: "common term, budget", args: []string{"find", "value", "--all", "--budget", "800"}},
		{name: "common term, turns", args: []string{"turns", "value", "--all", "--limit", "3"}},
		{name: "common term, when", args: []string{"when", "value", "--all"}},
		{name: "common term, show", args: []string{"show", needle.Session, "value"}},

		// A miss reports, per term, how many turns carry it on its own and — for a
		// term nothing carries — the corpus words closest to it. A single-term miss
		// skips the counting walk, because one term that matched nothing is already
		// known to be carried by no turn, and the one multi-term miss above has both
		// terms present, so it never asks for a suggestion. These are the shapes
		// where counting and suggesting happen in the same run and both have to
		// survive being gathered a range at a time.
		{name: "multi-term miss, one term the corpus carries",
			args: []string{"find", "timeout " + miss, "--all", "--all-terms"}},
		{name: "multi-term miss, one term the corpus carries, json",
			args: []string{"find", "timeout " + miss, "--all", "--all-terms", "--json"}},
		{name: "multi-term miss, nothing carries either term",
			args: []string{"find", miss + " " + single.Term + "qqzz", "--all", "--all-terms"}},

		{name: "turns", args: []string{"turns", needle.Term, "--all"}},
		{name: "turns, every tier", args: []string{"turns", resultOnly.Term, "--all", "--results", "--tools"}},
		{name: "turns, json", args: []string{"turns", needle.Term, "--all", "--json"}},
		{name: "turns, char cap", args: []string{"turns", needle.Term, "--all", "--chars", "60"}},
		{name: "when", args: []string{"when", needle.Term, "--all"}},
		{name: "when, json", args: []string{"when", needle.Term, "--all", "--json"}},
		{name: "show, anchored on a query", args: []string{"show", needle.Session, needle.Term}},
		{name: "show, json", args: []string{"show", needle.Session, needle.Term, "--json"}},
		{name: "doctor", args: []string{"doctor"}},
		{name: "guide", args: []string{"guide"}},
	}
}

// TestOutputIsByteIdenticalToTheBaseline is the gate every optimization passes
// before it merges.
func TestOutputIsByteIdenticalToTheBaseline(t *testing.T) {
	p := setup(t)
	for _, c := range cases(p) {
		t.Run(c.name, func(t *testing.T) {
			dir := c.dir
			if dir == "" {
				dir = p.corpus.Plants[0].Cwd
			}
			base := p.run(t, p.base, p.baseHome, dir, c.args)
			head := p.run(t, p.head, p.headHome, dir, c.args)
			if base.equal(head) {
				return
			}
			t.Errorf("`recall %s` in %s differs from %s\n%s",
				strings.Join(c.args, " "), shortDir(dir), p.ref, diff(base, head))
		})
	}
}

// TestTheBatteryActuallyExercisesHitsAndMisses guards the battery itself. A
// query battery that silently stopped matching anything would agree with the
// baseline on every case and prove nothing, which is the failure mode a
// differential harness is most exposed to.
func TestTheBatteryActuallyExercisesHitsAndMisses(t *testing.T) {
	p := setup(t)
	hits, misses := 0, 0
	for _, c := range cases(p) {
		if c.args[0] != "find" {
			continue
		}
		dir := c.dir
		if dir == "" {
			dir = p.corpus.Plants[0].Cwd
		}
		switch code := p.run(t, p.head, p.headHome, dir, c.args).code; code {
		case 0:
			hits++
		case 1:
			misses++
		default:
			t.Errorf("`recall %s` exited %d; the battery expects a hit or a clean miss",
				strings.Join(c.args, " "), code)
		}
	}
	// The battery names 12 searches that should match and 5 that should not. The
	// floors are set below those counts so that reordering a case is not a
	// failure, while a battery that has stopped searching is.
	if hits < 10 {
		t.Errorf("only %d of the find cases matched anything; the battery is not exercising the hit path", hits)
	}
	if misses < 3 {
		t.Errorf("only %d of the find cases missed; the battery is not exercising the empty-result path", misses)
	}
}

type output struct {
	stdout, stderr string
	code           int
}

func (o output) equal(other output) bool {
	return o.stdout == other.stdout && o.stderr == other.stderr && o.code == other.code
}

// rangeFloor is headTurnsPerRange for the binary under test and empty for the
// baseline, which leaves the baseline on whatever it was built to do.
func (p *pair) rangeFloor(binary string) string {
	if binary == p.head {
		return headTurnsPerRange
	}
	return ""
}

func (p *pair) run(t *testing.T, binary, archive, dir string, args []string) output {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	// Every path recall reads is derived from these, so a variable leaking in
	// from the developer's shell cannot point either binary at the real session
	// store. An empty value reads as unset.
	cmd.Env = append(os.Environ(),
		"HOME="+p.home,
		"RECALL_HOME="+archive,
		"XDG_DATA_HOME=",
		"CLAUDE_PROJECTS_DIR=",
		"CLAUDE_CODE_SESSION_ID=",
		"NO_COLOR=1",
		"TERM=dumb",
		"COLUMNS=100",
		scan.RangeFloorEnv+"="+p.rangeFloor(binary),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s %v: %v", filepath.Base(binary), args, err)
		}
		code = exit.ExitCode()
	}
	return output{
		stdout: strings.ReplaceAll(stdout.String(), archive, archivePlaceholder),
		stderr: strings.ReplaceAll(stderr.String(), archive, archivePlaceholder),
		code:   code,
	}
}

// diff reports the first differing line of each stream, which is enough to name
// what moved without printing two whole outputs.
func diff(base, head output) string {
	var b strings.Builder
	if base.code != head.code {
		fmt.Fprintf(&b, "exit code: baseline %d, this tree %d\n", base.code, head.code)
	}
	firstDiff(&b, "stdout", base.stdout, head.stdout)
	firstDiff(&b, "stderr", base.stderr, head.stderr)
	return b.String()
}

func firstDiff(b *strings.Builder, stream, base, head string) {
	if base == head {
		return
	}
	bl, hl := strings.Split(base, "\n"), strings.Split(head, "\n")
	for i := 0; i < len(bl) || i < len(hl); i++ {
		x, y := at(bl, i), at(hl, i)
		if x == y {
			continue
		}
		fmt.Fprintf(b, "%s line %d:\n  baseline:  %q\n  this tree: %q\n", stream, i+1, x, y)
		if n := len(bl) - len(hl); n != 0 {
			fmt.Fprintf(b, "  (%s has %d line(s) the other does not)\n", longer(n), abs(n))
		}
		return
	}
}

func at(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<no line>"
}

func longer(n int) string {
	if n > 0 {
		return "baseline"
	}
	return "this tree"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func shortDir(dir string) string { return filepath.Base(dir) }

// repoIdentity is the identity recall resolves a generated checkout to. Two
// clones of one repo differ only by the numeric suffix on the directory name,
// and share the origin url the identity is read from.
func repoIdentity(cwd string) string {
	name := filepath.Base(cwd)
	if i := strings.LastIndex(name, "-"); i > 0 {
		name = name[:i]
	}
	return "git.invalid/corpusgen/" + name
}

// setup builds both binaries and the corpus once for the package. The baseline
// is built from a detached worktree rather than by stashing, so a run cannot
// disturb the tree being tested — the diff is the record of what changed, and a
// harness that touches it is a harness that can lie about it.
func setup(t *testing.T) *pair {
	t.Helper()
	setupOnce.Do(func() { shared, setupSkip, setupErr = build() })
	switch {
	case setupErr != nil:
		t.Fatalf("differential setup: %v", setupErr)
	case setupSkip != "":
		t.Skip(setupSkip)
	}
	return shared
}

func build() (*pair, string, error) {
	root, err := repoRoot()
	if err != nil {
		return nil, "", err
	}
	ref := os.Getenv(baselineEnv)
	if ref == "" {
		ref = defaultBaseline
	}

	if out, err := run(root, "git", "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return nil, fmt.Sprintf(
			"no baseline to compare against: %s does not resolve in this checkout (%s). "+
				"A shallow clone or a fork without tags will not have it; set %s to a ref that exists.",
			ref, strings.TrimSpace(out), baselineEnv), nil
	}

	// os.MkdirTemp rather than t.TempDir: the work is shared by every test in the
	// package, so its lifetime is the package's, not one test's.
	work, err := os.MkdirTemp("", "recall-diff-")
	if err != nil {
		return nil, "", err
	}

	tree := filepath.Join(work, "baseline-tree")
	if out, err := run(root, "git", "worktree", "add", "--detach", "--quiet", tree, ref); err != nil {
		return nil, "", fmt.Errorf("check out %s: %v\n%s", ref, err, out)
	}
	defer func() { _, _ = run(root, "git", "worktree", "remove", "--force", tree) }()

	p := &pair{
		ref:      ref,
		base:     filepath.Join(work, "recall-baseline"),
		head:     filepath.Join(work, "recall-head"),
		home:     filepath.Join(work, "home"),
		baseHome: filepath.Join(work, "archive-baseline"),
		headHome: filepath.Join(work, "archive-head"),
	}
	if out, err := run(tree, "go", "build", "-o", p.base, "./cmd/recall"); err != nil {
		return nil, "", fmt.Errorf("build %s: %v\n%s", ref, err, out)
	}
	if out, err := run(root, "go", "build", "-o", p.head, "./cmd/recall"); err != nil {
		return nil, "", fmt.Errorf("build this tree: %v\n%s", err, out)
	}

	// One corpus, read by both binaries. recall never writes to a session store,
	// so sharing it keeps every path in both outputs identical and leaves the
	// archive directory as the only difference to normalize.
	corpus, err := corpusgen.Generate(filepath.Join(p.home, ".claude"), corpusgen.Small())
	if err != nil {
		return nil, "", fmt.Errorf("generate the corpus: %v", err)
	}
	p.corpus = corpus
	if len(corpus.Plants) < 4 {
		return nil, "", fmt.Errorf("the generated corpus planted %d needles, want at least 4", len(corpus.Plants))
	}
	_, other, ok := corpus.CrossCheckout()
	if !ok {
		return nil, "", fmt.Errorf("the generated corpus carries no cross-checkout needle")
	}
	p.checkout = other

	// Warm both archives before any case runs. A first invocation prints that it
	// is building the archive, so leaving this to the battery would make the
	// first case's output differ from the same case run again.
	for _, w := range []struct {
		binary, archive string
	}{{p.base, p.baseHome}, {p.head, p.headHome}} {
		cmd := exec.Command(w.binary, "find", "warmup", "--all")
		cmd.Dir = corpus.Plants[0].Cwd
		cmd.Env = append(os.Environ(),
			"HOME="+p.home, "RECALL_HOME="+w.archive,
			"XDG_DATA_HOME=", "CLAUDE_PROJECTS_DIR=", "CLAUDE_CODE_SESSION_ID=", "NO_COLOR=1")
		// Exit 1 is a clean miss, which is the expected answer here.
		if out, err := cmd.CombinedOutput(); err != nil {
			if exit, isExit := err.(*exec.ExitError); !isExit || exit.ExitCode() != 1 {
				return nil, "", fmt.Errorf("warm %s: %v\n%s", filepath.Base(w.binary), err, out)
			}
		}
	}
	return p, "", nil
}

func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func repoRoot() (string, error) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate this test's own source")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(self), "..", ".."))
}
