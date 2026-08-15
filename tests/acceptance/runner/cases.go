package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The coverage line is a contract, not decoration: a command that searches without emitting it is
// a defect. These are the two lines that contract requires, reproduced verbatim.
const (
	coverageTierDeclaration = "conversation only — tool output NOT searched (--results)"
	coverageBoundaryLive    = "live to"
	coverageBoundaryArchive = "archived before that"
	coverageRulePrefix      = "── "
)

func (h *harness) invoke(label string, dir string, args ...string) invocation {
	return invocation{Label: label, Argv: append([]string{h.binary}, args...), Dir: dir, Env: h.env()}
}

func (h *harness) a1(c *caseResult) {
	c.ID = "a1-original-failure"
	c.Title = "the failure the tool exists to invert"
	c.Asserts = "invoked with cwd set to one checkout of a repo, a query whose only hit lives in a different checkout of the same repo returns that session. This is the reason the tool exists."
	c.Notes = append(c.Notes,
		"This is the mandatory gate. The build is not done if a1 is anything but PASS, however green everything else is.",
		"The binary's working directory is genuinely set to that first checkout — see the cwd line in the .cmd file. A run that passed --repo instead would be testing something else.")

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	if st, err := os.Stat(s.A1Cwd); err != nil || !st.IsDir() {
		c.block("working directory %s does not exist", s.A1Cwd)
		return
	}
	c.fact("query", "%q, chosen from the ranked candidates in logs/acceptance-queries.md", s.A1Query)
	c.fact("cwd repo identity", "%s (%s)", repoKey(s.A1Repo), s.A1Repo.Value)
	c.fact("sessions that must be returned", "%s", strings.Join(s.A1Sessions, ", "))

	okLocal := c.requirePremise("query absent from the searched-from checkout",
		s.A1Local == 0,
		fmt.Sprintf("0 conversation-tier hits for %q under %s", s.A1Query, s.A1Cwd),
		fmt.Sprintf("%d", s.A1Local))
	okElsewhere := c.requirePremise("query present elsewhere in the same repo",
		s.A1InRepo > 0 && len(s.A1Sessions) > 0,
		fmt.Sprintf("at least one conversation-tier hit for %q in repo %s outside that checkout", s.A1Query, repoKey(s.A1Repo)),
		fmt.Sprintf("%d hits across %d session(s)", s.A1InRepo, len(s.A1Sessions)))
	if !okLocal || !okElsewhere {
		return
	}

	c.run(h.invoke("find-from-mobile-2", s.A1Cwd, "find", s.A1Query))

	c.PassRules = []string{
		"`01-find-from-mobile-2.exit` is 0",
		"`01-find-from-mobile-2.cmd` shows cwd `" + s.A1Cwd + "` and argv with no `--repo` and no `--all`",
		"stdout names at least one of the sessions listed above under \"sessions that must be returned\"",
		"stdout carries the coverage line: a line beginning `" + coverageRulePrefix + "` containing `" + coverageTierDeclaration + "`, and a second beginning `" + coverageRulePrefix + "` containing `" + coverageBoundaryLive + "` and `" + coverageBoundaryArchive + "`",
	}
	c.FailRules = []string{
		"stdout reports no results, or names none of the expected sessions",
		"stdout names only sessions from the checkout the query was run from",
		"the coverage line is absent",
		"a non-zero exit code",
	}
}

func (h *harness) a2(c *caseResult) {
	c.ID = "a2-machine-wide"
	c.Title = "--all reaches past the current repo"
	c.Asserts = "the same query with --all returns hits from more than one repo"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	repos := sortedKeys(s.A2Repos)
	c.fact("query", "%q", s.A2Query)
	c.fact("repo identities holding conversation-tier hits", "%s", strings.Join(repos, ", "))
	c.fact("of those, the ones that count toward cross-repo reach", "%s", strings.Join(s.A2Qualifying, ", "))
	c.fact("excluded as self-contamination", "%s — the repo this tool is being built in. Its sessions discuss the test queries in prose, so it appears as a second repo for any query the harness has ever named, and counting it would let the tool pass this case on its own exhaust", s.A2SelfRepo)
	if s.A2Substituted {
		c.Notes = append(c.Notes, fmt.Sprintf(
			"SUBSTITUTION FIRED. The contract says \"the same query\", but %q reaches %s besides the repo being built in, so it cannot demonstrate machine-wide reach on its own. The harness substituted %q, measured across %d qualifying repos: %s. See logs/escalations/acceptance-1.md.",
			s.A1Query, plural(s.A2OrigQual, "repo", "repos"), s.A2Query, len(s.A2Qualifying), strings.Join(s.A2Qualifying, ", ")))
	}
	if !c.requirePremise("query spans more than one repo, not counting the one being built in",
		len(s.A2Qualifying) >= 2,
		"conversation-tier hits in at least 2 distinct repo identities other than "+s.A2SelfRepo,
		fmt.Sprintf("%d qualifying: %s (full spread: %s)", len(s.A2Qualifying), strings.Join(s.A2Qualifying, ", "), strings.Join(repos, ", "))) {
		return
	}

	c.run(h.invoke("find-all", s.A1Cwd, "find", s.A2Query, "--all"))
	c.run(h.invoke("find-repo-scoped", s.A1Cwd, "find", s.A2Query))

	c.PassRules = []string{
		"`01-find-all.exit` is 0",
		"`01-find-all.stdout` attributes results to at least two distinct repos drawn from the qualifying list above — hits in " + s.A2SelfRepo + " do not count toward this, since that is the repo the tool is being built in",
		"the set of repos in `01-find-all.stdout` is a superset of the set in `02-find-repo-scoped.stdout` — `--all` widened the scope rather than replacing it",
	}
	c.FailRules = []string{
		"`--all` names one repo or none",
		"`--all` returns the same repo set as the repo-scoped run when the measured spread says otherwise",
		"a non-zero exit code",
	}
	c.Notes = append(c.Notes,
		"The repo being built in is excluded from the count that decides this case, and that exclusion is what makes the substitution reachable. An earlier version counted it, so a query contaminated by this build's own sessions looked like genuine cross-repo reach and the substitution could never fire — a safety net that was not attached to anything.")
}

