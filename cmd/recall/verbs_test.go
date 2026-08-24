package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
)

// The coverage declaration every searching verb emits by default, pinned in
// docs/design.md's no-false-negatives decision.
const defaultTierDeclaration = "conversation only — tool output NOT searched (--results)"

// harness materializes the shared fixture corpus and points recall at it. The
// archive goes to a fresh temp dir, so nothing here can touch the real one.
func harness(t *testing.T) fixtures.Corpus {
	t.Helper()
	c, _ := harnessAt(t)
	return c
}

// harnessAt also returns the archive directory, for the tests that corrupt it.
func harnessAt(t *testing.T) (fixtures.Corpus, string) {
	t.Helper()
	c := fixtures.Materialize(t)
	home := t.TempDir()
	t.Setenv("RECALL_HOME", home)
	t.Setenv("CLAUDE_PROJECTS_DIR", c.Root)
	return c, home
}

func callFind(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = notFoundIsNotAFailure(find(args, &out, &errOut))
	return out.String(), errOut.String(), err
}

func callWhen(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := notFoundIsNotAFailure(when(args, &out))
	return out.String(), err
}

// notFoundIsNotAFailure drops the sentinel a searching verb returns to set exit
// 1. The report is on stdout either way, and a test asserting on that report is
// asserting on a search that ran, not on one that broke.
func notFoundIsNotAFailure(err error) error {
	var typed *fperr.Error
	if errors.As(err, &typed) && typed.Code == fperr.NoHits {
		return nil
	}
	return err
}

func callShow(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := show(args, &out)
	return out.String(), err
}

func callDoctor(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := doctor(args, &out)
	return out.String(), err
}

func decodeJSON(t *testing.T, blob string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(blob), &got); err != nil {
		t.Fatalf("--json output does not parse: %v\n%s", err, blob)
	}
	return got
}

func codeOf(t *testing.T, err error) fperr.Code {
	t.Helper()
	var fe *fperr.Error
	if !errors.As(err, &fe) {
		t.Fatalf("error carries no code: %v", err)
	}
	return fe.Code
}

// TestFindReachesAnotherDirectoryOfTheSameRepo is acceptance case a1's shape on
// the shared fixtures: standing in the orphaned worktree, a query whose only hit
// was recorded in a different directory of the same repo returns it. That is the
// failure the whole tool exists to invert.
func TestFindReachesAnotherDirectoryOfTheSameRepo(t *testing.T) {
	c := harness(t)
	t.Chdir(c.ScratchPath(fixtures.ScratchOrphan))

	out, _, err := callFind(t, fixtures.NeedleConversation)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(out, fixtures.SessNeedle) {
		t.Errorf("standing in the orphaned worktree, %q did not return session %s, whose cwd was the repo root:\n%s",
			fixtures.NeedleConversation, fixtures.SessNeedle, out)
	}
	if !strings.Contains(out, defaultTierDeclaration) {
		t.Errorf("no coverage declaration on a searching command:\n%s", out)
	}
}

// TestEverySearchingVerbEmitsTheCoverageLine is acceptance case a4. A command
// that searches without emitting this is a defect, because the requirements
// dealbreaker is a *silent* false negative.
func TestEverySearchingVerbEmitsTheCoverageLine(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	cases := []struct {
		verb string
		run  func() (string, error)
	}{
		{"find", func() (string, error) { s, _, e := callFind(t, fixtures.NeedleConversation, "--all"); return s, e }},
		{"when", func() (string, error) { return callWhen(t, fixtures.NeedleConversation, "--all") }},
		{"show", func() (string, error) { return callShow(t, fixtures.SessNeedle, fixtures.NeedleConversation) }},
		{"find on a miss", func() (string, error) {
			s, _, e := callFind(t, "zzzzznothinghere", "--all")
			return s, e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			out, err := tc.run()
			if err != nil {
				t.Fatalf("%s: %v", tc.verb, err)
			}
			if !strings.Contains(out, defaultTierDeclaration) {
				t.Errorf("%s did not name the unsearched tier:\n%s", tc.verb, out)
			}
			if !strings.Contains(out, "── live to ") {
				t.Errorf("%s did not report the live boundary:\n%s", tc.verb, out)
			}
			if !strings.Contains(out, " searched · ") {
				t.Errorf("%s did not report how much it searched:\n%s", tc.verb, out)
			}
		})
	}
}

