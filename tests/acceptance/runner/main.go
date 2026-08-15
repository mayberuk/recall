// Command runner executes the recall acceptance cases and writes raw evidence for a judge agent.
// It never rules PASS or FAIL. Its only verdict-shaped output is BLOCKED, which records that a
// check could not be performed at all — a context that produced a result is the worst possible
// grader of it, so grading happens in a fresh one.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxBytesCap = 65536

type harness struct {
	repoRoot     string
	evidenceRoot string
	corpusRoot   string
	tmpRoot      string

	binary    string
	buildOK   bool
	buildLog  string
	verbs     map[string]bool
	verbProbe invResult
	probeNote string

	facts        *corpusFacts
	repos        *repoResolver
	sel          selection
	sentinel     string
	sentinelExit string
	scanned      []string

	warm     *sandbox
	warmErr  string
	fzfPath  string
	zshPath  string
	shellFn  string
	corpusIn corpusManifest

	cases []*caseResult
}

func main() {
	var (
		evidence = flag.String("evidence", "", "evidence directory (default <repo>/logs/acceptance)")
		corpus   = flag.String("corpus", "", "corpus root (default $HOME/.claude/projects)")
		only     = flag.String("only", "", "comma-separated case ids to run")
		skipPerf = flag.Bool("skip-perf", false, "skip the performance gates")
	)
	flag.Parse()

	root, err := repoRootDir()
	if err != nil {
		fatal("locate repo root: %v", err)
	}
	h := &harness{
		repoRoot:     root,
		evidenceRoot: firstNonEmpty(*evidence, filepath.Join(root, "logs", "acceptance")),
		corpusRoot:   firstNonEmpty(*corpus, filepath.Join(os.Getenv("HOME"), ".claude", "projects")),
		repos:        newRepoResolver(),
		verbs:        map[string]bool{},
	}

	tmp, err := os.MkdirTemp("", "recall-acceptance-")
	if err != nil {
		fatal("temp dir: %v", err)
	}
	h.tmpRoot = tmp
	defer os.RemoveAll(tmp)

	if err := os.MkdirAll(h.evidenceRoot, 0o755); err != nil {
		fatal("evidence dir: %v", err)
	}

	started := time.Now()
	h.preflight()
	h.selectAll()
	h.warmUp()

	filter := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			filter[id] = true
		}
	}
	for _, b := range h.caseBuilders(*skipPerf) {
		if len(filter) > 0 && !filter[b.id] {
			continue
		}
		c := &caseResult{HarnessStatus: "RAN"}
		b.fn(c)
		h.cases = append(h.cases, c)
		if err := c.write(h.evidenceRoot); err != nil {
			fatal("write evidence for %s: %v", c.ID, err)
		}
		status := c.HarnessStatus
		if status == "BLOCKED" {
			status = "BLOCKED — " + c.BlockReason
		}
		fmt.Printf("%-24s %s\n", c.ID, status)
	}

	h.finish(started)
}

// The id is repeated here so -only can skip a case without first executing it to learn its name.
type caseBuilder struct {
	id string
	fn func(*caseResult)
}

func (h *harness) caseBuilders(skipPerf bool) []caseBuilder {
	bs := []caseBuilder{
		{"a1-original-failure", h.a1},
		{"a2-machine-wide", h.a2},
		{"a3-bounded-output", h.a3},
		{"a4-coverage-line", h.a4},
		{"a5-zero-result-probe", h.a5},
		{"a6-needle-in-tool-output", h.a6},
		{"a7-dedup", h.a7},
		{"a8-repo-shapes", h.a8},
		{"a9-idempotent", h.a9},
		{"a10-doctor-clean", h.a10},
		{"a11-fzf-nonint", h.a11},
		{"a12-verb-help", h.a12},
		{"a13-exit-codes", h.a13},
		{"a14-relaxed-query", h.a14},
		{"a15-self-exclusion-declared", h.a15},
		{"a16-turns-verb", h.a16},
		{"a17-brief-is-cheaper", h.a17},
	}
	if !skipPerf {
		bs = append(bs,
			caseBuilder{"p1-cold-strip", h.p1},
			caseBuilder{"p2-incremental", h.p2},
			caseBuilder{"p3-find-conversation", h.p3},
			caseBuilder{"p4-find-results", h.p4},
			caseBuilder{"p5-startup", h.p5},
		)
	}
	return bs
}