func (h *harness) a3(c *caseResult) {
	c.ID = "a3-bounded-output"
	c.Title = "no verb on any input exceeds the byte cap"
	c.Asserts = "no invocation of any verb on any input exceeds --max-bytes; the largest session (>2 MB of conversation) does not blow the cap"

	s := &h.sel
	if !h.gate(c, "find", "show", "when", "doctor") {
		return
	}
	if s.A3Session == "" {
		c.block("no session found in the corpus")
		return
	}
	capArg := strconv.Itoa(maxBytesCap)
	c.fact("byte cap under test", "--max-bytes %s", capArg)
	c.fact("largest session by conversation bytes", "%s — %d bytes (%.2f MB) across %d files",
		s.A3Session, s.A3Bytes, float64(s.A3Bytes)/1e6, len(h.facts.Sessions[s.A3Session].Files))
	c.premise("largest session still exceeds 2 MB of conversation",
		s.A3Bytes > 2<<20,
		"more than 2 MB, per the contract's parenthetical",
		fmt.Sprintf("%d bytes", s.A3Bytes))

	dir := firstNonEmpty(s.A3Cwd, h.repoRoot)
	c.run(h.invoke("find-capped", dir, "find", s.A1Query, "--all", "--max-bytes", capArg))
	c.run(h.invoke("find-all-tiers-capped", dir, "find", s.A1Query, "--all", "--results", "--tools", "--max-bytes", capArg))
	c.run(h.invoke("show-largest-capped", dir, "show", s.A3Session, "--max-bytes", capArg))
	c.run(h.invoke("show-largest-full-capped", dir, "show", s.A3Session, "--full", "--max-bytes", capArg))
	c.run(h.invoke("when-capped", dir, "when", s.A1Query, "--all", "--max-bytes", capArg))
	c.run(h.invoke("doctor-capped", dir, "doctor", "--max-bytes", capArg))
	c.run(h.invoke("show-largest-full-uncapped", dir, "show", s.A3Session, "--full"))

	c.PassRules = []string{
		"invocations 01 through 06 each have `stdout_bytes` at or below " + capArg + " — read the number from the `.cmd` file, no need to open the stdout",
		"invocation 04 (`show --full` on the largest session, capped) is at or below the cap whether it rendered or refused",
	}
	c.FailRules = []string{
		"any of invocations 01-06 emitted more than " + capArg + " bytes on stdout",
		"an over-cap invocation silently truncated its output instead of refusing — the contract says `--max-bytes` refuses rather than truncates, so a run that hits the cap must say so in stdout or stderr rather than emitting a clipped transcript",
		"a start-error or a timeout on any invocation",
	}
	c.Notes = append(c.Notes,
		"Invocation 07 has no `--max-bytes` and is recorded, not asserted: the default cap is not pinned by the contract. Its `stdout_bytes` is worth reading anyway — the requirements dealbreaker is any single lookup capable of loading a multi-megabyte transcript into the asking context.",
		"`doctor` does not take a query; `--max-bytes` on it is testing that the flag is honoured by every verb, not that doctor searches.")
}

func (h *harness) a4(c *caseResult) {
	c.ID = "a4-coverage-line"
	c.Title = "every searching verb declares what it did not search"
	c.Asserts = "every searching verb emits the coverage line naming the unsearched tier"

	s := &h.sel
	if !h.gate(c, "find", "when") {
		return
	}
	c.fact("required line 1", "%s{n} sessions · {m} searched · %s", coverageRulePrefix, coverageTierDeclaration)
	c.fact("required line 2", "%s%s {mtime-boundary} · %s", coverageRulePrefix, coverageBoundaryLive, coverageBoundaryArchive)

	c.run(h.invoke("find-repo-scoped", s.A1Cwd, "find", s.A1Query))
	c.run(h.invoke("find-all", s.A1Cwd, "find", s.A1Query, "--all"))
	c.run(h.invoke("find-results", s.A1Cwd, "find", s.A1Query, "--all", "--results"))
	c.run(h.invoke("when", s.A1Cwd, "when", s.A1Query, "--all"))
	c.run(h.invoke("find-json", s.A1Cwd, "find", s.A1Query, "--all", "--json"))
	c.run(h.invoke("doctor", s.A1Cwd, "doctor"))

	c.PassRules = []string{
		"invocations 01, 02 and 04 each emit both coverage lines — line 1 beginning `" + coverageRulePrefix + "` and containing `" + coverageTierDeclaration + "`, line 2 beginning `" + coverageRulePrefix + "` and containing both `" + coverageBoundaryLive + "` and `" + coverageBoundaryArchive + "`",
		"invocation 03 (`--results`) emits a coverage line too, and it does not claim tool output was unsearched — it was",
		"the session counts in line 1 are consistent between the repo-scoped and `--all` runs: `--all` searched at least as many sessions as the repo-scoped run",
	}
	c.FailRules = []string{
		"any of 01, 02, 03, 04 searched and emitted no coverage line",
		"a coverage line states tool output was not searched on the `--results` run, or states it was searched on a default run",
		"the counts in the coverage line contradict each other across runs (a repo-scoped run claiming to have searched more sessions than `--all`)",
	}
	c.Notes = append(c.Notes,
		"Invocation 05 (`--json`) and 06 (`doctor`) are recorded, not asserted. The contract pins the coverage line's text but not its JSON representation, and `doctor` does not search. If the JSON output carries no coverage information at all, say so in your report as an observation rather than a FAIL.")
}

