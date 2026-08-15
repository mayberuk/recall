package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// Cases covering agentic-ergonomics behaviour: help text, exit codes, relaxed multi-term
// matching, self-exclusion, the `turns` verb, and `--brief`. Authored against the pinned
// `--help`/`guide` surface, never against cmd/recall/cmd_*.go or internal/render.

func (h *harness) a12(c *caseResult) {
	c.ID = "a12-verb-help"
	c.Title = "--help and a bad flag both enumerate the flag surface"
	c.Asserts = "recall find --help exits 0 and lists every flag find accepts; a wrong flag exits 2 and its stderr also lists the valid flags"

	if !h.gate(c, "find") {
		return
	}
	required := []string{"all-terms", "author", "brief", "include-self", "not", "results", "since"}
	c.fact("flags this build's newer behaviour depends on", "%s — named directly in docs/plan-agent-ergonomics.md P1/P3/P4, so their absence from the help surface would mean documented behaviour an agent cannot discover", strings.Join(required, ", "))
	c.fact("old behaviour this case replaces", "docs/handoff-agent-ergonomics.md B1: `recall find --help` used to exit with `flag: help requested` (no flag list), and a bad flag printed `flag provided but not defined` with no list of valid flags — a dead end with no next move")

	help := c.run(h.invoke("find-help", h.repoRoot, "find", "--help"))
	badFlag := c.run(h.invoke("find-bad-flag", h.repoRoot, "find", "x", "--bogusflag"))

	helpFlags := flagsFromListing(string(help.stdout))
	errFlags := flagsFromTakesLine(string(badFlag.stderr) + string(badFlag.stdout))
	c.attach("flags.json", map[string]any{
		"from_help":  helpFlags,
		"from_error": errFlags,
		"note":       "extracted by the harness with regexes over the CLI's own text output, not read from source",
	})
	c.fact("flags found in --help", "%d: %s", len(helpFlags), strings.Join(helpFlags, ", "))
	c.fact("flags found in the bad-flag error", "%d: %s", len(errFlags), strings.Join(errFlags, ", "))

	c.PassRules = []string{
		"`01-find-help.exit` is 0",
		"`01-find-help.stdout` enumerates individual flags (a `-name` per line with a description), not just a usage line — and the set includes at least " + strings.Join(quoteAll(required), ", "),
		"`02-find-bad-flag.exit` is 2",
		"`02-find-bad-flag.stderr` names the specific rejected flag (`bogusflag`) and separately lists the valid flag surface — a caller gets a next move, not a dead end",
		"the flag set named in `02-find-bad-flag.stderr` is the same surface enumerated in `01-find-help.stdout` (see `flags.json`) — help and the error path agree on one flag surface rather than two",
	}
	c.FailRules = []string{
		"`01-find-help` exits non-zero, or its stdout is only an error like `flag: help requested` with no per-flag listing — the retired behaviour B1 describes",
		"`02-find-bad-flag` exits anything other than 2",
		"`02-find-bad-flag.stderr` names the bad flag but lists no valid flags, or lists flags nowhere near what `--help` shows",
		"a flag `flags.json`'s `from_help` names is absent from `from_error`, or vice versa",
	}
	c.Notes = append(c.Notes,
		"The required-flags list is drawn from docs/plan-agent-ergonomics.md, not from cmd/recall source: --all-terms carries P1's relaxed-matching escape hatch, --author/--since/--not are P3's filters, --brief/--include-self are P3/P4's context-cost and self-exclusion controls — exactly the flags a13-a17 exercise.")
}