// TestToolOutputIsOptInAndTheDefaultRunSaysSo is acceptance case a6: the token
// planted in the result tier is not returned by default, is returned with
// --results, and the default run declared the gap.
func TestToolOutputIsOptInAndTheDefaultRunSaysSo(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, fixtures.NeedleResult, "--all")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if strings.Contains(out, fixtures.SessHugeResult) {
		t.Errorf("%q lives only in tool output and was returned by a default search:\n%s", fixtures.NeedleResult, out)
	}
	if !strings.Contains(out, defaultTierDeclaration) {
		t.Errorf("the default run did not declare that tool output was skipped:\n%s", out)
	}

	out, _, err = callFind(t, fixtures.NeedleResult, "--all", "--results")
	if err != nil {
		t.Fatalf("find --results: %v", err)
	}
	if !strings.Contains(out, fixtures.SessHugeResult) {
		t.Errorf("--results did not find %q in session %s:\n%s", fixtures.NeedleResult, fixtures.SessHugeResult, out)
	}
	if strings.Contains(out, defaultTierDeclaration) {
		t.Errorf("--results searched tool output but still claimed it had not:\n%s", out)
	}
}

// TestRecordInTwoFilesIsCountedOnce is acceptance case a7. The token sits on one
// record uuid that two source files carry; the session must report one hit, not
// two.
func TestRecordInTwoFilesIsCountedOnce(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, fixtures.NeedleDuplicated, "--all", "--json")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	got := decodeJSON(t, out)
	if n := got["hits"]; n != float64(1) {
		t.Errorf("hits = %v, want 1 — the record is carried by two files and must collapse (fixtures.Manifest.DupUUIDs)", n)
	}
	sessions, _ := got["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if id := sessions[0].(map[string]any)["id"]; id != fixtures.SessDup {
		t.Errorf("session = %v, want %s", id, fixtures.SessDup)
	}
}

// TestRepoScopedMissProbesWiderInsteadOfReportingZero is acceptance case a5, and
// the guard against the dangerous silence: the token lives in the remoteless
// repo, and a search from the repo with a remote must say where it is.
func TestRepoScopedMissProbesWiderInsteadOfReportingZero(t *testing.T) {
	c := harness(t)
	t.Chdir(c.ScratchPath(fixtures.ScratchNormal))

	out, _, err := callFind(t, fixtures.NeedleRemoteless)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(out, "found elsewhere") {
		t.Fatalf("a repo-scoped miss reported a bare zero:\n%s", out)
	}
	if !strings.Contains(out, c.ScratchPath(fixtures.ScratchRemoteless)) {
		t.Errorf("the report does not name the repo that holds it (%s):\n%s", fixtures.RepoNoRemote, out)
	}
	if !strings.Contains(out, "recall find "+fixtures.NeedleRemoteless+" --all") {
		t.Errorf("the report gives no next command:\n%s", out)
	}

	wide, _, err := callFind(t, fixtures.NeedleRemoteless, "--all")
	if err != nil {
		t.Fatalf("find --all: %v", err)
	}
	if !strings.Contains(wide, fixtures.SessRemoteless) {
		t.Errorf("--all did not return session %s:\n%s", fixtures.SessRemoteless, wide)
	}
}

// TestOutputRefusesRatherThanTruncating is acceptance case a3. --full is the
// path that can ask for a whole session, and it stays behind the cap.
func TestOutputRefusesRatherThanTruncating(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	for _, tc := range []struct {
		name string
		run  func() (string, error)
	}{
		{"show --full", func() (string, error) {
			return callShow(t, fixtures.SessNeedle, "--full", "--max-bytes", "40")
		}},
		{"find", func() (string, error) {
			s, _, e := callFind(t, fixtures.NeedleConversation, "--all", "--max-bytes", "40")
			return s, e
		}},
		{"find --json", func() (string, error) {
			s, _, e := callFind(t, fixtures.NeedleConversation, "--all", "--json", "--max-bytes", "40")
			return s, e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.run()
			if err == nil {
				t.Fatalf("emitted %d bytes under a 40 byte cap", len(out))
			}
			if got := codeOf(t, err); got != fperr.OutputTooLarge {
				t.Errorf("code = %s, want %s", got, fperr.OutputTooLarge)
			}
			if out != "" {
				t.Errorf("wrote %d bytes before refusing; a partial write is a truncation", len(out))
			}
		})
	}
}