func (h *harness) a5(c *caseResult) {
	c.ID = "a5-zero-result-probe"
	c.Title = "a repo-scoped miss reports what exists elsewhere"
	c.Asserts = "a repo-scoped query with no local hits reports hits existing elsewhere rather than a bare zero"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	if s.A5Cwd == "" {
		c.block("no repo on disk has sessions in the corpus and zero conversation-tier hits for %q", s.A5Query)
		return
	}
	elsewhere := 0
	for _, n := range h.facts.Queries[s.A5Query].ConvByCwd {
		elsewhere += n
	}
	c.fact("query", "%q", s.A5Query)
	c.fact("working directory", "%s — repo identity %s", s.A5Cwd, repoKey(s.A5Repo))

	if !c.requirePremise("zero hits in the searched repo",
		true, "0 conversation-tier hits in "+repoKey(s.A5Repo), "0") {
		return
	}
	if !c.requirePremise("hits exist elsewhere in the corpus",
		elsewhere > 0,
		"at least one conversation-tier hit outside that repo",
		fmt.Sprintf("%d hits corpus-wide", elsewhere)) {
		return
	}

	c.run(h.invoke("find-repo-scoped-miss", s.A5Cwd, "find", s.A5Query))

	c.PassRules = []string{
		"stdout does more than report zero: it states that the term exists outside the current repo — a count, a repo name, a session, or an explicit instruction to widen with `--all`",
		"the coverage line is present",
	}
	c.FailRules = []string{
		"stdout is a bare \"no results\" / \"0 hits\" with nothing about the rest of the machine",
		"stdout claims the term does not exist anywhere, which the premise above measures as false",
		"a crash or a start-error",
	}
	c.Notes = append(c.Notes,
		"A non-zero exit code on a miss is not by itself a failure — the contract does not pin the exit code for an empty result. Judge the content.")
}

func (h *harness) a6(c *caseResult) {
	c.ID = "a6-needle-in-tool-output"
	c.Title = "tool output is excluded by default, included on request, and said so"
	c.Asserts = "a token present only in tool output is NOT returned by default, IS returned with --results, and the default run said so"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	if st, err := os.Stat(s.A6Cwd); err != nil || !st.IsDir() {
		c.block("working directory %s does not exist", s.A6Cwd)
		return
	}
	c.fact("query", "%q", s.A6Query)
	c.fact("working directory", "%s — repo identity %s", s.A6Cwd, repoKey(s.A6Repo))
	c.fact("sessions --results must return", "%s", strings.Join(s.A6Sessions, ", "))

	okConv := c.requirePremise("token absent from conversation text in this repo",
		s.A6Conv == 0,
		fmt.Sprintf("0 conversation-tier hits for %q in repo %s", s.A6Query, repoKey(s.A6Repo)),
		strconv.Itoa(s.A6Conv))
	okRes := c.requirePremise("token present in tool-result text in this repo",
		s.A6Result > 0 && len(s.A6Sessions) > 0,
		fmt.Sprintf("at least one tool-result-tier hit for %q in repo %s", s.A6Query, repoKey(s.A6Repo)),
		fmt.Sprintf("%d hits across %d session(s)", s.A6Result, len(s.A6Sessions)))
	if !okConv || !okRes {
		return
	}

	c.run(h.invoke("find-default", s.A6Cwd, "find", s.A6Query))
	c.run(h.invoke("find-results", s.A6Cwd, "find", s.A6Query, "--results"))

	c.PassRules = []string{
		"all three halves hold, not two: (a) `01-find-default.stdout` names none of the sessions listed above; (b) `01-find-default.stdout` carries the coverage line containing `" + coverageTierDeclaration + "`; (c) `02-find-results.stdout` names at least one of those sessions",
	}
	c.FailRules = []string{
		"the default run returned one of those sessions — tool output was searched when the contract says it is not",
		"the default run silently returned nothing with no coverage line saying tool output was excluded — that is the silent false negative the dealbreaker names",
		"`--results` did not return any of the sessions the corpus measurably holds the token in",
	}
	c.Notes = append(c.Notes,
		"The premises are stated repo-scoped, not corpus-wide, and that is deliberate: the token has since appeared in conversation text in the repo where recall itself was built, so the corpus-wide claim in logs/acceptance-queries.md no longer holds. Inside repo "+repoKey(s.A6Repo)+", which is what this case searches, it does.",
		"The default run may legitimately mention that hits exist elsewhere — a5's wider probe. That is not a violation of (a); what matters is whether the sessions above are returned as results.")
}