func (h *harness) a13(c *caseResult) {
	c.ID = "a13-exit-codes"
	c.Title = "the four exit codes are reachable and mean what the guide says"
	c.Asserts = "a query certain to match nothing exits 1 with an empty stdout report that still carries the coverage line; a bad flag exits 2; a normal hit exits 0"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	qf := h.facts.Queries[h.sentinelExit]
	absent := 0
	if qf != nil {
		absent = qf.ConvTotal + qf.ResultTotal
	}
	if !c.requirePremise("the miss-path token matches nothing anywhere in the corpus",
		absent == 0,
		"0 hits in any tier, corpus-wide",
		fmt.Sprintf("%d hit(s)", absent)) {
		return
	}
	if !c.requirePremise("the hit-path query has at least one hit somewhere",
		s.A1InRepo > 0 || s.A1Local > 0,
		fmt.Sprintf("a nonzero hit count for %q", s.A1Query),
		fmt.Sprintf("in-repo %d, elsewhere %d", s.A1Local, s.A1InRepo)) {
		return
	}
	c.fact("miss-path token", "128 bits of randomness generated for this run, verified absent from the whole corpus before use — a hardcoded token would end up mentioned in this harness's own transcripts, the same hazard p5-startup's sentinel exists to avoid")
	c.fact("hit-path query", "%q, the same query a1 uses", s.A1Query)

	c.run(h.invoke("miss", h.repoRoot, "find", h.sentinelExit, "--all"))
	c.run(h.invoke("bad-flag", h.repoRoot, "find", "x", "--not-a-real-flag"))
	c.run(h.invoke("hit", h.repoRoot, "find", s.A1Query, "--all"))

	c.PassRules = []string{
		"`01-miss.exit` is 1",
		"`01-miss.stdout` reports no session or hit data (no session id, no snippet) — the query is verified to match nothing — yet still carries the coverage line",
		"`02-bad-flag.exit` is 2",
		"`03-hit.exit` is 0 and its stdout names at least one session and carries the coverage line",
	}
	c.FailRules = []string{
		"`01-miss` exits anything other than 1 (0 would mean a miss is reported as a hit; 3 or 4 would mean an unrelated error path fired)",
		"`01-miss.stdout` carries session or snippet data despite the token being verified absent from the whole corpus",
		"`01-miss.stdout` has no coverage line — a miss is exactly the case where a caller most needs to know what was searched",
		"`02-bad-flag` exits anything other than 2",
		"`03-hit` exits non-zero, or its stdout shows no hit for a query independently verified to have one",
	}
	c.Notes = append(c.Notes,
		"docs/handoff-agent-ergonomics.md B4 pins this table: 0 hits, 1 no hits, 2 usage error, 3 archive problem, 4 refused over cap. This case exercises three of the four; a3-bounded-output already exercises 4.")
}

// a14Query is one term the corpus is measured to carry plus six tokens that cannot be in any
// corpus written before this run started. Naming a fixed set of real words in prose is what
// broke the first version of this case: the words ended up in a transcript together, a turn
// carrying all seven appeared, and the premise the case rests on stopped holding. Randomness
// per run makes the premise true by construction rather than by luck.
func (h *harness) a14Query() (present string, terms []string) {
	best := ""
	for term, qf := range h.facts.Queries {
		if qf == nil || qf.ConvTotal == 0 {
			continue
		}
		if best == "" || qf.ConvTotal > h.facts.Queries[best].ConvTotal ||
			(qf.ConvTotal == h.facts.Queries[best].ConvTotal && term < best) {
			best = term
		}
	}
	if best == "" {
		return "", nil
	}
	terms = []string{best}
	for i := 0; i < 6; i++ {
		terms = append(terms, newSentinel())
	}
	return best, terms
}