// TestShowReturnsAWindowNotTheWholeSession is the fetch decision: the mean
// session's conversation is ~67K tokens, so show anchors on the hit.
func TestShowReturnsAWindowNotTheWholeSession(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	windowed, err := callShow(t, fixtures.SessNeedle, fixtures.NeedleConversation, "--around", "0")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(windowed, "── showing 1 of ") {
		t.Errorf("show did not declare that it returned a window:\n%s", windowed)
	}
	if !strings.Contains(windowed, fixtures.NeedleConversation) {
		t.Errorf("the window does not contain the match:\n%s", windowed)
	}

	full, err := callShow(t, fixtures.SessNeedle, "--full", "--max-bytes", "65536")
	if err != nil {
		t.Fatalf("show --full: %v", err)
	}
	if len(full) <= len(windowed) {
		t.Errorf("--full (%d bytes) returned no more than the window (%d bytes)", len(full), len(windowed))
	}
}

func TestShowResolvesAUniqueSessionPrefix(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callShow(t, fixtures.SessNeedle[:8], "--around", "0")
	if err != nil {
		t.Fatalf("show by prefix: %v", err)
	}
	if !strings.Contains(out, fixtures.SessNeedle) {
		t.Errorf("prefix %q did not resolve to %s:\n%s", fixtures.SessNeedle[:8], fixtures.SessNeedle, out)
	}

	if _, err := callShow(t, "ffffffff-dead"); err == nil {
		t.Error("an unknown session id was accepted")
	} else if got := codeOf(t, err); got != fperr.NotFound {
		t.Errorf("code = %s, want %s", got, fperr.NotFound)
	}
}

// TestDoctorIsCleanOnAHealthyArchive is acceptance case a10, plus the
// version-tolerance property: the unrecognised record types must surface here
// rather than be silently ignored.
func TestDoctorIsCleanOnAHealthyArchive(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callDoctor(t)
	if err != nil {
		t.Fatalf("doctor returned an error on a healthy archive: %v\n%s", err, out)
	}
	if !strings.Contains(out, "integrity  ok") {
		t.Errorf("doctor did not report integrity ok:\n%s", out)
	}
	for typ, n := range c.Manifest.UnknownTypes {
		if !strings.Contains(out, typ) {
			t.Errorf("unknown record type %q (x%d) was not reported:\n%s", typ, n, out)
		}
	}
	if want := c.Manifest.SkewDays; !strings.Contains(out, "skew       ") {
		t.Errorf("doctor did not report the per-file skew (fixtures plant %d days):\n%s", want, out)
	}
	if strings.Contains(out, "promptSource stopped being written") {
		t.Errorf("doctor warned about missing typed labels on a corpus that has %d:\n%s", c.Manifest.TypedTurns, out)
	}
}