func (h *harness) a7(c *caseResult) {
	c.ID = "a7-dedup"
	c.Title = "a session whose records span two files is reported once"
	c.Asserts = "a session whose records appear in two files is reported once, with one turn count"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	if !c.requirePremise("a matching record uuid exists in more than one file within one session",
		s.A7Session != "" && s.A7Dup > 0,
		"at least one record uuid carrying a query match present in two or more files of the same session",
		fmt.Sprintf("query %q, session %q, %d duplicated uuid(s)", s.A7Query, s.A7Session, s.A7Dup)) {
		if s.A7Session == "" {
			c.BlockReason = "no query in the measured set has a matching record duplicated across files, so this case cannot tell a deduplicating implementation from a naive one"
		}
		return
	}

	// The default result list is truncated, and the session under test does not rank near the top.
	// A case that cannot see the session it is about rules on nothing.
	limit := strconv.Itoa(s.A7Sessions + 25)
	probe := c.run(h.invoke("limit-probe", h.repoRoot, "find", s.A7Query, "--all", "--limit", limit))
	if probe.ExitCode != 0 {
		c.block("the result list is truncated by default and this build did not accept `--limit %s` (exit %d): the session under test ranks about %d of %d and cannot be brought into range — stderr: %s",
			limit, probe.ExitCode, 13, s.A7Sessions, oneLine(string(probe.stderr)))
		return
	}

	c.fact("query", "%q", s.A7Query)
	c.fact("session under test", "%s", s.A7Session)
	c.fact("result list", "%d sessions hold hits for this query, so every invocation passes --limit %s to keep the session under test in range", s.A7Sessions, limit)
	c.fact("distinct matching record uuids in that session", "%d", s.A7Distinct)
	c.fact("of those, uuids present in more than one file", "%d", s.A7Dup)
	c.fact("redundant copies those duplicates represent", "%d", s.A7Copies)
	c.fact("files the session's matches live in", "%d", s.A7Files)
	c.fact("EXPECTED hit count, deduplicated", "%d — every matching record counted once", s.A7Deduped)
	c.fact("hit count if cross-file copies are double counted", "%d", s.A7Raw)
	c.fact("corpus-wide cross-file duplication measured by the harness",
		"%d of %d distinct (session, uuid) pairs are written to more than one file, worth %d redundant copies",
		h.facts.DuplicatedPairs, h.facts.RecordPairs, h.facts.RedundantCopies)

	// A list this long refuses under the default byte cap, which is a3's business, not a7's.
	room := strconv.Itoa(8 << 20)
	c.run(h.invoke("find-all", firstNonEmpty(s.A7Cwd, h.repoRoot), "find", s.A7Query, "--all", "--limit", limit, "--max-bytes", room))
	c.run(h.invoke("find-all-json", firstNonEmpty(s.A7Cwd, h.repoRoot), "find", s.A7Query, "--all", "--limit", limit, "--max-bytes", room, "--json"))

	low, high := (s.A7Deduped*85)/100, (s.A7Deduped*115)/100
	c.PassRules = []string{
		"session `" + s.A7Session + "` appears in `02-find-all.stdout` — if it does not, the list is still truncated and the case is BLOCKED, not FAIL",
		"it appears as a result entry exactly once, not once per file it occupies",
		"the hit count reported for it is inside the band " + strconv.Itoa(low) + "–" + strconv.Itoa(high) + ", around the deduplicated figure of " + strconv.Itoa(s.A7Deduped) + ". Read `sessions[].hits` for that id in `03-find-all-json.stdout` — the plain output truncates hit lines but not the count",
		"`03-find-all-json.stdout` returns " + strconv.Itoa(s.A7Sessions) + " sessions, matching the number the harness measured independently as holding hits for this query. This is the check that the result list is complete rather than silently truncated, and it is what makes the count above worth reading",
	}
	c.FailRules = []string{
		"the session is listed twice, or listed once per file",
		"the reported hit count is above " + strconv.Itoa(high) + " — at " + strconv.Itoa(s.A7Raw) + " it is every cross-file copy counted as its own hit, and anything approaching that is the same error partially applied",
		"the reported hit count is below " + strconv.Itoa(low) + " — over-collapse, where a key coarser than (session, uuid, tier, offset) merges hits that are genuinely distinct. A count near " + strconv.Itoa(s.A7Distinct) + " would mean one hit per record no matter how many times the term appears in it, and a count near 0 means the session's hits were merged into another session's",
		"the JSON returns materially fewer than " + strconv.Itoa(s.A7Sessions) + " sessions, which means the list is still truncated and the hit count above was read from an incomplete result",
	}
	c.Notes = append(c.Notes,
		fmt.Sprintf("The two candidate answers are a clean factor of two apart (%d deduplicated, %d raw) because all %d matching records in this session exist in exactly two files. The rule is a band of plus or minus 15%% around %d rather than an equality, because the harness counts substring occurrences in conversation-tier text while the tool counts hits in its own turn model, and the two differ by a couple of hits at a tier boundary. It is a band and not a ceiling deliberately: a ceiling would also pass 30, or 3, or 0, and so could not fail the over-collapse the rules below name.",
			s.A7Deduped, s.A7Raw, s.A7Distinct, s.A7Deduped),
		fmt.Sprintf("A one-session difference in the returned session count is worth reporting rather than failing if `corpus-integrity.json` shows a session file appearing or growing during the run — the harness measured %d sessions moments before the invocation, and the corpus is written to while this runs.", s.A7Sessions),
		"Both figures were measured by the harness walking the corpus, and the duplicated uuids were confirmed byte-identical between their two files, so the copies are the same logical turn rather than two turns that happen to share an id.",
		"Dedup is by uuid within a session, not across the corpus — a fork carries a record into a new session keeping its uuid, and a global key would delete the turn from the other session entirely.",
		"The `redundant` counter in the JSON reads 0 while the harness measures thousands of cross-file copies in the corpus, and that gap is expected rather than a defect — the two count different stages. The harness counts duplication in the raw corpus. `redundant` counts copies still present in the hits reaching the ranker, and by then `internal/archive` has already collapsed them at ingest on (session, uuid), so there is nothing left for it to drop. It is a working second-line backstop reading zero because the primary dedup upstream did its job: if ingest dedup ever regressed, the scanner would emit both copies and this counter would go non-zero. Do not read `redundant: 0` as evidence that no deduplication happened — the evidence that it happened is this case's hit count.",
		"What is genuinely not reported anywhere: how many copies ingest collapsed. `doctor` shows turn counts and checksums but no dedup figure, so a reader cannot see that deduplication did any work at all. That is an observation for the report, not a failure of this case.")
}