func (h *harness) a14(c *caseResult) {
	c.ID = "a14-relaxed-query"
	c.Title = "a query no turn carries in full still returns the best partial match"
	c.Asserts = "a query of 7 terms where no single turn carries them all still returns hits, with a coverage line naming how many terms the returned turns do carry; --all-terms on the same query returns nothing"

	if !h.gate(c, "find") {
		return
	}
	present, terms := h.a14Query()
	if !c.requirePremise("at least one query term is present in the conversation tier",
		present != "",
		"a nonzero conversation-tier hit count for one of the harness's measured queries",
		fmt.Sprintf("measured counts: %v", h.facts.Queries)) {
		return
	}

	maxCarried, err := termCoverage(h.corpusRoot, terms)
	if err != nil {
		c.block("could not independently measure term co-occurrence: %v", err)
		return
	}
	c.fact("terms", "%s — the first is present in the conversation tier (%d hits), the other six are 128-bit random tokens generated when this run started",
		strings.Join(terms, ", "), h.facts.Queries[present].ConvTotal)
	c.fact("independently measured max co-occurrence", "%d of %d terms found together in any single conversation-tier record, by the harness's own scan over the corpus — not asked of the tool under test", maxCarried, len(terms))

	if !c.requirePremise("no single turn carries every term",
		maxCarried < len(terms),
		fmt.Sprintf("fewer than %d of the %d terms co-occurring in any one record", len(terms), len(terms)),
		strconv.Itoa(maxCarried)) {
		return
	}

	relaxedArgs := append(append([]string{"find"}, terms...), "--all")
	c.run(h.invoke("relaxed", h.repoRoot, relaxedArgs...))
	strictArgs := append(append([]string{"find"}, terms...), "--all", "--all-terms")
	c.run(h.invoke("strict-all-terms", h.repoRoot, strictArgs...))

	c.PassRules = []string{
		"`01-relaxed.exit` is 0 and its stdout names at least one session",
		"`01-relaxed.stdout` carries a line beginning `── no turn carried all`, naming " + strconv.Itoa(len(terms)) + " as the total term count and stating how many of those terms the shown turns do carry",
		"`02-strict-all-terms.exit` is 1 and its stdout reports no hits",
	}
	c.FailRules = []string{
		"`01-relaxed` returns no hits for a query independently measured to have every term present somewhere in the corpus — the false negative relaxed matching exists to prevent",
		"`01-relaxed.stdout` has no line declaring the relaxation — a silent narrowing to a partial match with nothing said about it",
		"`02-strict-all-terms` returns any hit, or exits anything other than 1 — the harness independently measured that no single turn carries all " + strconv.Itoa(len(terms)) + " terms, so a strict AND has nothing left to return",
	}
	c.Notes = append(c.Notes,
		"docs/handoff-agent-ergonomics.md B6 / docs/plan-agent-ergonomics.md P1: a query no turn carries in full returns the best partial match and says so, rather than the old cliff-edge empty result; --all-terms keeps the strict behaviour for a caller who wants it.",
		"Six of the seven terms are generated per run rather than named, because a term set written down in prose reaches a transcript and stops satisfying the premise. An earlier fixed set of seven real words did exactly that and BLOCKED this case.")
}

func (h *harness) a15(c *caseResult) {
	c.ID = "a15-self-exclusion-declared"
	c.Title = "the calling session is excluded by default and the exclusion is declared"
	c.Asserts = "with CLAUDE_CODE_SESSION_ID set to a session that exists in the corpus and matches the query, that session is absent from the results and the footer says so; --include-self puts it back"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	qf := h.facts.Queries[s.A1Query]
	sessions := sortedKeys(qf.ConvBySession)
	if !c.requirePremise("at least two sessions carry the query, so excluding one still leaves a hit to report",
		len(sessions) >= 2,
		"2 or more sessions with a conversation-tier hit for "+strconv.Quote(s.A1Query),
		fmt.Sprintf("%d session(s): %s", len(sessions), strings.Join(sessions, ", "))) {
		return
	}
	target := sessions[0]
	c.fact("query", "%q", s.A1Query)
	c.fact("session under test", "%s — picked because the harness's own corpus scan independently confirms it carries a conversation-tier hit for this query", target)
	c.fact("other sessions the query also hits", "%d: %s", len(sessions)-1, strings.Join(sessions[1:], ", "))

	env := withEnv(h.env(), map[string]string{"CLAUDE_CODE_SESSION_ID": target})
	c.run(invocation{Label: "excluded", Argv: []string{h.binary, "find", s.A1Query, "--all"}, Dir: h.repoRoot, Env: env})
	c.run(invocation{Label: "included", Argv: []string{h.binary, "find", s.A1Query, "--all", "--include-self"}, Dir: h.repoRoot, Env: env})

	c.PassRules = []string{
		"`01-excluded.stdout` does not name session `" + target + "`",
		"`01-excluded.stdout` states that turns belonging to the calling session were skipped — the coverage contract's declaration requirement for any narrowing",
		"`02-included.stdout` names session `" + target + "`",
	}
	c.FailRules = []string{
		"`01-excluded.stdout` names session `" + target + "` — it was not excluded despite `CLAUDE_CODE_SESSION_ID` naming it",
		"`01-excluded.stdout` excludes the session but says nothing about it anywhere in the output — a silent narrowing, which the coverage contract forbids",
		"`02-included.stdout` still omits session `" + target + "` — `--include-self` did not restore it",
	}
	c.Notes = append(c.Notes,
		"docs/handoff-agent-ergonomics.md A3: the caller's own session ranking first was the original defect (asking a question and getting yourself back). The fix excludes it by default and `--include-self` opts back in; both halves are checked here, not just the exclusion.")
}