func (h *harness) preflight() {
	dir := filepath.Join(h.evidenceRoot, "_preflight")
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0o755)

	h.binary = filepath.Join(h.repoRoot, "logs", ".bin", "recall")
	_ = os.MkdirAll(filepath.Dir(h.binary), 0o755)
	build := exec.Command("go", "build", "-o", h.binary, "./cmd/recall")
	build.Dir = h.repoRoot
	build.Env = mergeEnv(map[string]string{"CGO_ENABLED": "0"})
	out, err := build.CombinedOutput()
	h.buildOK = err == nil
	h.buildLog = string(out)
	if err != nil {
		h.buildLog += "\ngo build error: " + err.Error() + "\n"
	}
	_ = os.WriteFile(filepath.Join(dir, "build.log"), []byte(h.buildLog), 0o644)

	if h.buildOK {
		h.verbProbe = runInvocation(invocation{Label: "verb-registry", Argv: []string{h.binary}, Dir: h.repoRoot, Env: map[string]string{"NO_COLOR": "1"}})
		_ = os.WriteFile(filepath.Join(dir, "verbs.stdout"), h.verbProbe.stdout, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "verbs.stderr"), h.verbProbe.stderr, 0o644)
		listing := string(h.verbProbe.stdout) + string(h.verbProbe.stderr)
		for _, v := range []string{"find", "show", "when", "doctor", "turns"} {
			if regexp.MustCompile(`(?m)\b` + v + `\b`).MatchString(listing) {
				h.verbs[v] = true
			}
		}
		if len(h.verbs) == 0 {
			h.probeNote = "`recall` with no verb listed none of find/show/when/doctor"
		}
	}

	h.fzfPath, _ = exec.LookPath("fzf")
	h.zshPath, _ = exec.LookPath("zsh")
	h.shellFn = filepath.Join(h.repoRoot, "shell", "recall.zsh")

	m, err := manifestCorpus(h.corpusRoot, true)
	if err != nil {
		h.probeNote = strings.TrimSpace(h.probeNote + " corpus manifest error: " + err.Error())
	}
	h.corpusIn = m

	h.sentinel = newSentinel()
	h.sentinelExit = newSentinel()
	h.scanned = scanQueries(h.sentinel, h.sentinelExit)
	facts, err := scanCorpus(h.corpusRoot, h.scanned)
	if err != nil {
		fatal("scan corpus (read-only): %v", err)
	}
	h.facts = facts
	_ = writeJSON(filepath.Join(dir, "corpus-facts.json"), map[string]any{
		"root":     h.corpusRoot,
		"files":    facts.FileCount,
		"lines":    facts.LineCount,
		"sessions": len(facts.Sessions),
		"cwds":     len(facts.CwdRecords),
		"cross_file_duplication": map[string]any{
			"distinct_session_uuid_pairs":  facts.RecordPairs,
			"pairs_written_to_more_than_1": facts.DuplicatedPairs,
			"redundant_copies":             facts.RedundantCopies,
			"note": "Measured by the harness walking the corpus itself. Keyed on (session, uuid), " +
				"not uuid alone: a fork carries a record into a new session keeping its uuid, so a " +
				"global uuid key would delete the turn from the other session entirely.",
		},
		"queries": facts.Queries,
	})
}

func (h *harness) warmUp() {
	if !h.buildOK || !h.verbs["find"] {
		h.warmErr = "no runnable binary with a find verb"
		return
	}
	sb, err := newSandbox(h.tmpRoot, "warm", h.corpusRoot)
	if err != nil {
		h.warmErr = err.Error()
		return
	}
	h.warm = sb
	var probe invResult
	for i := 0; i < 2; i++ {
		probe = runInvocation(invocation{
			Label: "warm", Argv: []string{h.binary, "find", h.sel.A1Query, "--all"},
			Dir: h.repoRoot, Env: sb.env(),
		})
	}
	h.checkCorpusVisible(probe)
}

// checkCorpusVisible separates "the harness could not point the binary at the corpus" from "the
// binary searched nothing". The first is BLOCKED for every case; the second is a defect the judge
// must be allowed to see and rule on.
func (h *harness) checkCorpusVisible(probe invResult) {
	dir := filepath.Join(h.evidenceRoot, "_preflight")
	_ = os.WriteFile(filepath.Join(dir, "corpus-visible.stdout"), probe.stdout, 0o644)

	searched, ok := sessionsSearched(probe.stdoutString())
	if !ok || searched > 0 {
		return
	}
	alt, err := newSandbox(h.tmpRoot, "corpus-discovery-probe", h.corpusRoot)
	if err != nil {
		return
	}
	env := alt.env()
	delete(env, "CLAUDE_PROJECTS_DIR")
	second := runInvocation(invocation{
		Label: "discovery", Argv: []string{h.binary, "find", h.sel.A1Query, "--all"},
		Dir: h.repoRoot, Env: env,
	})
	_ = os.WriteFile(filepath.Join(dir, "corpus-visible-selfdiscovery.stdout"), second.stdout, 0o644)
	if n, ok := sessionsSearched(second.stdoutString()); ok && n > 0 {
		h.warmErr = fmt.Sprintf(
			"the harness could not point the binary at the corpus: with CLAUDE_PROJECTS_DIR=%s it searched 0 sessions, without it %d — the isolation the cases depend on is not being honoured",
			h.corpusRoot, n)
		h.warm = nil
	}
}