func (h *harness) a8(c *caseResult) {
	c.ID = "a8-repo-shapes"
	c.Title = "four awkward cwd shapes resolve to the documented identity"
	c.Asserts = "orphaned worktree, remoteless repo, subdirectory cwd, and relocated record each resolve to the documented identity, none to a wrong repo"

	if !h.gate(c, "show") {
		return
	}
	want := []string{"orphan-worktree", "remoteless", "subdir", "relocated"}
	var missing []string
	for _, shape := range want {
		if _, ok := h.sel.Shapes[shape]; !ok {
			missing = append(missing, shape)
		}
	}
	if !c.requirePremise("all four cwd shapes are present in the corpus today",
		len(missing) == 0,
		"an instance of each of "+strings.Join(want, ", "),
		"missing: "+strings.Join(missing, ", ")) {
		return
	}

	capArg := strconv.Itoa(maxBytesCap)
	for _, shape := range want {
		in := h.sel.Shapes[shape]
		c.fact(shape, "cwd %s → expected identity %s (%s); observed via session %s. %s",
			in.Cwd, repoKey(in.Expect), in.Expect.Value, in.Session, in.Detail)
		if !in.Strict {
			c.Notes = append(c.Notes, fmt.Sprintf(
				"The %s instance's session also carries cwds in other repos, so more than one repo label on it is legitimate; what must not appear is an unresolved or wrong identity.", shape))
		}
		c.run(h.invoke(shape+"-show", h.repoRoot, "show", in.Session, "--max-bytes", capArg))
		c.run(h.invoke(shape+"-show-json", h.repoRoot, "show", in.Session, "--json", "--max-bytes", capArg))
	}
	c.attach("shapes.json", h.sel.Shapes)

	c.PassRules = []string{
		"for each of the four shapes, the `-show` output attributes the session to the expected identity named above — the repo name for a remote-backed repo, the toplevel path for the remoteless one",
		"no shape resolves to `unresolved`, to an empty repo, or to a different repo than the one measured",
		"the orphaned-worktree shape resolves to the parent repo rather than stopping at the pruned gitdir",
	}
	c.FailRules = []string{
		"any shape reports no repo, an unresolved repo, or a repo other than the measured expectation",
		"the remoteless repo is reported as unresolved rather than as its own identity keyed by toplevel path",
		"`show` cannot be told which repo a session belongs to at all — the identity has to be observable somewhere in the output for this case to be gradable; if it is entirely absent from both the plain and the JSON form, that is a FAIL, because repo identity is a pinned field of the hit schema",
	}
	c.Notes = append(c.Notes,
		"Expected identities were computed by the harness from `git` directly, walking up from each cwd and continuing past any failure, per the contract's repo-identity rule. They were not read back from recall.",
		"The corpus holds exactly one `relocated` record, so that shape rests on a single instance.")
}