func (h *harness) a16(c *caseResult) {
	c.ID = "a16-turns-verb"
	c.Title = "turns returns citable passages, and the citation resolves"
	c.Asserts = "recall turns <query> returns passages stamped <session>:<uuid>, and the uuid it prints can be fed back to recall show <session> --turn <uuid> and resolves — the one-call path replacing find -> choose -> show"

	s := &h.sel
	if !h.gate(c, "turns", "show") {
		return
	}
	c.fact("query", "%q", s.A1Query)

	probe := c.run(h.invoke("turns", h.repoRoot, "turns", s.A1Query, "--all"))
	if probe.ExitCode != 0 {
		c.block("`turns` verb returned exit %d for %q, a query independently measured to have hits — stderr: %s",
			probe.ExitCode, s.A1Query, oneLine(string(probe.stderr)))
		return
	}
	session, turn, ok := firstCite(probe.stdout)
	if !ok {
		c.block("no `<session>:<uuid>` citation could be found in 01-turns.stdout by pattern-matching two UUIDs joined by a colon — either the format differs from the contract or the run produced no passages")
		return
	}
	c.fact("passage picked to verify", "session %s, turn %s — the first `session:uuid` citation found in 01-turns.stdout", session, turn)

	c.run(h.invoke("turns-json", h.repoRoot, "turns", s.A1Query, "--all", "--json"))
	c.run(h.invoke("show-turn", h.repoRoot, "show", session, "--turn", turn))

	c.PassRules = []string{
		"`01-turns.stdout` names at least one session and stamps at least one passage `<session>:<uuid>`",
		"the pair picked above (session " + session + ", turn " + turn + ") is a genuine record uuid, not a session id or a coincidental hex string — `02-turns-json.stdout` is available to cross-check the same pair machine-readably",
		"`03-show-turn.exit` is 0 and its output centres on turn " + turn + " within session " + session + " — the uuid `turns` printed is the one `show --turn` accepts and resolves, not decorative",
	}
	c.FailRules = []string{
		"`01-turns` names no session or stamps no `session:uuid` passage for a query independently known to have hits",
		"`03-show-turn` exits non-zero, reports the turn as not found, or shows a window that is not anchored on that uuid",
	}
	c.Notes = append(c.Notes,
		"docs/plan-agent-ergonomics.md P6 / docs/handoff-agent-ergonomics.md B9: turns collapses find -> choose -> show into one call and gives free citations. 03 is what proves the citation is load-bearing rather than cosmetic — the uuid has to actually resolve.")
}