// TestDoctorReportsTheSessionsTheArchiveHolds keeps doctor's counts tied to the
// manifest rather than to whatever the code produced.
func TestDoctorReportsTheSessionsTheArchiveHolds(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callDoctor(t, "--json")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := decodeJSON(t, out)
	if n := got["sessions"]; n != float64(len(c.Manifest.Sessions)) {
		t.Errorf("sessions = %v, want %d (fixtures.Manifest.Sessions)", n, len(c.Manifest.Sessions))
	}
	if n := got["files"]; n != float64(c.Manifest.SessionFiles+len(c.Manifest.SubagentDirs)) {
		t.Errorf("files = %v, want %d", n, c.Manifest.SessionFiles+len(c.Manifest.SubagentDirs))
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
}

// TestMineIsExactlyTypedTurns holds --mine to the settled human rule. Widening
// it to content shape is a refuted rule returning by the back door.
func TestMineIsExactlyTypedTurns(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	all, _, err := callFind(t, "wallet", "--all", "--json")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	mine, _, err := callFind(t, "wallet", "--all", "--mine", "--json")
	if err != nil {
		t.Fatalf("find --mine: %v", err)
	}

	allHits := decodeJSON(t, all)["hits"].(float64)
	got := decodeJSON(t, mine)
	if got["hits"].(float64) >= allHits {
		t.Errorf("--mine kept %v of %v hits; the fixture has assistant turns carrying the word too", got["hits"], allHits)
	}
	authors, _ := got["authors"].([]any)
	if len(authors) == 0 {
		t.Fatal("--mine returned no hits at all")
	}
	for _, a := range authors {
		if v := a.(map[string]any)["value"]; v != "human" {
			t.Errorf("--mine returned a hit authored %v", v)
		}
	}
	if !strings.Contains(mine, `"flag":"--mine"`) {
		t.Errorf("--mine narrowed the result without declaring it in the coverage line:\n%s", mine)
	}
}

// TestJSONCarriesWhatTheTextDoes is the rule that a caller never has to parse
// the human form.
func TestJSONCarriesWhatTheTextDoes(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--json")
	if err != nil {
		t.Fatalf("find --json: %v", err)
	}
	got := decodeJSON(t, out)
	cov, ok := got["coverage"].(map[string]any)
	if !ok {
		t.Fatalf("--json has no coverage object:\n%s", out)
	}
	for _, field := range []string{"sessions", "sessions_searched", "searched", "unsearched", "live_from", "content_from", "content_to", "archive_reaches_before_live"} {
		if _, found := cov[field]; !found {
			t.Errorf("coverage.%s is missing from --json", field)
		}
	}
	if cov["live_from"] == cov["content_from"] && cov["live_from"] == "" {
		t.Error("neither boundary was emitted")
	}

	sessions, _ := got["sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatalf("--json returned no sessions:\n%s", out)
	}
	first := sessions[0].(map[string]any)
	for _, field := range []string{"id", "repo", "hits", "turns", "score", "shown"} {
		if _, found := first[field]; !found {
			t.Errorf("sessions[0].%s is missing from --json", field)
		}
	}
	shown, _ := first["shown"].([]any)
	if len(shown) == 0 {
		t.Fatal("sessions[0].shown is empty, so a caller cannot see any matched text")
	}
	if snip, _ := shown[0].(map[string]any)["snippet"].(string); !strings.Contains(snip, fixtures.NeedleConversation) {
		t.Errorf("snippet does not contain the match: %q", snip)
	}
}

// TestFZFStreamIsAddressableByField keeps the interactive contract at the CLI
// boundary: field 1 is the bare session id, records are NUL-terminated, and no
// record's field 1 is anything but a session id.
func TestFZFStreamIsAddressableByField(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--fzf")
	if err != nil {
		t.Fatalf("find --fzf: %v", err)
	}
	if !strings.HasSuffix(out, "\x00") {
		t.Fatalf("the record stream is not NUL-terminated:\n%q", out)
	}
	records := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	found := false
	for i, rec := range records {
		id, _, ok := strings.Cut(rec, "\x1f")
		if !ok {
			t.Fatalf("record %d has no field separator: %q", i, rec)
		}
		if len(id) != len(fixtures.SessNeedle) || strings.ContainsAny(id, " \t\n─") {
			t.Errorf("record %d field 1 is not a bare session id: %q", i, id)
		}
		if id == fixtures.SessNeedle {
			found = true
		}
	}
	if !found {
		t.Errorf("the record stream does not carry session %s:\n%q", fixtures.SessNeedle, out)
	}
	if !strings.Contains(records[len(records)-1], defaultTierDeclaration) {
		t.Errorf("the coverage line did not travel with the records:\n%q", out)
	}
}

func TestFZFAndJSONAreRefusedTogether(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	if _, _, err := callFind(t, fixtures.NeedleConversation, "--fzf", "--json"); err == nil {
		t.Error("two output formats were accepted at once")
	} else if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("code = %s, want %s", got, fperr.ArgError)
	}
}

// TestFlagsAfterTheQueryAreParsed guards the failure mode an agent caller hits
// first: stdlib flag stops at the first non-flag argument, so `find x --all`
// would otherwise search for the two words "x --all" and quietly find nothing.
func TestFlagsAfterTheQueryAreParsed(t *testing.T) {
	c := harness(t)
	t.Chdir(c.ScratchPath(fixtures.ScratchRemoteless))

	out, _, err := callFind(t, fixtures.NeedleConversation, "--all")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(out, fixtures.SessNeedle) {
		t.Errorf("a flag after the query was swallowed into it:\n%s", out)
	}
}