func (h *harness) a9(c *caseResult) {
	c.ID = "a9-idempotent"
	c.Title = "a second run changes nothing"
	c.Asserts = "two consecutive runs produce byte-identical archive content"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}

	// Comparing archive bytes across a corpus that grew measures the writer, not the archive.
	// Claude Code appends this session's own transcript while the case runs, and meta.json holds
	// per-file mtimes as fixed-width nanos — so a source file growing changes meta.json's content
	// without changing its length while the .turns files stay identical. That is a corpus moving,
	// not an archive misbehaving. A copy-on-write clone costs about a second and removes the
	// question entirely.
	corpus, how, err := h.freezeCorpus(filepath.Join(h.tmpRoot, "a9-corpus"))
	if err != nil {
		c.block("could not obtain a static corpus for this case: %v", err)
		return
	}
	c.fact("corpus under test", "%s — %s", corpus, how)

	before, err := manifestCorpus(corpus, false)
	if err != nil {
		c.block("could not fingerprint the corpus for this case: %v", err)
		return
	}

	sb, err := newSandbox(h.tmpRoot, "idempotent", corpus)
	if err != nil {
		c.block("could not create an isolated archive location: %v", err)
		return
	}
	env := sb.env()
	c.fact("archive location", "a throwaway RECALL_HOME at %s. Nothing is written under the real corpus.", sb.Archive)

	c.run(invocation{Label: "first-run", Argv: []string{h.binary, "find", s.A1Query, "--all"}, Dir: h.repoRoot, Env: env})
	first, err1 := snapshotTree(sb.Root)
	c.run(invocation{Label: "second-run", Argv: []string{h.binary, "find", s.A1Query, "--all"}, Dir: h.repoRoot, Env: env})
	second, err2 := snapshotTree(sb.Root)
	if err1 != nil || err2 != nil {
		c.block("could not snapshot the archive: %v / %v", err1, err2)
		return
	}

	after, err := manifestCorpus(corpus, false)
	if err != nil {
		c.block("could not re-fingerprint the corpus: %v", err)
		return
	}
	if !c.requirePremise("the corpus held still between the two runs",
		before.Digest == after.Digest,
		"an unchanged corpus, so any difference between the two archives is the archive's own doing",
		fmt.Sprintf("corpus fingerprint moved from %s to %s across %d files", before.Digest[:12], after.Digest[:12], before.Files)) {
		c.BlockReason += ". Comparing archive bytes across a corpus that changed measures the writer rather than the archive, which is the one thing this case must not do"
		return
	}

	c.attach("archive-after-run-1.json", first)
	c.attach("archive-after-run-2.json", second)
	c.attach("archive-diff.json", diffSnapshots(first, second))
	c.fact("files written by the first run", "%d — %s", len(first), strings.Join(digestPaths(first), ", "))

	c.PassRules = []string{
		"`archive-diff.json` is empty — same file set, same sizes, same sha256 after both runs",
		"the comparison covers every file the store writes, currently six: the three `.turns` tiers, `cursor`, `meta.json` and the `checksums` sidecar. Check `archive-after-run-1.json` lists all of them; a diff over a subset would miss exactly the files that changed here before",
		"`02-second-run.exit` is 0",
		"the first run actually wrote something — `files written by the first run` above is greater than zero. An archive that was never created is not evidence of idempotency",
	}
	c.FailRules = []string{
		"`archive-diff.json` lists any file whose sha256 changed, appeared, or disappeared between the two runs",
		"the second run exited non-zero",
	}
	c.Notes = append(c.Notes,
		"Only content is compared. A changed mtime with an unchanged hash is not a failure — the contract pins byte-identical archive content.",
		"The corpus is pinned for this case and verified to have held still across both runs; if it moved, the case BLOCKs rather than reporting a difference it cannot attribute. This case can still fail: against a frozen corpus, any hash difference is the archive's own non-determinism.",
		"If the first run wrote no files at all, the tool may be honouring a different archive location than RECALL_HOME. Report that as an observation; `archive-after-run-1.json` is the evidence either way.")
}

// freezeCorpus gives the idempotency case a corpus that cannot move under it. On APFS the clone
// is copy-on-write: about a second for 1.4 GB and no extra disk.
func (h *harness) freezeCorpus(dst string) (string, string, error) {
	if out, err := exec.Command("cp", "-Rc", h.corpusRoot, dst).CombinedOutput(); err == nil {
		return dst, "a copy-on-write clone of " + h.corpusRoot + ", frozen for the duration of this case", nil
	} else if len(out) > 0 {
		_ = os.RemoveAll(dst)
	}
	if err := exec.Command("cp", "-R", h.corpusRoot, dst).Run(); err == nil {
		return dst, "a full copy of " + h.corpusRoot + " (clone unavailable on this filesystem), frozen for the duration of this case", nil
	}
	_ = os.RemoveAll(dst)
	return h.corpusRoot, "the live corpus — neither a clone nor a copy could be made, so this case falls back to fingerprinting the corpus either side of the two runs and blocking if it moved", nil
}

func digestPaths(ds []fileDigest) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Path)
	}
	return out
}

