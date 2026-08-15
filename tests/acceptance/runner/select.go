package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Queries the harness measures on every run. The first three are the a1 candidates ranked in
// logs/acceptance-queries.md; the cross-repo candidates exist so a2 still has something to
// discriminate with if the a1 query stops spanning repos.
func scanQueries(sentinels ...string) []string {
	base := []string{
		"agvtool", "save-bundle", "failure-notification",
		"test-bitrise-staging-build",
		"gjson", "flightplan", "chezmoi",
	}
	return append(base, sentinels...)
}

// newSentinel returns a token for the miss-path timing gate. It is random per run and never
// hardcoded, because a fixed sentinel is guaranteed to end up in the corpus: this harness
// discusses its own token in prose, Claude Code writes that prose to a transcript, and from then
// on the token has hits and `find` takes the cheaper hit-elsewhere path instead of the miss path
// the gate exists to measure. 128 bits of randomness cannot be in a corpus written before the run.
func newSentinel() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "x" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "x" + hex.EncodeToString(b)
}

type shapeInstance struct {
	Shape   string       `json:"shape"`
	Cwd     string       `json:"cwd"`
	Session string       `json:"session"`
	Expect  repoIdentity `json:"expect"`
	Detail  string       `json:"detail"`
	Strict  bool         `json:"unambiguous_session"`
}

type selection struct {
	A1Query    string
	A1Cwd      string
	A1Repo     repoIdentity
	A1Sessions []string
	A1Local    int
	A1InRepo   int

	A2Query       string
	A2Repos       map[string]int
	A2Qualifying  []string
	A2SelfRepo    string
	A2Substituted bool
	A2OrigQual    int

	A5Cwd   string
	A5Repo  repoIdentity
	A5Query string

	A6Query    string
	A6Cwd      string
	A6Repo     repoIdentity
	A6Conv     int
	A6Result   int
	A6Sessions []string

	A3Session string
	A3Bytes   int64
	A3Cwd     string

	A7Query    string
	A7Session  string
	A7Cwd      string
	A7Dup      int
	A7Copies   int
	A7Distinct int
	A7Files    int
	A7Raw      int
	A7Deduped  int
	A7Sessions int

	Shapes map[string]shapeInstance
}