var searchedRE = regexp.MustCompile(`(\d+)\s+searched`)

func sessionsSearched(out string) (int, bool) {
	m := searchedRE.FindStringSubmatch(out)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// gate blocks a case for the reasons the contract calls BLOCKED: nothing built, nothing
// registered, no corpus. It deliberately does not block for a verb that runs and behaves wrongly,
// which is a FAIL for the judge to rule.
func (h *harness) gate(c *caseResult, verbs ...string) bool {
	if !h.buildOK {
		c.block("the binary does not build; see _preflight/build.log")
		return false
	}
	if h.facts.FileCount == 0 {
		c.block("corpus at %s contains no .jsonl files", h.corpusRoot)
		return false
	}
	for _, v := range verbs {
		if !h.verbs[v] {
			c.block("verb %q is not registered in this build — `recall` with no verb listed: %s",
				v, oneLine(string(h.verbProbe.stdout)+string(h.verbProbe.stderr)))
			return false
		}
	}
	if h.warm == nil && h.warmErr != "" {
		c.block("no warm sandbox: %s", h.warmErr)
		return false
	}
	return true
}

func (h *harness) env() map[string]string {
	if h.warm == nil {
		return map[string]string{}
	}
	return h.warm.env()
}

func (h *harness) finish(started time.Time) {
	after, _ := manifestCorpus(h.corpusRoot, false)
	delta := diffCorpus(h.corpusIn, after)
	integrity := map[string]any{
		"root":          h.corpusRoot,
		"files_before":  h.corpusIn.Files,
		"files_after":   after.Files,
		"digest_before": h.corpusIn.Digest,
		"digest_after":  after.Digest,
		"delta":         delta,
		"dangerous":     delta.dangerous(),
		"counts": map[string]int{
			"grew_append_only":                len(delta.Grew),
			"touched_but_content_identical":   len(delta.TouchedOnly),
			"content_changed_without_growing": len(delta.ContentChanged),
			"shrank":                          len(delta.Shrank),
			"appeared":                        len(delta.Appeared),
			"removed":                         len(delta.Removed),
		},
		"note": "Every corpus file is hashed before the run and stat-ed after; anything whose size " +
			"or mtime moved is re-hashed. Growth and new files are expected — Claude Code appends " +
			"to the transcript of the very session running this harness, and other sessions may be " +
			"live. `touched_but_content_identical` is a concurrent process restamping an mtime and " +
			"is benign. `dangerous: true` means a file shrank, disappeared, or changed content " +
			"without growing: the corpus must never receive a write, and if it did, every verdict " +
			"in this run is suspect.",
	}
	_ = writeJSON(filepath.Join(h.evidenceRoot, "corpus-integrity.json"), integrity)

	type entry struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		HarnessStatus string `json:"harness_status"`
		BlockReason   string `json:"block_reason,omitempty"`
	}
	var entries []entry
	for _, c := range h.cases {
		entries = append(entries, entry{c.ID, c.Title, c.HarnessStatus, c.BlockReason})
	}
	_ = writeJSON(filepath.Join(h.evidenceRoot, "manifest.json"), map[string]any{
		"generated":               time.Now().Format(time.RFC3339),
		"duration_s":              int(time.Since(started).Seconds()),
		"repo_root":               h.repoRoot,
		"git_head":                gitHead(h.repoRoot),
		"binary":                  h.binary,
		"binary_builds":           h.buildOK,
		"verbs_available":         sortedKeys(h.verbs),
		"corpus_root":             h.corpusRoot,
		"corpus_files":            h.facts.FileCount,
		"corpus_sessions":         len(h.facts.Sessions),
		"corpus_redundant_copies": h.facts.RedundantCopies,
		"filter":                  sortedKeys(filterOf(h.cases)),
		"filter_note":             filterNote(len(h.cases), h.caseBuilders(false)),
		"fzf":                     h.fzfPath,
		"zsh":                     h.zshPath,
		"shell_function":          h.shellFn,
		"cases":                   entries,
	})
	_ = os.WriteFile(filepath.Join(h.evidenceRoot, "README-judge.md"), []byte(h.judgeReadme(entries != nil)), 0o644)

	blocked, ran := 0, 0
	for _, c := range h.cases {
		if c.blocked() {
			blocked++
		} else {
			ran++
		}
	}
	fmt.Printf("\n%d cases ran, %d BLOCKED. Evidence: %s\n", ran, blocked, h.evidenceRoot)
	if delta.dangerous() {
		fmt.Println("CORPUS WAS WRITTEN TO DURING THE RUN — see corpus-integrity.json")
	}
}