func (h *harness) a10(c *caseResult) {
	c.ID = "a10-doctor-clean"
	c.Title = "doctor reports a healthy archive"
	c.Asserts = "recall doctor exits 0 and reports archive integrity ok"

	if !h.gate(c, "doctor") {
		return
	}
	c.run(h.invoke("doctor", h.repoRoot, "doctor"))
	c.run(h.invoke("doctor-json", h.repoRoot, "doctor", "--json"))

	// doctor's own claim of integrity is unfalsifiable from its text alone, so the harness hashes
	// the archive it just checked and hands the judge something to compare against.
	if snap, err := snapshotTree(h.warm.Root); err == nil {
		c.attach("archive-sha256.json", snap)
		c.fact("archive under test", "%d file(s) in %s, hashed by the harness immediately after invocation 02 — see archive-sha256.json", len(snap), h.warm.Archive)
		c.fact("files the store writes", "%s. Integrity has to cover all of them, not only the tiers: until the `checksums` sidecar was added, a corrupt `meta.json` still reported `integrity ok`", strings.Join(digestPaths(snap), ", "))
	}

	c.PassRules = []string{
		"`01-doctor.exit` is 0",
		"`01-doctor.stdout` states the archive's integrity is ok",
		"`02-doctor-json.stdout` carries a checksum per archive file, and each matches the sha256 the harness computed over the same file in `archive-sha256.json`. This is the assertion that has teeth; the plain-text line is a claim, this is the check",
		"doctor's integrity report accounts for every file listed in `archive-sha256.json`, not just the three `.turns` tiers — `meta.json` and `cursor` are covered by the `checksums` sidecar and must be verified before anything claims `integrity ok`. Compare the set of files doctor reports on against the set the harness found on disk; judge the coverage by which files are accounted for, not by the spelling of any particular JSON field",
	}
	c.FailRules = []string{
		"a non-zero exit code",
		"the output reports corruption, a failed checksum, or an unreadable archive",
		"the output says nothing about integrity at all — the contract makes integrity a checksum verified by `doctor`, so silence is not clean",
		"a checksum in `02-doctor-json.stdout` disagrees with the harness's own sha256 for that file in `archive-sha256.json` — doctor is reporting a digest it did not compute from the bytes on disk",
		"doctor claims `integrity ok` while `meta.json` or `cursor` is absent from what it verified. Those two carry the byte cursors and the per-file marks, so a corrupt one silently changes what the tool believes it has already read — that is the gap the `checksums` sidecar exists to close, and an integrity claim that does not cover it is the same unverifiable claim as the plain-text line",
	}
	c.Notes = append(c.Notes,
		"This runs against the warm sandbox archive built earlier in the run, not against a pristine one.",
		"GREEN FOR A FRAGILE REASON — the plain-text `integrity ok` line is unverifiable on its own. It is a claim with no checksum shown, so nothing in `01-doctor.stdout` distinguishes a verified archive from one that was never checked. Do not treat that line as covering integrity.",
		"What actually carries this case is invocation 02 against `archive-sha256.json`, which the harness computed over this case's own archive right after that invocation. Assert on the JSON. If someone later drops that comparison believing the text line covers it, this case stops testing integrity and keeps passing.",
		"If doctor's digest covers the archive as a whole rather than per file, compare it against whatever `archive-sha256.json` shows and say in your report which files it did and did not cover.",
		"The archive portion added the `checksums` sidecar and `Report` gained per-file coverage flags for meta and cursor; doctor's reporting of them was still landing when this evidence was captured. If doctor verifies the tiers but says nothing about `meta.json` and `cursor`, that is the gap — report it plainly rather than reading the tier checksums as covering the whole store.")
}