// TestWhenPlacesTheTopicInTime is the third question shape: when did this come
// up, not just where.
func TestWhenPlacesTheTopicInTime(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callWhen(t, fixtures.NeedleConversation, "--all")
	if err != nil {
		t.Fatalf("when: %v", err)
	}
	if !strings.Contains(out, "first ") || !strings.Contains(out, "last ") {
		t.Errorf("when did not report a first and last date:\n%s", out)
	}
	if !strings.Contains(out, "oldest first") {
		t.Errorf("when did not order its sessions chronologically:\n%s", out)
	}
	if !strings.Contains(out, fixtures.SessNeedle) {
		t.Errorf("when did not return session %s:\n%s", fixtures.SessNeedle, out)
	}
}

// TestShowInterleavesTiersByTime is the regression the tier-split archive
// creates: it keeps a file per tier and returns them file by file, so a
// multi-tier read arrives grouped by tier. A passage that printed every reply
// before any of the tool output that produced it would be unreadable, and no
// assertion about content would notice.
func TestShowInterleavesTiersByTime(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callShow(t, fixtures.SessHugeResult, "--full", "--results", "--tools", "--max-bytes", "200000")
	if err != nil {
		t.Fatalf("show: %v", err)
	}

	heading := regexp.MustCompile(`^(?:> |  )(\d{4}-\d{2}-\d{2})  (\S+)(?: \[(\S+)\])?$`)
	var stamps, tiers []string
	for _, line := range strings.Split(out, "\n") {
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		stamps = append(stamps, m[1])
		if m[3] == "" {
			tiers = append(tiers, "conversation")
		} else {
			tiers = append(tiers, m[3])
		}
	}
	if len(stamps) < 3 {
		t.Fatalf("parsed %d turn headings, expected the whole session:\n%s", len(stamps), out)
	}
	for i := 1; i < len(stamps); i++ {
		if stamps[i] < stamps[i-1] {
			t.Errorf("turn %d is dated %s, before turn %d at %s — the tiers came back grouped, not interleaved",
				i, stamps[i], i-1, stamps[i-1])
		}
	}

	var seen []string
	for _, tier := range tiers {
		if len(seen) == 0 || seen[len(seen)-1] != tier {
			seen = append(seen, tier)
		}
	}
	if len(seen) == len(uniq(tiers)) && len(uniq(tiers)) > 1 {
		t.Errorf("each tier appeared in exactly one unbroken run (%v); the output is grouped by tier, not ordered by time", seen)
	}
}

func uniq(vals []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// showSize is the byte size of exactly these arguments at an unbounded cap. The
// text and JSON forms render to different sizes, so a cap derived from one says
// nothing about the other. It picks the cap only; every expectation below comes
// from the contract.
func showSize(t *testing.T, args ...string) int {
	t.Helper()
	out, err := callShow(t, append(args, "--max-bytes", "9999999")...)
	if err != nil {
		t.Fatalf("show at an unbounded cap: %v", err)
	}
	return len(out)
}

func showMatches(t *testing.T, args ...string) int {
	t.Helper()
	out, err := callShow(t, append(args, "--max-bytes", "9999999", "--json")...)
	if err != nil {
		t.Fatalf("show at an unbounded cap: %v", err)
	}
	return int(decodeJSON(t, out)["matches"].(float64))
}

// suppressStatsForCapTest turns off the stats footer for the duration of a
// test. The tests that call this compare byte sizes across two separate
// invocations of the same command; elapsed_ms is a real wall-clock
// measurement that can render to a different width each time, which would
// make the comparison flaky for a reason that has nothing to do with the
// byte-cap arithmetic under test.
func suppressStatsForCapTest(t *testing.T) {
	t.Helper()
	was := statsSuppressed
	statsSuppressed = true
	t.Cleanup(func() { statsSuppressed = was })
}

// TestShowFitsToTheCapAndDeclaresTheCut is the repair for a defect that made a
// question shape docs/requirements.md forbids cutting fail by default: an
// ordinary session at default settings refused outright. Bounded output is
// still absolute — it fits, it does not truncate, and it says what it left out.
func TestShowFitsToTheCapAndDeclaresTheCut(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)
	suppressStatsForCapTest(t)

	matches := showMatches(t, fixtures.SessNeedle, "adapter", "--around", "0")
	full := showSize(t, fixtures.SessNeedle, "adapter", "--around", "0")
	if matches < 2 {
		t.Fatalf("the fixture session carries %d matches for %q; this case needs at least 2", matches, "adapter")
	}
	cap := full - 1

	out, err := callShow(t, fixtures.SessNeedle, "adapter", "--around", "0", "--max-bytes", itoa(cap))
	if err != nil {
		t.Fatalf("show refused at a cap that fits at least one match: %v", err)
	}
	if len(out) > cap {
		t.Errorf("emitted %d bytes over a %d byte cap", len(out), cap)
	}
	if !strings.Contains(out, "matches (--max-bytes)") {
		t.Fatalf("windows were dropped without declaring it:\n%s", out)
	}
	shown, total := parseDeclaredCut(t, out)
	if total != matches {
		t.Errorf("declared a total of %d matches, want %d", total, matches)
	}
	if shown < 1 || shown >= total {
		t.Errorf("declared %d of %d matches shown; want at least one and fewer than all", shown, total)
	}
	if !strings.Contains(out, "adapter") {
		t.Errorf("what survived the fit does not contain the match:\n%s", out)
	}
}