func (h *harness) selectAll() {
	s := &h.sel
	rr := h.repos
	c := h.facts

	h.pickCrossCheckout()
	h.pickResultOnly()

	// The repo this tool is being built in does not count toward cross-repo reach. Its sessions
	// discuss the test queries in prose, so it appears as a second repo for any query the harness
	// has ever named — self-contamination wearing the costume of the thing being tested.
	s.A2SelfRepo = repoKey(rr.resolve(h.repoRoot))
	qualifying := func(spread map[string]int) []string {
		out := []string{}
		for k := range spread {
			if k != s.A2SelfRepo {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	s.A2Query = s.A1Query
	s.A2Repos = c.Queries[s.A1Query].repoSpread(rr, true)
	s.A2Qualifying = qualifying(s.A2Repos)
	s.A2OrigQual = len(s.A2Qualifying)
	if len(s.A2Qualifying) < 2 {
		for _, q := range []string{"gjson", "flightplan", "chezmoi"} {
			spread := c.Queries[q].repoSpread(rr, true)
			if qs := qualifying(spread); len(qs) >= 2 {
				s.A2Query, s.A2Repos, s.A2Qualifying = q, spread, qs
				s.A2Substituted = true
				break
			}
		}
	}

	s.A5Query = s.A1Query
	s.A5Cwd, s.A5Repo = h.pickZeroHitRepo(s.A1Query, s.A1Repo)

	s.A3Session, s.A3Bytes = c.largestSession()
	s.A3Cwd = existingCwdOf(c, s.A3Session)

	h.pickDedupCase()
	s.Shapes = h.pickShapes()
}

// checkout is one working directory tree a repo was worked in, with every cwd the corpus recorded
// inside it. The identity resolves at the checkout root, so two clones of one repo share an
// identity and differ in their root — which is exactly the shape a1 needs.
type checkout struct {
	root string
	id   repoIdentity
	cwds []string
}

// checkoutsByRepo groups every cwd the corpus holds by the repo it belongs to and the checkout it
// sits in. Only checkouts still on disk are kept: the harness sets a real working directory, and a
// path that is gone cannot be one.
func (h *harness) checkoutsByRepo() map[string][]checkout {
	byRoot := map[string]*checkout{}
	for _, cwd := range sortedKeys(h.facts.CwdSessions) {
		id := h.repos.resolve(cwd)
		if id.Kind == "outside" || id.Via == "" {
			continue
		}
		if st, err := os.Stat(id.Via); err != nil || !st.IsDir() {
			continue
		}
		co := byRoot[id.Via]
		if co == nil {
			co = &checkout{root: id.Via, id: id}
			byRoot[id.Via] = co
		}
		co.cwds = append(co.cwds, cwd)
	}
	out := map[string][]checkout{}
	for _, root := range sortedKeys(byRoot) {
		co := byRoot[root]
		out[repoKey(co.id)] = append(out[repoKey(co.id)], *co)
	}
	return out
}

func (c checkout) convHits(qf *queryFacts) int {
	total := 0
	for _, cwd := range c.cwds {
		total += qf.ConvByCwd[cwd]
	}
	return total
}

// pickCrossCheckout looks for a1's premise on whatever machine the harness runs on: one repo with
// two checkouts still on disk, and a query the corpus carries in one and not the other. Naming two
// directories outright, as this used to, makes the case unrunnable for anyone else and silently
// wrong here the moment a checkout is deleted. Queries are tried in the ranked order scanQueries
// lists them, so the candidate the harness would have chosen anyway still wins when it qualifies.
func (h *harness) pickCrossCheckout() {
	s := &h.sel
	byRepo := h.checkoutsByRepo()
	for _, q := range h.scanned {
		qf := h.facts.Queries[q]
		if qf == nil {
			continue
		}
		for _, key := range sortedKeys(byRepo) {
			cos := byRepo[key]
			if len(cos) < 2 {
				continue
			}
			from, elsewhere := splitByHits(cos, qf)
			if from == nil || elsewhere == 0 {
				continue
			}
			s.A1Query, s.A1Cwd, s.A1Repo = q, from.root, from.id
			h.measureA1(qf)
			return
		}
	}

	// Nothing qualified, so a1 measures the repo the harness runs in and blocks on that: the absence
	// of a second checkout is a property of this machine, never a defect in recall, and never a PASS
	// on a case that measured nothing.
	s.A1Query = h.scanned[0]
	s.A1Cwd, s.A1Repo = h.repoRoot, h.repos.resolve(h.repoRoot)
	h.measureA1(h.facts.Queries[s.A1Query])
}

// measureA1 fills in what the chosen query and checkout actually carry, which is what a1 states as
// its premise and blocks on when it no longer holds.
func (h *harness) measureA1(qf *queryFacts) {
	s := &h.sel
	if qf == nil {
		return
	}
	s.A1Local, s.A1InRepo = hitsUnderPrefix(qf.ConvByCwd, s.A1Cwd), 0
	inRepo := qf.sessionsInRepo(h.repos, s.A1Repo, true)
	s.A1Sessions = sortedKeys(inRepo)
	for _, n := range inRepo {
		s.A1InRepo += n
	}
}

// splitByHits returns a checkout the query is absent from and the number of hits its siblings
// carry, which together are the whole of a1's premise.
func splitByHits(cos []checkout, qf *queryFacts) (*checkout, int) {
	var zero *checkout
	elsewhere := 0
	for i := range cos {
		if n := cos[i].convHits(qf); n > 0 {
			elsewhere += n
		} else if zero == nil {
			zero = &cos[i]
		}
	}
	return zero, elsewhere
}

// pickResultOnly finds a6's premise: a repo whose conversation never mentions the query and whose
// tool output does. It is discovered rather than named for the same reason a1's checkouts are —
// the token this used to name has since been typed into conversation in the repo recall is built
// in, and a premise that decays into being false is worse than one never asserted. It takes the
// candidate with the most tool-result hits rather than the first it meets, because a repo holding
// one hit demonstrates the same thing far more weakly.
func (h *harness) pickResultOnly() {
	s := &h.sel
	byRepo := h.checkoutsByRepo()
	best := 0
	var bestQuery, bestCwd string
	var bestRepo repoIdentity
	for _, q := range h.scanned {
		qf := h.facts.Queries[q]
		if qf == nil {
			continue
		}
		for _, key := range sortedKeys(byRepo) {
			for _, co := range byRepo[key] {
				s.A6Query, s.A6Cwd, s.A6Repo = q, co.root, co.id
				if h.measureA6(qf); s.A6Conv == 0 && s.A6Result > best {
					best, bestQuery, bestCwd, bestRepo = s.A6Result, q, co.root, co.id
				}
			}
		}
	}

	s.A6Query, s.A6Cwd, s.A6Repo = bestQuery, bestCwd, bestRepo
	if best == 0 {
		// Nothing qualified, so a6 states what the repo the harness runs in carries and blocks on
		// that measurement: a build with nothing to exclude from the default tier cannot show that
		// it excludes anything, and that is the harness's inability rather than a defect.
		s.A6Query = h.scanned[0]
		s.A6Cwd, s.A6Repo = h.repoRoot, h.repos.resolve(h.repoRoot)
	}
	h.measureA6(h.facts.Queries[s.A6Query])
}

func (h *harness) measureA6(qf *queryFacts) {
	s := &h.sel
	s.A6Conv, s.A6Result, s.A6Sessions = 0, 0, nil
	if qf == nil {
		return
	}
	for _, n := range qf.sessionsInRepo(h.repos, s.A6Repo, true) {
		s.A6Conv += n
	}
	res := qf.sessionsInRepo(h.repos, s.A6Repo, false)
	s.A6Sessions = sortedKeys(res)
	for _, n := range res {
		s.A6Result += n
	}
}

func hitsUnderPrefix(byCwd map[string]int, prefix string) int {
	total := 0
	for cwd, n := range byCwd {
		if cwd == prefix || strings.HasPrefix(cwd, prefix+string(filepath.Separator)) {
			total += n
		}
	}
	return total
}

// pickZeroHitRepo finds a repo that exists on disk, has sessions in the corpus, and has no
// conversation-tier hit for the query — the situation a5's wider probe is meant to rescue.
func (h *harness) pickZeroHitRepo(query string, exclude repoIdentity) (string, repoIdentity) {
	qf := h.facts.Queries[query]
	type cand struct {
		cwd      string
		id       repoIdentity
		sessions int
	}
	byRepo := map[string]*cand{}
	for cwd, sessions := range h.facts.CwdSessions {
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			continue
		}
		id := h.repos.resolve(cwd)
		if id.Kind == "outside" || sameRepo(id, exclude) {
			continue
		}
		key := repoKey(id)
		if cur := byRepo[key]; cur == nil || len(cwd) < len(cur.cwd) {
			byRepo[key] = &cand{cwd: cwd, id: id}
		}
		byRepo[key].sessions += len(sessions)
	}
	keys := make([]string, 0, len(byRepo))
	for k := range byRepo {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := byRepo[keys[i]], byRepo[keys[j]]
		if a.sessions != b.sessions {
			return a.sessions > b.sessions
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		cd := byRepo[k]
		hits := 0
		for cwd, n := range qf.ConvByCwd {
			if sameRepo(h.repos.resolve(cwd), cd.id) {
				hits += n
			}
		}
		if hits == 0 {
			return cd.cwd, cd.id
		}
	}
	return "", repoIdentity{}
}

// pickDedupCase finds the query and session with the most record uuids duplicated across files.
// Without one, a7 cannot tell a deduplicating implementation from a naive one.
func (h *harness) pickDedupCase() {
	s := &h.sel
	best := 0
	for _, q := range h.scanned {
		qf := h.facts.Queries[q]
		for session := range qf.ConvUUIDFiles {
			dup, copies := qf.dupUUIDs(session)
			if dup > best {
				best = dup
				s.A7Query, s.A7Session = q, session
				s.A7Dup, s.A7Copies = dup, copies
				s.A7Distinct = qf.distinctUUIDs(session)
				s.A7Files = len(qf.ConvFilesBySession[session])
				s.A7Raw = qf.ConvBySession[session]
				s.A7Deduped = qf.dedupedHits(session)
				s.A7Sessions = len(qf.ConvBySession)
			}
		}
	}
	if s.A7Session != "" {
		s.A7Cwd = existingCwdOf(h.facts, s.A7Session)
	}
}

func (h *harness) pickShapes() map[string]shapeInstance {
	out := map[string]shapeInstance{}
	cwds := sortedKeys(h.facts.CwdRecords)
	used := map[string]bool{}

	// Relocated first: the corpus holds one such record, so it has no choice of session and every
	// other shape must work around whatever it takes.
	for _, session := range sortedKeys(h.facts.RelocatedCwds) {
		rc := h.facts.RelocatedCwds[session]
		id := h.repos.resolve(rc)
		if id.Kind == "outside" {
			continue
		}
		out["relocated"] = shapeInstance{
			Shape: "relocated", Cwd: rc, Session: session, Expect: id, Strict: true,
			Detail: "record type `relocated` carries relocatedCwd and no cwd",
		}
		used[session] = true
		break
	}

	// An orphaned worktree is one whose own directory answers no git question, so the identity
	// only appears after the walk continues past the failure.
	h.assignShape(out, used, "orphan-worktree", cwds, func(cwd string) (repoIdentity, string, bool) {
		id := h.repos.resolve(cwd)
		if !strings.Contains(cwd, "/worktrees/") || id.Kind != "remote" || id.Via == cwd {
			return id, "", false
		}
		return id, "git fails at the worktree path itself; identity resolves at ancestor " + id.Via, true
	})
	h.assignShape(out, used, "remoteless", cwds, func(cwd string) (repoIdentity, string, bool) {
		id := h.repos.resolve(cwd)
		if id.Kind != "no-remote" {
			return id, "", false
		}
		return id, "real repo with no origin; identity is keyed by toplevel path " + id.Value, true
	})
	h.assignShape(out, used, "subdir", cwds, func(cwd string) (repoIdentity, string, bool) {
		id := h.repos.resolve(cwd)
		if id.Kind != "remote" || strings.Contains(cwd, "/worktrees/") {
			return id, "", false
		}
		top, err := gitOut(cwd, "rev-parse", "--show-toplevel")
		if err != nil || top == cwd || top == "" {
			return id, "", false
		}
		return id, "cwd is a subdirectory of toplevel " + top, true
	})
	return out
}

// assignShape sweeps the candidate cwds twice: once demanding a session no other shape is already
// standing in for, and only then settling for a shared one. Two shapes on one session test the
// second shape barely at all.
func (h *harness) assignShape(out map[string]shapeInstance, used map[string]bool, shape string, cwds []string,
	match func(cwd string) (repoIdentity, string, bool)) {
	for _, allowUsed := range []bool{false, true} {
		for _, cwd := range cwds {
			id, detail, ok := match(cwd)
			if !ok {
				continue
			}
			if inst, ok := h.instanceFor(shape, cwd, id, used, detail, allowUsed); ok {
				out[shape] = inst
				used[inst.Session] = true
				return
			}
		}
	}
}

// instanceFor prefers a session not already standing in for another shape, and among those one
// whose every cwd resolves to the same identity, so the expected repo label is unambiguous.
func (h *harness) instanceFor(shape, cwd string, id repoIdentity, used map[string]bool, detail string, allowUsed bool) (shapeInstance, bool) {
	sessions := sortedKeys(h.facts.CwdSessions[cwd])
	var loose, reused string
	for _, session := range sessions {
		sf := h.facts.Sessions[session]
		if sf == nil {
			continue
		}
		consistent := true
		for other := range sf.Cwds {
			if !sameRepo(h.repos.resolve(other), id) {
				consistent = false
				break
			}
		}
		if used[session] {
			if reused == "" && consistent {
				reused = session
			}
			continue
		}
		if consistent {
			return shapeInstance{Shape: shape, Cwd: cwd, Session: session, Expect: id, Detail: detail, Strict: true}, true
		}
		if loose == "" {
			loose = session
		}
	}
	switch {
	case loose != "":
		return shapeInstance{Shape: shape, Cwd: cwd, Session: loose, Expect: id, Strict: false,
			Detail: detail + "; this session also has cwds in other repos, so more than one repo label on it is legitimate"}, true
	case allowUsed && reused != "":
		return shapeInstance{Shape: shape, Cwd: cwd, Session: reused, Expect: id, Strict: false,
			Detail: detail + "; no unused session exists at this cwd, so this shape shares a session with another shape and exercises it less independently"}, true
	}
	return shapeInstance{}, false
}

func existingCwdOf(c *corpusFacts, session string) string {
	sf := c.Sessions[session]
	if sf == nil {
		return ""
	}
	for _, cwd := range sortedKeys(sf.Cwds) {
		if st, err := os.Stat(cwd); err == nil && st.IsDir() {
			return cwd
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