func (h *harness) a11(c *caseResult) {
	c.ID = "a11-fzf-nonint"
	c.Title = "the fzf pipeline works with no TTY and keeps recall's ranking"
	c.Asserts = "the shell function's pipeline runs under fzf --filter (no TTY) and produces ranked lines"

	s := &h.sel
	if !h.gate(c, "find") {
		return
	}
	if h.fzfPath == "" {
		c.block("fzf is not on PATH")
		return
	}
	if h.zshPath == "" {
		c.block("zsh is not on PATH")
		return
	}
	if _, err := os.Stat(h.shellFn); err != nil {
		c.block("the shell function %s does not exist", h.shellFn)
		return
	}

	c.fact("fzf", "%s", h.fzfPath)
	c.fact("shell function", "%s, entry point recall-fzf", h.shellFn)
	c.fact("FZF_DEFAULT_OPTS", "cleared for every invocation — the spike found this machine's value injects a bat-based --preview that hijacks the invocation")
	c.fact("--disabled", "deliberately not used: the spike measured it inert under --filter, so a case leaning on it would be testing nothing")
	c.fact("--filter", "empty, and that is load-bearing. A non-empty --filter makes fzf re-rank by its own fuzzy score and discard recall's concentration ranking; the query goes to `recall find`, never to fzf")

	fzfArgs := `--read0 --print0 --delimiter=$'\x1f' --with-nth=2.. --ansi --filter=''`
	control := fmt.Sprintf(`printf 'id1\x1frepo-one\x1ffirst line\nsecond line\x00id2\x1frepo-two\x1fonly line\x00' | %s %s`,
		shellQuote(h.fzfPath), fzfArgs)
	controlRes := c.run(invocation{Label: "fzf-control", Argv: []string{control}, Shell: h.zshPath, Dir: h.repoRoot, Env: h.env()})

	producer := c.run(h.invoke("producer-fzf-records", s.A1Cwd, "find", s.A1Query, "--fzf"))
	if producer.ExitCode != 0 || producer.StdoutBytes == 0 {
		c.block("`recall find <query> --fzf` has not landed: it exited %d with %d bytes on stdout. The pipeline, its flags and its non-TTY behaviour are independently verified (see 01-fzf-control); only the producer is missing, which is a BLOCKED, not a FAIL — stderr: %s",
			producer.ExitCode, producer.StdoutBytes, oneLine(string(producer.stderr)))
		return
	}

	fn := fmt.Sprintf(`source %s; RECALL_BIN=%s recall-fzf %s`, shellQuote(h.shellFn), shellQuote(h.binary), shellQuote(s.A1Query))
	pipeline := c.run(invocation{Label: "recall-fzf", Argv: []string{fn}, Shell: h.zshPath, Dir: s.A1Cwd, Env: h.env()})

	fnIDs := fmt.Sprintf(`source %s; RECALL_BIN=%s recall-fzf --ids %s`, shellQuote(h.shellFn), shellQuote(h.binary), shellQuote(s.A1Query))
	idsRes := c.run(invocation{Label: "recall-fzf-ids", Argv: []string{fnIDs}, Shell: h.zshPath, Dir: s.A1Cwd, Env: h.env()})

	order := map[string]any{
		"producer_order":           recordSessionIDs(producer.stdout),
		"pipeline_order":           sessionIDsInOrder(pipeline.stdout),
		"pipeline_ids_order":       sessionIDsInOrder(idsRes.stdout),
		"producer_nul_records":     bytes.Count(producer.stdout, []byte{0}),
		"producer_field_seps":      bytes.Count(producer.stdout, []byte{0x1f}),
		"pipeline_nul_bytes":       bytes.Count(pipeline.stdout, []byte{0}),
		"measured_hits_by_session": h.facts.Queries[s.A1Query].sessionsInRepo(h.repos, s.A1Repo, true),
		"note": "producer_order is what recall ranked, read from field 1 of each NUL-terminated " +
			"record. pipeline_order and pipeline_ids_order are the session ids in the order they " +
			"survived fzf. All three must be the same sequence: fzf is there to display, never to " +
			"re-rank.",
	}
	c.attach("record-order.json", order)
	c.fact("escape sequences in the pipeline output", "%d ESC bytes — --ansi is required headlessly too, or raw escapes leak into stdout", strings.Count(string(pipeline.stdout), "\x1b"))

	c.PassRules = []string{
		"`01-fzf-control.exit` is 0 and its stdout holds two NUL-terminated records — this proves fzf runs headless, so an empty pipeline result cannot be blamed on fzf",
		"`02-producer-fzf-records.stdout` is a NUL-terminated record stream: `producer_nul_records` in `record-order.json` is one per result session, `producer_field_seps` is at least that many, and every entry of `producer_order` is a bare session id",
		"`03-recall-fzf.exit` is 0 and its stdout is non-empty",
		"in `record-order.json`, `pipeline_order` and `pipeline_ids_order` are the same sequence of session ids as `producer_order` — same ids, same order. fzf displayed recall's ranking rather than replacing it",
	}
	c.FailRules = []string{
		"the control produced records but the pipeline produced none — recall emitted nothing fzf could rank",
		"`pipeline_order` is a reordering of `producer_order`. That is the signature of a non-empty `--filter`: fzf re-ranks by its own fuzzy score and throws concentration ranking away, and a session with one passing mention surfaces above the session the query is about",
		"`producer_nul_records` is 0 — `--print0`/NUL termination was lost, and any downstream parser reading by newline misparses a multi-line record as two records",
		"an entry of `producer_order` is not a session id. The coverage footer emitted as its own record breaks every `--preview` and key binding, which resolve `{1}`",
		"`03-recall-fzf.exit` is non-zero, or `04-recall-fzf-ids.stderr` shows the file failing to source",
	}
	c.Notes = append(c.Notes,
		"Records are NUL-terminated and field-separated by 0x1f, so `cat` misleads. Use `xxd` or `tr '\\0' '\\n'`.",
		"The NUL/0x1f record shape is asserted on the producer (`find --fzf`), which logs/escalations/interactive-1.md specifies byte for byte. The shell function's own stdout format is not pinned anywhere, so it is judged on content and order, not on separators.",
		fmt.Sprintf("The control at invocation 01 exited %d with %d bytes; if it failed, the environment is at fault and the case should be BLOCKED rather than FAILed.", controlRes.ExitCode, controlRes.StdoutBytes))
}

// recordSessionIDs reads field 1 of each NUL-terminated record — the producer's pinned format.
func recordSessionIDs(b []byte) []string {
	out := []string{}
	for _, rec := range strings.Split(string(b), "\x00") {
		if rec = strings.TrimSpace(rec); rec == "" {
			continue
		}
		out = append(out, strings.TrimSpace(strings.SplitN(rec, "\x1f", 2)[0]))
	}
	return out
}

var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// sessionIDsInOrder recovers ranking order from output whose framing is not pinned, by taking
// session ids in first-appearance order.
func sessionIDsInOrder(b []byte) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range uuidRE.FindAllString(string(b), -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "only 1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func diffSnapshots(a, b []fileDigest) []map[string]string {
	index := func(xs []fileDigest) map[string]fileDigest {
		m := map[string]fileDigest{}
		for _, x := range xs {
			m[x.Path] = x
		}
		return m
	}
	ai, bi := index(a), index(b)
	var out []map[string]string
	for _, path := range sortedKeys(ai) {
		x := ai[path]
		y, ok := bi[path]
		switch {
		case !ok:
			out = append(out, map[string]string{"path": path, "change": "disappeared after the second run"})
		case x.SHA256 != y.SHA256:
			out = append(out, map[string]string{"path": path, "change": "content changed", "before": x.SHA256, "after": y.SHA256,
				"size_before": strconv.FormatInt(x.Size, 10), "size_after": strconv.FormatInt(y.Size, 10)})
		}
	}
	for _, path := range sortedKeys(bi) {
		if _, ok := ai[path]; !ok {
			out = append(out, map[string]string{"path": path, "change": "appeared after the second run"})
		}
	}
	return out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