// TestShowFullRefusesRatherThanFitting keeps acceptance case a3's teeth. --full
// is the explicit give-me-everything path; fitting it would turn an answer the
// caller asked for in whole into a silently partial one.
func TestShowFullRefusesRatherThanFitting(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)
	suppressStatsForCapTest(t)

	full := showSize(t, fixtures.SessNeedle, "--full")
	out, err := callShow(t, fixtures.SessNeedle, "--full", "--max-bytes", itoa(full-1))
	if err == nil {
		t.Fatalf("--full fitted itself to the cap instead of refusing, emitting %d bytes", len(out))
	}
	if got := codeOf(t, err); got != fperr.OutputTooLarge {
		t.Errorf("code = %s, want %s", got, fperr.OutputTooLarge)
	}
	if out != "" {
		t.Errorf("wrote %d bytes before refusing", len(out))
	}
}

func TestShowRefusesWhenNotEvenOneMatchFits(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callShow(t, fixtures.SessNeedle, "adapter", "--around", "0", "--max-bytes", "40")
	if err == nil {
		t.Fatalf("emitted %d bytes under a 40 byte cap", len(out))
	}
	if got := codeOf(t, err); got != fperr.OutputTooLarge {
		t.Errorf("code = %s, want %s", got, fperr.OutputTooLarge)
	}
	if out != "" {
		t.Errorf("wrote %d bytes before refusing", len(out))
	}
}