func (h *harness) judgeReadme(bool) string {
	var b strings.Builder
	b.WriteString(`# Acceptance evidence — how to judge it

You are reading this cold, and that is the design. The process that produced these files does not
grade them, because a context that produced a result is the worst possible grader of it.

## Your job

Rule exactly one of **PASS**, **FAIL**, or **BLOCKED** for every case directory. "Mostly passed"
is a malformed verdict. Do not run the binary. Do not read the product source. Everything you
need is in the case directory.

## The three verdicts

- **PASS** — the evidence satisfies every pass rule in that case's ` + "`expected.md`" + `.
- **FAIL** — the check ran and the evidence violates a rule. A performance gate breach is a FAIL,
  not a warning.
- **BLOCKED** — a file named ` + "`BLOCKED`" + ` exists in the case directory. The harness could
  not perform the check: nothing built, the verb is not registered, the corpus premise the case
  rests on no longer holds. BLOCKED is never reported as FAIL and never as PASS. A wave whose
  acceptance is BLOCKED is unconverged, exactly like a failing one.

The ` + "`BLOCKED`" + ` sentinel file is authoritative and settles the verdict on its own. Its
first line is the word BLOCKED and its body is the reason.

## Where the line between BLOCKED and FAIL sits

BLOCKED is about the harness's inability, not the tool's incompleteness, with one deliberate
exception. A binary that does not build, a verb absent from the registry, an unreadable corpus,
a missing ` + "`fzf`" + `, or a corpus premise that stopped holding all BLOCK — there is nothing
to grade. A verb that runs and answers wrongly, rejects a flag the contract pins, or breaches a
timing gate is a FAIL — there is something to grade and it is wrong.

## Reading a case directory

| file | holds |
|---|---|
| ` + "`expected.md`" + ` | the contract text, the re-verified premises, and the pass/fail rules. Start here. |
| ` + "`case.json`" + ` | the same in machine-readable form, plus every invocation's argv, cwd, exit code and wall time |
| ` + "`premise.json`" + ` | each corpus premise with expected vs measured |
| ` + "`NN-<label>.cmd`" + ` | cwd, environment overrides and argv for invocation NN |
| ` + "`NN-<label>.stdout`" + ` | verbatim stdout |
| ` + "`NN-<label>.stderr`" + ` | verbatim stderr |
| ` + "`NN-<label>.exit`" + ` | exit code, plus ` + "`start-error:`" + ` or ` + "`timed-out:`" + ` when either happened |
| ` + "`timing.json`" + ` | performance cases only: the three runs, the median, and the contract's gate |
| ` + "`BLOCKED`" + ` | present only when blocked; its existence settles the verdict |

## Run-wide files

- ` + "`manifest.json`" + ` — what was built, which verbs the registry listed, corpus size, per-case harness status
- ` + "`corpus-integrity.json`" + ` — the corpus fingerprint before and after. ` + "`changed: true`" + ` means something wrote to a read-only corpus and every verdict here is suspect.
- ` + "`_preflight/`" + ` — the build log, the verb-registry probe, and the measured corpus facts every expectation was derived from

## The one case that is not like the others

` + "`a1-original-failure`" + ` is the mandatory gate. The build is not done if a1 is anything but
PASS, however green everything else is. It is also the case most likely to be BLOCKED honestly:
its query depends on a corpus that deletes itself on a 90-day cleanup and grows daily, so the
harness re-measures the premise every run and blocks rather than inventing a green.
`)
	return b.String()
}

// filterNote keeps a partial re-run from reading as a complete one. Case directories left over
// from an earlier run are still on disk and still look authoritative.
func filterNote(ran int, all []caseBuilder) string {
	if ran == len(all) {
		return "every case was run; the directories here are all from this run"
	}
	return fmt.Sprintf("PARTIAL RUN — only %d of %d cases were re-run. Every other case directory "+
		"under this path is left over from an earlier run and its evidence is older than this "+
		"manifest. Check each case.json you rely on for its own timestamp before judging it.", ran, len(all))
}

func filterOf(cases []*caseResult) map[string]bool {
	m := map[string]bool{}
	for _, c := range cases {
		m[c.ID] = true
	}
	return m
}

func repoRootDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	d := wd
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod at or above %s", wd)
		}
		d = parent
	}
}

func gitHead(dir string) string {
	out, err := gitOut(dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "(no output)"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "acceptance runner: "+format+"\n", args...)
	os.Exit(2)
}

func medianMS(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}