func (h *harness) a17(c *caseResult) {
	c.ID = "a17-brief-is-cheaper"
	c.Title = "--brief cuts context cost substantially"
	c.Asserts = "--brief output is at least 3x smaller than the same query without it, and both still carry the coverage line"

	if !h.gate(c, "find") {
		return
	}
	query := "flightplan"
	qf := h.facts.Queries[query]
	n := 0
	if qf != nil {
		n = qf.ConvTotal
	}
	if !c.requirePremise("the comparison query has enough hits for the size difference to be meaningful",
		n > 0,
		"a nonzero conversation-tier hit count for "+strconv.Quote(query),
		strconv.Itoa(n)) {
		return
	}
	c.fact("query", "%q — chosen for its corpus-wide hit volume (%d conversation-tier hits measured independently) so both runs carry enough sessions and snippets for the size comparison to be meaningful", query, n)
	c.fact("--limit and --hits", "raised to 50 and 5 for both runs, so the full run is not artificially small before --brief even applies")

	full := c.run(h.invoke("full", h.repoRoot, "find", query, "--all", "--limit", "50", "--hits", "5"))
	brief := c.run(h.invoke("brief", h.repoRoot, "find", query, "--all", "--limit", "50", "--hits", "5", "--brief"))

	ratio := "n/a (brief produced 0 bytes)"
	if brief.StdoutBytes > 0 {
		ratio = fmt.Sprintf("%.2fx", float64(full.StdoutBytes)/float64(brief.StdoutBytes))
	}
	c.fact("measured sizes", "full %d bytes, brief %d bytes, ratio %s", full.StdoutBytes, brief.StdoutBytes, ratio)

	c.PassRules = []string{
		"`02-brief.stdout_bytes` (read from `02-brief.cmd`) is at most a third of `01-full.stdout_bytes` (read from `01-full.cmd`)",
		"both `01-full.stdout` and `02-brief.stdout` carry the coverage line",
		"both invocations exit 0",
	}
	c.FailRules = []string{
		"brief is less than 3x smaller than full",
		"either invocation is missing the coverage line",
		"either invocation exits non-zero",
	}
	c.Notes = append(c.Notes,
		"docs/handoff-agent-ergonomics.md B5: --brief is meant for triage at roughly a tenth of the bytes. The 3x bar here is deliberately looser than that tenth, so this case fails only on a much smaller regression, not on ordinary corpus-driven variance in how many snippets a session happens to produce.")
}

// termCoverage measures, over the raw corpus, the largest number of the given terms that appear
// together in any single conversation-tier record. It is how a14 verifies its premise — that no
// turn carries every term — independently of the tool under test, reusing splitTiers so "turn"
// means the same thing here as it does to corpusFacts.
func termCoverage(root string, terms []string) (int, error) {
	lowered := make([]string, len(terms))
	for i, t := range terms {
		lowered[i] = strings.ToLower(t)
	}
	max := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, ferr := os.Open(path)
		if ferr != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
		for sc.Scan() {
			rec := gjson.ParseBytes(sc.Bytes())
			rt := rec.Get("type").String()
			if rt != "user" && rt != "assistant" {
				continue
			}
			conv, _ := splitTiers(rec.Get("message.content"))
			if conv == "" {
				continue
			}
			low := strings.ToLower(conv)
			carried := 0
			for _, t := range lowered {
				if strings.Contains(low, t) {
					carried++
				}
			}
			if carried > max {
				max = carried
			}
		}
		return sc.Err()
	})
	return max, err
}

var citeRE = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}):([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// firstCite reads the <session>:<uuid> citation format directly, rather than parsing --json field
// names that are not pinned by the contract.
func firstCite(b []byte) (session, turn string, ok bool) {
	m := citeRE.FindSubmatch(b)
	if m == nil {
		return "", "", false
	}
	return string(m[1]), string(m[2]), true
}

var helpFlagRE = regexp.MustCompile(`(?m)^  -([A-Za-z][\w-]*)\b`)
var takesLineRE = regexp.MustCompile(`\btakes: (.+)`)

// flagsFromListing reads a --help body's own "  -name" lines rather than any fixed flag list, so
// the check compares the tool's help output against its own error output, not against a list this
// harness invented.
func flagsFromListing(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range helpFlagRE.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

func flagsFromTakesLine(s string) []string {
	m := takesLineRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	var out []string
	for _, f := range strings.Fields(m[1]) {
		out = append(out, strings.TrimPrefix(f, "--"))
	}
	sort.Strings(out)
	return out
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "`--" + s + "`"
	}
	return out
}

func withEnv(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