// TestShowJSONFitsToTheSameCap keeps the machine form under the cap the human
// form is measured against: the two render to different sizes, so fitting one
// says nothing about the other.
func TestShowJSONFitsToTheSameCap(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)
	suppressStatsForCapTest(t)

	matches := showMatches(t, fixtures.SessNeedle, "adapter", "--around", "0")
	full := showSize(t, fixtures.SessNeedle, "adapter", "--around", "0", "--json")
	cap := full - 1

	out, err := callShow(t, fixtures.SessNeedle, "adapter", "--around", "0", "--json", "--max-bytes", itoa(cap))
	if err != nil {
		t.Fatalf("show --json refused at a cap that fits at least one match: %v", err)
	}
	if len(out) > cap {
		t.Errorf("emitted %d bytes over a %d byte cap", len(out), cap)
	}
	got := decodeJSON(t, out)
	fitted := int(got["fitted"].(float64))
	if fitted < 1 || fitted >= matches {
		t.Errorf("fitted = %d of %d matches; want at least one and fewer than all", fitted, matches)
	}
	limits, _ := got["coverage"].(map[string]any)["limits"].([]any)
	var declared bool
	for _, l := range limits {
		if l.(map[string]any)["flag"] == "--max-bytes" {
			declared = true
		}
	}
	if !declared {
		t.Errorf("--json dropped windows without declaring the cut:\n%s", out)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// parseDeclaredCut reads the `showing N of M matches (--max-bytes)` line back
// out of the human form.
func parseDeclaredCut(t *testing.T, out string) (shown, total int) {
	t.Helper()
	m := regexp.MustCompile(`── showing (\d+) of (\d+) matches \(--max-bytes\)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no declared cut in:\n%s", out)
	}
	shown, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	return shown, total
}

// twoWindows is a show with two separately-fitting windows. The CLI cannot
// produce that shape for --full today, which is exactly why this is a unit
// test: the rule "--full refuses, it never fits" has to be checked somewhere a
// change to windowViews cannot quietly satisfy.
func twoWindows() (render.Show, []render.Window) {
	body := strings.Repeat("x", 400)
	win := func(i int) render.Window {
		return render.Window{From: i, To: i, Turns: []render.Turn{
			{Index: i, TS: "2026-08-01T00:00:0" + itoa(i) + "Z", Tier: schema.TierConversation,
				Author: schema.AuthorAssistant, Match: true, Text: body},
		}}
	}
	return render.Show{Verb: "show", Session: "s", Turns: 9, Matches: 2}, []render.Window{win(0), win(1)}
}

func TestFitShowNeverFitsFull(t *testing.T) {
	view, windows := twoWindows()
	view.Full = true

	body, err := (&corpus{}).fitShow(&view, windows, scan.Result{}, &Globals{MaxBytes: 900}, true)
	if err != nil {
		t.Fatalf("fitShow: %v", err)
	}
	if int64(len(body)) <= 900 {
		t.Fatalf("--full fitted itself to the 900 byte cap (%d); it must render whole and let the cap refuse", len(body))
	}
	if len(view.Windows) != len(windows) {
		t.Errorf("--full kept %d of %d windows; it must keep all of them", len(view.Windows), len(windows))
	}
	if strings.Contains(string(body), "--max-bytes") {
		t.Errorf("--full declared a cut it must never make:\n%s", body)
	}
}

func TestFitShowFitsAQuery(t *testing.T) {
	view, windows := twoWindows()

	body, err := (&corpus{}).fitShow(&view, windows, scan.Result{}, &Globals{MaxBytes: 900}, false)
	if err != nil {
		t.Fatalf("fitShow: %v", err)
	}
	if int64(len(body)) > 900 {
		t.Fatalf("fitted answer is %d bytes over a 900 byte cap", len(body))
	}
	if len(view.Windows) != 1 {
		t.Errorf("kept %d windows, want 1 to fit", len(view.Windows))
	}
	if want := "── showing 1 of 2 matches (--max-bytes)"; !strings.Contains(string(body), want) {
		t.Errorf("output is missing %q:\n%s", want, body)
	}
}

// TestDoctorDetectsACorruptMetaFile is the case that used to pass silently.
// meta.json carries the format version, the per-tier checksums and both
// coverage boundaries, so a corrupt one must misstate coverage rather than
// fail to load — once the raw transcripts age out there is nothing left to
// reconcile against.
func TestDoctorDetectsACorruptMetaFile(t *testing.T) {
	_, home := harnessAt(t)

	if _, err := callDoctor(t); err != nil {
		t.Fatalf("first doctor: %v", err)
	}
	corrupt(t, filepath.Join(home, "meta.json"))

	out, err := callDoctor(t)
	if err == nil {
		t.Fatalf("a corrupt meta.json reported clean:\n%s", out)
	}
	if got := codeOf(t, err); got != fperr.BadArchive {
		t.Errorf("code = %s, want %s", got, fperr.BadArchive)
	}
	if !strings.Contains(out, "meta.json") {
		t.Errorf("the report does not name the file that failed:\n%s", out)
	}
	if !strings.Contains(out, "as found FAILED") {
		t.Errorf("the verdict does not distinguish the store as found from the store after the refresh:\n%s", out)
	}
	if strings.Contains(out, "integrity  ok") {
		t.Errorf("a corrupt store still printed `integrity ok`:\n%s", out)
	}
}

func TestDoctorDetectsACorruptCursor(t *testing.T) {
	_, home := harnessAt(t)

	if _, err := callDoctor(t); err != nil {
		t.Fatalf("first doctor: %v", err)
	}
	corrupt(t, filepath.Join(home, "cursor"))

	out, err := callDoctor(t)
	if err == nil {
		t.Fatalf("a corrupt cursor reported clean:\n%s", out)
	}
	if !strings.Contains(out, "cursor") {
		t.Errorf("the report does not name the file that failed:\n%s", out)
	}
}

// TestDoctorVerdictRequiresEveryComponent holds the rule that the aggregate is
// not enough: meta.json carries the tier checksums, so a verdict that trusted
// only the tier comparison would be checking a corrupt file against itself.
func TestDoctorVerdictRequiresEveryComponent(t *testing.T) {
	base := archive.Report{
		OK: true, MetaOK: true, CursorOK: true,
		Tiers: []archive.TierReport{{Tier: schema.TierConversation, Checksum: "a", Expected: "a"}},
	}
	if !intact(base) {
		t.Fatal("a report with every component sound was called corrupt")
	}
	for _, tc := range []struct {
		name   string
		break_ func(*archive.Report)
	}{
		{"aggregate", func(r *archive.Report) { r.OK = false }},
		{"meta.json", func(r *archive.Report) { r.MetaOK = false }},
		{"cursor", func(r *archive.Report) { r.CursorOK = false }},
		{"a tier", func(r *archive.Report) { r.Tiers[0].Checksum = "b" }},
		{"no tiers at all", func(r *archive.Report) { r.Tiers = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := base
			rep.Tiers = append([]archive.TierReport(nil), base.Tiers...)
			tc.break_(&rep)
			if intact(rep) {
				t.Errorf("a store with a broken %s was reported intact", tc.name)
			}
		})
	}
}

// TestDoctorReportsCollapsedRecords keeps the deduplication visible. rank
// reports `redundant: 0` because ingest already collapsed the copies, which
// reads as though no deduplication happened at all.
func TestDoctorReportsCollapsedRecords(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	out, err := callDoctor(t, "--json")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := decodeJSON(t, out)
	collapsed := int(got["collapsed"].(float64))
	if want := len(c.Manifest.DupUUIDs); collapsed < want {
		t.Errorf("collapsed = %d, want at least %d (fixtures.Manifest.DupUUIDs are carried by two files each)", collapsed, want)
	}

	text, err := callDoctor(t)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(text, "records collapsed on (session, uuid)") {
		t.Errorf("the text form does not report deduplication:\n%s", text)
	}
}

func corrupt(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(b) < 8 {
		t.Fatalf("%s is %d bytes, too small to corrupt meaningfully", path, len(b))
	}
	copy(b[4:8], "\x00\x00\x00\x00")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestDoctorDoesNotAlarmOnAStoreThatPredatesTheChecksums keeps a format upgrade
// from reading as corruption. A store written before the sidecar existed has
// nothing to check against, and a false alarm here is how the real one at
// TestDoctorDetectsACorruptMetaFile gets ignored.
// TestEveryVerbIsReachableThroughTheRealDispatcher is the one place these
// verbs run through their actual registered entry point (run -> the
// registry's closure) rather than being called directly, which is how every
// other test in this file reaches them. A verb registered but never wired to
// its own closure would pass every other test here and still be broken from
// the command line. The closure writes to the process's real os.Stdout, not
// the buffer run() takes for its own usage/error output, so only the exit
// code is observable here — the content itself is covered by every direct
// call to find/show/when/turns/doctor/guide elsewhere in this file.
func TestEveryVerbIsReachableThroughTheRealDispatcher(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	for _, tc := range []struct {
		verb string
		args []string
	}{
		{"find", []string{"find", fixtures.NeedleConversation, "--all"}},
		{"show", []string{"show", fixtures.SessNeedle}},
		{"when", []string{"when", fixtures.NeedleConversation, "--all"}},
		{"turns", []string{"turns", fixtures.NeedleConversation, "--all"}},
		{"doctor", []string{"doctor"}},
		{"guide", []string{"guide"}},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tc.args, &out, &errOut)
			if code != 0 {
				t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
			}
		})
	}
}

func TestDoctorDoesNotAlarmOnAStoreThatPredatesTheChecksums(t *testing.T) {
	_, home := harnessAt(t)

	if _, err := callDoctor(t); err != nil {
		t.Fatalf("first doctor: %v", err)
	}
	if err := os.Remove(filepath.Join(home, "checksums")); err != nil {
		t.Fatalf("remove the sidecar: %v", err)
	}

	out, err := callDoctor(t)
	if err != nil {
		t.Fatalf("a store with no checksums yet was reported corrupt: %v\n%s", err, out)
	}
	if !strings.Contains(out, "integrity  ok") {
		t.Errorf("verdict is not ok:\n%s", out)
	}
	if !strings.Contains(out, "predates the integrity checksums") {
		t.Errorf("the report does not say why it could not check the store as found:\n%s", out)
	}
}
