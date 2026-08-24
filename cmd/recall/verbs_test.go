package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/render"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/scan"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// The coverage declaration every searching verb emits by default. The wording is
// pinned: no-false-negatives holds only over the tier searched, and this says which.
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
	out, _, err := callDoctorFull(t, args...)
	return out, err
}

func callDoctorFull(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = doctor(args, &out, &errOut)
	return out.String(), errOut.String(), err
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

// TestShowFitsToTheCapAndDeclaresTheCut is the repair for a defect that made one
// of the four question shapes — recover a conclusion and its reasoning — fail by
// default: an ordinary session at default settings refused outright. Bounded
// output is still absolute — it fits, it does not truncate, and it says what it
// left out.
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

// pinClaudeCodeSelection resets archive's process-global agent selection to
// claude-code before and after the test. The package exposes no way to
// unset it, and once anything sets it explicitly, archive.Select stops
// consulting RECALL_AGENT at all — so a selection one test leaves behind
// would otherwise silently decide what corpus a later, unrelated test reads.
func pinClaudeCodeSelection(t *testing.T) {
	t.Helper()
	reset := func() {
		if err := archive.SetSelection(string(schema.AgentClaudeCode)); err != nil {
			t.Fatalf("archive.SetSelection: %v", err)
		}
	}
	reset()
	t.Cleanup(reset)
}

// pinCodexSelection is pinClaudeCodeSelection's sibling for tests that need
// the process-global selection to start somewhere other than claude-code, so
// the pin cannot accidentally supply the answer an assertion is checking. It
// still restores claude-code on cleanup, matching every other test's
// expectation of where the process starts.
func pinCodexSelection(t *testing.T) {
	t.Helper()
	if err := archive.SetSelection(string(schema.AgentCodex)); err != nil {
		t.Fatalf("archive.SetSelection: %v", err)
	}
	t.Cleanup(func() {
		if err := archive.SetSelection(string(schema.AgentClaudeCode)); err != nil {
			t.Fatalf("archive.SetSelection: %v", err)
		}
	})
}

func TestClaudeCodeAndCodexProvidersAreRegisteredAtStartup(t *testing.T) {
	var names []string
	for _, p := range archive.Registered() {
		names = append(names, string(p.Agent()))
	}
	if got := strings.Join(names, ","); got != "claude-code,codex" {
		t.Errorf("registered agents = %q, want %q", got, "claude-code,codex")
	}
}

// directDoctorText builds the whole-corpus doctor report from a single store
// opened directly, with no selection and no per-agent block labelling. It
// shares warningsOf, intact, exists and plural with the current code, so the
// only thing that differs between the two is the machinery under test.
func directDoctorText(t *testing.T, dir string) []byte {
	t.Helper()
	provider := strip.ClaudeCode()
	store, err := archive.Open(archive.Options{
		Dir:      dir,
		Provider: provider,
		Resolve:  repo.New().Repo,
		Force:    true,
	})
	if err != nil {
		t.Fatalf("direct archive.Open: %v", err)
	}

	var found archive.Report
	checked, upgraded := false, false
	switch {
	case !exists(store.MetaPath()):
	case !exists(store.ChecksumsPath()):
		upgraded = true
	default:
		found, err = store.Verify()
		if err != nil {
			t.Fatalf("direct Verify (found): %v", err)
		}
		checked = true
	}

	res, err := store.Update()
	if err != nil {
		t.Fatalf("direct Update: %v", err)
	}
	rep, err := store.Verify()
	if err != nil {
		t.Fatalf("direct Verify: %v", err)
	}
	obs := provider.Observation()

	view := render.Doctor{
		Verb:     "doctor",
		Dir:      rep.Dir,
		Root:     store.Root(),
		OK:       intact(rep) && (!checked || intact(found)),
		FoundOK:  intact(found),
		AfterOK:  intact(rep),
		Checked:  checked,
		MetaOK:   rep.MetaOK,
		CursorOK: rep.CursorOK,
		Turns:    rep.Turns,
		Sessions: rep.Sessions,

		LiveFrom:    render.Day(rep.Coverage.LiveFrom),
		ContentFrom: render.Day(rep.Coverage.ContentFrom),
		ContentTo:   render.Day(rep.Coverage.ContentTo),
		SkewDays:    rep.Coverage.MaxFileSkewDays(),
		SkewFile:    rep.Coverage.MaxSkewFile,

		Files:      res.FilesSeen,
		Vanished:   res.Vanished,
		Unreadable: res.Unreadable,

		Lines:        res.Tally.Lines,
		Malformed:    res.Tally.Malformed,
		Untyped:      res.Tally.Untyped,
		UnknownTotal: res.Tally.UnknownTotal(),
		Collapsed:    res.Collapsed,

		HumanShaped:        obs.HumanShapedMain,
		Typed:              obs.Typed,
		CommandArgs:        obs.CommandArgs,
		TypedLabelsMissing: obs.TypedLabelsMissing(),

		Problems: rep.Problems,
	}
	for _, tt := range rep.Tiers {
		view.Bytes += tt.Bytes
		view.Tiers = append(view.Tiers, render.TierIntegrity{
			Tier: string(tt.Tier), Path: tt.Path, OK: tt.Checksum == tt.Expected,
			Bytes: tt.Bytes, Turns: tt.Turns, Checksum: tt.Checksum,
		})
	}
	for _, u := range res.Tally.UnknownCounts() {
		view.UnknownTypes = append(view.UnknownTypes, render.TypeCount{Type: u.Type, Count: u.Count})
	}
	if checked && !intact(found) {
		for _, p := range found.Problems {
			view.Problems = append([]string{"as found, before this run refreshed the store: " + p}, view.Problems...)
		}
	}
	view.Warnings = warningsOf(view)
	if upgraded {
		view.Warnings = append(view.Warnings,
			"this store predates the integrity checksums, so it could not be checked as found; this run wrote them")
	}
	if checked && !intact(found) && intact(rep) {
		view.Warnings = append(view.Warnings,
			"this run rebuilt the store from the transcripts, so a second `recall doctor` will read clean; "+
				"anything the raw files no longer cover was already lost before it ran")
	}
	return view.Text()
}

// TestDoctorDefaultSelectionOutputIsUnchanged proves that routing a
// single-agent default run through the selection machinery prints the same
// bytes as opening that one store directly, rather than trust a hardcoded
// golden value: the fixture's remoteless-repo identity embeds the scratch
// directory's own path, so a tier's byte size is not stable across separate
// test runs and can only be compared within one, against the same
// materialized corpus.
func TestDoctorDefaultSelectionOutputIsUnchanged(t *testing.T) {
	pinClaudeCodeSelection(t)
	c, home := harnessAt(t)
	t.Chdir(c.Scratch)

	directDir := filepath.Join(home, "direct")
	want := directDoctorText(t, directDir)

	out, err := callDoctor(t)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	norm := strings.NewReplacer(directDir, "<ARCHIVE>", home, "<ARCHIVE>")
	got := norm.Replace(out)
	wantNorm := norm.Replace(string(want))
	if got != wantNorm {
		t.Errorf("doctor's default-selection output no longer matches a directly-opened store on the same corpus:\n--- got ---\n%s\n--- want ---\n%s", got, wantNorm)
	}
	if strings.Contains(out, "agent      ") {
		t.Errorf("a single-agent default run printed a block label it never used to:\n%s", out)
	}
}

// TestDoctorObservationCountsDoNotAccumulateAcrossRepeatedRunsInOneProcess
// pins freshProviderFor's reason for existing: it builds a provider with no
// carried-over state, rather than reaching for the registry's process-lifetime
// singleton, which would let a second doctor() call in the same process add
// its counts on top of the first's instead of reporting the corpus fresh.
func TestDoctorObservationCountsDoNotAccumulateAcrossRepeatedRunsInOneProcess(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)

	first, err := callDoctor(t, "--json")
	if err != nil {
		t.Fatalf("first doctor: %v", err)
	}
	second, err := callDoctor(t, "--json")
	if err != nil {
		t.Fatalf("second doctor: %v", err)
	}
	firstGot, secondGot := decodeJSON(t, first), decodeJSON(t, second)
	for _, field := range []string{"human_shaped", "typed", "command_args"} {
		if firstGot[field] != secondGot[field] {
			t.Errorf("%s = %v on the second doctor() call in this process, want %v (the first run's count)",
				field, secondGot[field], firstGot[field])
		}
	}
}

// TestDoctorRefusesAnUnregisteredProvider is EARS: WHEN --provider names an
// agent with no registered provider THE SYSTEM SHALL exit 2 with an argument
// error naming the registered agents.
func TestDoctorRefusesAnUnregisteredProvider(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)

	var out, errOut bytes.Buffer
	code := run([]string{"doctor", "--provider", "no-such-agent"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"claude-code", "codex"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr does not name registered agent %q:\n%s", want, errOut.String())
		}
	}
	if out.String() != "" {
		t.Errorf("a refused --provider still printed output: %q", out.String())
	}
}

// TestProviderFlagWinsOverRecallAgentWhenTheyDisagree pins the process
// selection to codex — the opposite of what --provider is about to ask for —
// before also setting RECALL_AGENT to codex and passing --provider
// claude-code. Neither the leftover pin nor the env can supply the right
// answer by accident here: only Check() actually wiring --provider into
// archive.SetSelection makes the resolved root the claude-code one.
func TestProviderFlagWinsOverRecallAgentWhenTheyDisagree(t *testing.T) {
	pinCodexSelection(t)
	c, _ := harnessAt(t)
	fixtures.MaterializeCodex(t)
	t.Setenv("RECALL_AGENT", "codex")

	out, err := callDoctor(t, "--provider", "claude-code", "--json")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := decodeJSON(t, out)
	root, _ := got["root"].(string)
	if root != c.Root {
		t.Errorf("root = %q, want the claude-code corpus %q — RECALL_AGENT=codex and the pinned codex selection should both have lost to --provider claude-code", root, c.Root)
	}
}

// TestNoProviderFlagLeavesAnExistingSelectionInPlace is the companion to the
// case above, and the reason it is not vacuous: an implementation that always
// writes some fixed provider regardless of what --provider actually holds
// would still pass the case above, because there --provider's value happens
// to equal the fixed one. Here no --provider is given at all, so Provider
// stays the "auto" zero value — Check() must not call archive.SetSelection in
// that case, or it clobbers a selection RECALL_AGENT alone already put in
// place with the auto default, which is exactly what auto is supposed to
// mean: let the environment's answer stand.
func TestNoProviderFlagLeavesAnExistingSelectionInPlace(t *testing.T) {
	pinCodexSelection(t)
	codex := fixtures.MaterializeCodex(t)
	t.Setenv("RECALL_AGENT", "codex")

	out, err := callDoctor(t, "--json")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	got := decodeJSON(t, out)
	root, _ := got["root"].(string)
	wantRoot := filepath.Join(codex.Root, "sessions")
	if root != wantRoot {
		t.Errorf("root = %q, want the codex corpus %q — with no --provider flag, RECALL_AGENT's codex selection should have been left alone", root, wantRoot)
	}
}

// codexCompressedCount is the number of rollouts fixtures.MaterializeCodex
// plants that a Codex reader must skip as opaque, read from the manifest
// rather than typed in — the fixture is free to grow more of them and the
// warning's count has to track that.
func codexCompressedCount(m fixtures.CodexManifest) int {
	var n int
	for _, r := range m.Rows {
		if r.Quirk == fixtures.CodexQuirkZstdOpaque {
			n++
		}
	}
	return n
}

// blockRoots pulls every "corpus     <root> ..." value out of a doctor text
// report, in the order the blocks were printed.
func blockRoots(out string) []string {
	var roots []string
	for _, m := range regexp.MustCompile(`(?m)^corpus     (\S+)`).FindAllStringSubmatch(out, -1) {
		roots = append(roots, m[1])
	}
	return roots
}

// plantExtraCompressedRollout adds a second opaque .jsonl.zst file beside the
// one fixtures.MaterializeCodex already plants, so a test asserting the
// compressed count is fixture-derived is not proving it against a fixture
// that happens to hold exactly one either way. Its content is never read —
// the Codex reader counts and skips a rollout by name alone.
func plantExtraCompressedRollout(t *testing.T, codex fixtures.CodexCorpus) {
	t.Helper()
	for _, r := range codex.Manifest.Rows {
		if r.Quirk != fixtures.CodexQuirkZstdOpaque {
			continue
		}
		extra := strings.Replace(codex.Path(r.File), ".jsonl.zst", "-extra.jsonl.zst", 1)
		if err := os.WriteFile(extra, []byte("opaque"), 0o644); err != nil {
			t.Fatalf("plant an extra compressed rollout: %v", err)
		}
		return
	}
	t.Fatal("fixtures.MaterializeCodex no longer plants a zstd-opaque row")
}

// TestDoctorProviderAllPrintsOneBlockPerRegisteredProviderNamingEachAgent
// materializes both corpora so the multi-block case is actually exercised: a
// run with only one provider registered proves nothing about labelling. It
// also proves the two blocks are two distinct stores rather than the same
// block printed twice: strings.Count on a fixed label counts lines, not
// values, and would pass even if freshProviderFor opened the same corpus for
// both agents.
func TestDoctorProviderAllPrintsOneBlockPerRegisteredProviderNamingEachAgent(t *testing.T) {
	pinClaudeCodeSelection(t)
	c := harness(t)
	codex := fixtures.MaterializeCodex(t)
	plantExtraCompressedRollout(t, codex)

	out, err := callDoctor(t, "--provider", "all")
	if err != nil {
		t.Fatalf("doctor --provider all: %v", err)
	}

	claudeAt := strings.Index(out, "agent      claude-code\n")
	codexAt := strings.Index(out, "agent      codex\n")
	if claudeAt < 0 || codexAt < 0 {
		t.Fatalf("doctor --provider all did not label both blocks:\n%s", out)
	}
	if claudeAt > codexAt {
		t.Errorf("blocks are not in registered order (claude-code before codex):\n%s", out)
	}
	if got := strings.Count(out, "archive    "); got != 2 {
		t.Errorf("doctor --provider all printed %d integrity blocks, want 2:\n%s", got, out)
	}

	roots := blockRoots(out)
	if len(roots) != 2 {
		t.Fatalf("doctor --provider all printed %d corpus lines, want 2:\n%s", len(roots), out)
	}
	claudeRoot, codexRoot := roots[0], roots[1]
	if claudeRoot == codexRoot {
		t.Errorf("both blocks report the same root %q — the codex block did not open its own store:\n%s", claudeRoot, out)
	}
	if claudeRoot != c.Root {
		t.Errorf("claude-code block root = %q, want %q", claudeRoot, c.Root)
	}
	wantCodexRoot := filepath.Join(codex.Root, "sessions")
	if codexRoot != wantCodexRoot {
		t.Errorf("codex block root = %q, want %q", codexRoot, wantCodexRoot)
	}

	wantCompressed := codexCompressedCount(codex.Manifest) + 1 // +1 for plantExtraCompressedRollout
	wantWarning := strconv.Itoa(wantCompressed) + " rollout(s) have been compressed to .jsonl.zst and were not read"
	if !strings.Contains(out, wantWarning) {
		t.Errorf("the codex block did not warn about the %d compressed rollout(s) the fixture plants:\n%s", wantCompressed, out)
	}
}

// TestDoctorDoesNotWarnAboutCompressedRolloutsWhenThereAreNone proves the
// warning line is conditional on the fixture-derived count above, not always
// present: with the one compressed rollout removed before doctor ever reads
// the corpus, there is nothing to warn about.
func TestDoctorDoesNotWarnAboutCompressedRolloutsWhenThereAreNone(t *testing.T) {
	pinCodexSelection(t)
	codex := fixtures.MaterializeCodex(t)

	var zstdRow fixtures.CodexRow
	found := false
	for _, r := range codex.Manifest.Rows {
		if r.Quirk == fixtures.CodexQuirkZstdOpaque {
			zstdRow, found = r, true
			break
		}
	}
	if !found {
		t.Fatal("fixtures.MaterializeCodex no longer plants a zstd-opaque row")
	}
	if err := os.Remove(codex.Path(zstdRow.File)); err != nil {
		t.Fatalf("remove the compressed rollout: %v", err)
	}

	out, err := callDoctor(t)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if strings.Contains(out, "have been compressed to .jsonl.zst") {
		t.Errorf("doctor warned about compressed rollouts on a corpus with none:\n%s", out)
	}
}

// TestDoctorProviderAllPrintsOneBlockWhenOnlyOneAgentRootExists is criterion
// 4's partial case: claude-code's root exists, codex's does not, so
// Select("all") names only claude-code and there is exactly one block, not a
// refusal — the all-present and none-present cases already existed, but
// neither proves the loop over sel.Agents handles a length-1 selection.
func TestDoctorProviderAllPrintsOneBlockWhenOnlyOneAgentRootExists(t *testing.T) {
	pinClaudeCodeSelection(t)
	c := harness(t)
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-codex"))

	out, err := callDoctor(t, "--provider", "all")
	if err != nil {
		t.Fatalf("doctor --provider all: %v", err)
	}
	if strings.Contains(out, "agent      ") {
		t.Errorf("a single-agent --provider all run printed a block label it should only use when there is more than one block:\n%s", out)
	}
	if got := strings.Count(out, "archive    "); got != 1 {
		t.Errorf("doctor --provider all with only claude-code's root present printed %d integrity blocks, want 1:\n%s", got, out)
	}
	roots := blockRoots(out)
	if len(roots) != 1 || roots[0] != c.Root {
		t.Errorf("corpus roots = %v, want exactly [%q]:\n%s", roots, c.Root, out)
	}
}

// TestDoctorProviderAllRefusesWhenNoAgentRootExists is fact 6: Select("all")
// returns an empty, error-free Selection when no agent root exists, and
// doctor has to turn that into a refusal itself rather than a silent,
// empty-looking success.
func TestDoctorProviderAllRefusesWhenNoAgentRootExists(t *testing.T) {
	pinClaudeCodeSelection(t)
	base := t.TempDir()
	t.Setenv("RECALL_HOME", filepath.Join(base, "home"))
	t.Setenv("CLAUDE_PROJECTS_DIR", filepath.Join(base, "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(base, "missing-codex"))

	out, err := callDoctor(t, "--provider", "all")
	if err == nil {
		t.Fatalf("doctor --provider all with no agent root succeeded:\n%s", out)
	}
	if got := codeOf(t, err); got != fperr.CorpusUnreadable {
		t.Errorf("code = %s, want %s", got, fperr.CorpusUnreadable)
	}
	if out != "" {
		t.Errorf("a refused selection still printed output: %q", out)
	}
}

// captureRealStdout intercepts the process's real os.Stdout for the
// duration of the test. find, turns, when and show are registered against
// os.Stdout directly (see cmd_find.go and friends), not against the writer
// run() itself takes, so proving one of them printed nothing on a refusal has
// to watch the real stream — the buffer passed to run() only ever carries
// run()'s own usage, version and error text.
//
// It returns a stop func rather than the buffer itself: the copier goroutine
// keeps writing to the buffer until the pipe's write end is closed, so a
// caller reading the buffer directly would race that goroutine. stop closes
// the write end, waits for the copier to finish, and only then reads the
// buffer. It is safe to call more than once — t.Cleanup also calls it, so a
// test that calls it explicitly must not double-close the pipe.
func captureRealStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	captured := &bytes.Buffer{}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(captured, r)
		close(done)
	}()
	var once sync.Once
	stop := func() string {
		once.Do(func() {
			os.Stdout = orig
			_ = w.Close()
			<-done
			_ = r.Close()
		})
		return captured.String()
	}
	t.Cleanup(func() { stop() })
	return stop
}

// answeredSessionIDs is the session ids a --json answer actually returned. A
// session id is never echoed back from the query the way a planted token is,
// so it tells a corpus that answered from one that was merely asked.
func answeredSessionIDs(t *testing.T, blob string) []string {
	t.Helper()
	var ids []string
	for _, s := range answeredSessions(t, blob) {
		id, _ := s["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// answeredSnippets is every snippet the answer carries. The coverage line
// echoes the query's own terms, so a substring test over the whole output
// would find a planted token in a search that returned nothing at all.
func answeredSnippets(t *testing.T, blob string) []string {
	t.Helper()
	var out []string
	for _, s := range answeredSessions(t, blob) {
		shown, _ := s["shown"].([]any)
		for _, h := range shown {
			hit, _ := h.(map[string]any)
			snippet, _ := hit["snippet"].(string)
			out = append(out, snippet)
		}
	}
	return out
}

func answeredSessions(t *testing.T, blob string) []map[string]any {
	t.Helper()
	sessions, _ := decodeJSON(t, blob)["sessions"].([]any)
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		session, _ := s.(map[string]any)
		out = append(out, session)
	}
	return out
}

func anyContains(vals []string, want string) bool {
	for _, v := range vals {
		if strings.Contains(v, want) {
			return true
		}
	}
	return false
}

// codexNeedleThread is the thread id of the rollout carrying
// fixtures.NeedleCodexConversation, read from the manifest rather than typed
// in, so the fixture is free to renumber its threads.
func codexNeedleThread(t *testing.T, codex fixtures.CodexCorpus) string {
	t.Helper()
	for _, r := range codex.Manifest.Rows {
		if r.Quirk == fixtures.CodexQuirkPlain {
			return r.ThreadID
		}
	}
	t.Fatal("fixtures.MaterializeCodex no longer plants the plain rollout that carries the needle")
	return ""
}

// TestFindWithProviderCodexAnswersFromCodexAndNotFromClaudeCode is the
// selection reaching a searching verb at all: the token planted only in the
// Codex corpus comes back, and the token planted only in the claude-code
// corpus does not, from the same run's selection. The process selection is
// pinned to claude-code first — the opposite of what the run asks for — so
// neither the pin nor a leftover from an earlier test can supply the answer.
func TestFindWithProviderCodexAnswersFromCodexAndNotFromClaudeCode(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)
	codex := fixtures.MaterializeCodex(t)

	out, _, err := callFind(t, fixtures.NeedleCodexConversation, "--all", "--provider", "codex", "--json")
	if err != nil {
		t.Fatalf("find --provider codex: %v", err)
	}
	if snippets := answeredSnippets(t, out); !anyContains(snippets, fixtures.NeedleCodexConversation) {
		t.Errorf("no returned snippet carries the Codex corpus's planted token %q:\n%s",
			fixtures.NeedleCodexConversation, out)
	}
	if want, ids := codexNeedleThread(t, codex), answeredSessionIDs(t, out); !slices.Contains(ids, want) {
		t.Errorf("answered sessions = %v, want the Codex rollout %q among them", ids, want)
	}

	// The negative control. The claude-code corpus is materialized and its
	// token really is there to be found; a run that read it under --provider
	// codex would answer this.
	elsewhere, _, err := callFind(t, fixtures.NeedleMultiTierText, "--all", "--provider", "codex", "--json")
	if err != nil {
		t.Fatalf("find --provider codex for a claude-code token: %v", err)
	}
	if ids := answeredSessionIDs(t, elsewhere); len(ids) != 0 {
		t.Errorf("--provider codex answered %v for %q, a token planted only in the claude-code corpus",
			ids, fixtures.NeedleMultiTierText)
	}
}

// TestTurnsAndWhenReadTheSelectedCorpusToo keeps the selection from being one
// verb's property: every searching verb opens the same corpus, so each has to
// answer from the Codex store and name no claude-code session while doing it.
func TestTurnsAndWhenReadTheSelectedCorpusToo(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)
	codex := fixtures.MaterializeCodex(t)
	thread := codexNeedleThread(t, codex)

	verbs := map[string]func(t *testing.T, args ...string) (string, error){
		"turns": callTurns,
		"when":  callWhen,
	}
	for name, call := range verbs {
		t.Run(name, func(t *testing.T) {
			out, err := call(t, fixtures.NeedleCodexConversation, "--all", "--provider", "codex")
			if err != nil {
				t.Fatalf("%s --provider codex: %v", name, err)
			}
			if !strings.Contains(out, thread) {
				t.Errorf("%s --provider codex does not name the Codex rollout %q that carries the token:\n%s",
					name, thread, out)
			}
			if strings.Contains(out, fixtures.SessNeedle) {
				t.Errorf("%s --provider codex named the claude-code session %q:\n%s", name, fixtures.SessNeedle, out)
			}
		})
	}
}

// TestShowResolvesASessionInTheSelectedCorpusOnly is show's shape of the same
// rule: it takes a session id rather than a query, so the selection decides
// which ids exist at all. The claude-code session really is on this machine,
// which is what makes the second half a control rather than a tautology.
func TestShowResolvesASessionInTheSelectedCorpusOnly(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)
	codex := fixtures.MaterializeCodex(t)
	thread := codexNeedleThread(t, codex)

	out, err := callShow(t, thread, "--provider", "codex")
	if err != nil {
		t.Fatalf("show a Codex session under --provider codex: %v", err)
	}
	if !strings.Contains(out, fixtures.NeedleCodexConversation) {
		t.Errorf("show did not return the Codex rollout's planted token:\n%s", out)
	}

	if _, err := callShow(t, fixtures.SessNeedle, "--provider", "codex"); err == nil {
		t.Fatalf("show resolved the claude-code session %q under --provider codex", fixtures.SessNeedle)
	} else if got := codeOf(t, err); got != fperr.NotFound {
		t.Errorf("code = %s, want %s", got, fperr.NotFound)
	}
}

// TestFindWithProviderAllAnswersFromBothCorporaInOneSearch is the case a
// group exists for. One query carries both corpora's planted tokens; a run
// reading either store alone can return only one of them.
func TestFindWithProviderAllAnswersFromBothCorporaInOneSearch(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)
	codex := fixtures.MaterializeCodex(t)

	query := fixtures.NeedleMultiTierText + " " + fixtures.NeedleCodexConversation
	out, _, err := callFind(t, query, "--all", "--provider", "all", "--json")
	if err != nil {
		t.Fatalf("find --provider all: %v", err)
	}
	snippets := answeredSnippets(t, out)
	for _, want := range []string{fixtures.NeedleMultiTierText, fixtures.NeedleCodexConversation} {
		if !anyContains(snippets, want) {
			t.Errorf("no returned snippet carries %q; one search over both corpora must return both tokens:\n%s", want, out)
		}
	}
	ids := answeredSessionIDs(t, out)
	for _, want := range []string{fixtures.SessNeedle, codexNeedleThread(t, codex)} {
		if !slices.Contains(ids, want) {
			t.Errorf("answered sessions = %v, want %q among them", ids, want)
		}
	}
}

// TestSearchingVerbsRefuseASelectionNamingNoAgent is --provider all on a
// machine carrying neither agent's session store. A group of no stores would
// answer every query with no matches, which a caller cannot tell from a
// search that ran — so this fails as an unreadable corpus and prints nothing
// on stdout.
func TestSearchingVerbsRefuseASelectionNamingNoAgent(t *testing.T) {
	pinClaudeCodeSelection(t)
	base := t.TempDir()
	t.Setenv("RECALL_HOME", filepath.Join(base, "home"))
	t.Setenv("CLAUDE_PROJECTS_DIR", filepath.Join(base, "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(base, "missing-codex"))

	stopCapture := captureRealStdout(t)
	var out, errOut bytes.Buffer
	code := run([]string{"find", fixtures.NeedleConversation, "--all", "--provider", "all"}, &out, &errOut)
	if code != 3 {
		t.Fatalf("exit %d, want 3 (an unreadable corpus, not the exit 1 of a search that matched nothing)\nstderr: %s",
			code, errOut.String())
	}
	if stdout := stopCapture(); stdout != "" {
		t.Errorf("a refused selection still printed an answer:\n%s", stdout)
	}
	if !strings.Contains(errOut.String(), "session store") {
		t.Errorf("stderr does not name the reason the selection was empty:\n%s", errOut.String())
	}
}

// TestSearchingVerbsRefuseAnAgentWithNoRegisteredProvider keeps an unknown
// agent from being answered out of whichever corpus happens to be default:
// naming an agent this build cannot read is an argument error that lists what
// it can read.
func TestSearchingVerbsRefuseAnAgentWithNoRegisteredProvider(t *testing.T) {
	pinClaudeCodeSelection(t)
	harness(t)

	stopCapture := captureRealStdout(t)
	var out, errOut bytes.Buffer
	code := run([]string{"find", fixtures.NeedleConversation, "--all", "--provider", "no-such-agent"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstderr: %s", code, errOut.String())
	}
	if stdout := stopCapture(); stdout != "" {
		t.Errorf("a refused agent still answered from another agent's corpus:\n%s", stdout)
	}
	for _, want := range []string{"claude-code", "codex"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr does not name registered agent %q:\n%s", want, errOut.String())
		}
	}
}

// TestProviderFlagWinsOverRecallAgentOnASearchingVerb is doctor's
// disagreement case on a verb that searches. The process selection starts at
// codex and RECALL_AGENT names codex, so only --provider actually reaching
// archive.SetSelection can produce a claude-code answer.
func TestProviderFlagWinsOverRecallAgentOnASearchingVerb(t *testing.T) {
	pinCodexSelection(t)
	harness(t)
	fixtures.MaterializeCodex(t)
	t.Setenv("RECALL_AGENT", "codex")

	out, _, err := callFind(t, fixtures.NeedleMultiTierText, "--all", "--provider", "claude-code", "--json")
	if err != nil {
		t.Fatalf("find --provider claude-code: %v", err)
	}
	if ids := answeredSessionIDs(t, out); !slices.Contains(ids, fixtures.SessNeedle) {
		t.Errorf("answered sessions = %v, want the claude-code session %q: --provider must outrank RECALL_AGENT",
			ids, fixtures.SessNeedle)
	}
	codexOnly, _, err := callFind(t, fixtures.NeedleCodexConversation, "--all", "--provider", "claude-code", "--json")
	if err != nil {
		t.Fatalf("find --provider claude-code for a codex token: %v", err)
	}
	if ids := answeredSessionIDs(t, codexOnly); len(ids) != 0 {
		t.Errorf("--provider claude-code answered %v for %q, a token planted only in the Codex corpus",
			ids, fixtures.NeedleCodexConversation)
	}
}

// TestNoUpdateReportsStalenessFromTheEarliestWriteInTheGroup pins the group's
// staleness to its stalest store. Reporting the newest write would tell a
// caller the corpus is current when half of it is three days old.
func TestNoUpdateReportsStalenessFromTheEarliestWriteInTheGroup(t *testing.T) {
	pinClaudeCodeSelection(t)
	_, home := harnessAt(t)
	fixtures.MaterializeCodex(t)

	if _, _, err := callFind(t, fixtures.NeedleCodexConversation, "--all", "--provider", "all"); err != nil {
		t.Fatalf("building both stores: %v", err)
	}

	// 73 hours ago is "3 days ago" by ago()'s own ladder, and the codex store
	// is left as this run wrote it, which is "just now".
	stale := time.Now().Add(-73 * time.Hour)
	claudeMeta := filepath.Join(home, "meta.json")
	if err := os.Chtimes(claudeMeta, stale, stale); err != nil {
		t.Fatalf("chtimes %s: %v", claudeMeta, err)
	}

	out, _, err := callFind(t, fixtures.NeedleCodexConversation, "--all", "--provider", "all", "--no-update", "--json")
	if err != nil {
		t.Fatalf("find --no-update: %v", err)
	}
	coverage, _ := decodeJSON(t, out)["coverage"].(map[string]any)
	if got, _ := coverage["refreshed_ago"].(string); got != "3 days ago" {
		t.Errorf("refreshed_ago = %q, want %q: the group is as stale as its earliest-written store", got, "3 days ago")
	}
}

// TestSearchingVerbsAllowDefaultProviderValues covers the two spellings every
// existing invocation carries implicitly: auto means "let the environment
// decide" and claude-code names the corpus that decision lands on, and both
// have to reach a real search.
func TestSearchingVerbsAllowDefaultProviderValues(t *testing.T) {
	c := harness(t)
	t.Chdir(c.Scratch)

	for _, spec := range []string{"auto", "claude-code"} {
		t.Run(spec, func(t *testing.T) {
			out, _, err := callFind(t, fixtures.NeedleConversation, "--all", "--provider", spec)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if !strings.Contains(out, fixtures.SessNeedle) {
				t.Errorf("--provider %s blocked a normal search:\n%s", spec, out)
			}
		})
	}
}

// The child half of freshRecall reads its arguments from freshArgsEnv and
// leaves its three results in the directory freshDirEnv names.
const (
	freshArgsEnv = "RECALL_TEST_FRESH_ARGS"
	freshDirEnv  = "RECALL_TEST_FRESH_DIR"
)

// TestRecallInAFreshProcess is not a case of its own: it is the child half of
// freshRecall, and it skips in an ordinary run.
func TestRecallInAFreshProcess(t *testing.T) {
	spec, dir := os.Getenv(freshArgsEnv), os.Getenv(freshDirEnv)
	if spec == "" || dir == "" {
		t.Skip("not the child half of a freshRecall run")
	}
	var args []string
	if err := json.Unmarshal([]byte(spec), &args); err != nil {
		t.Fatalf("decode %s: %v", freshArgsEnv, err)
	}

	out := createFile(t, filepath.Join(dir, "stdout"))
	errOut := createFile(t, filepath.Join(dir, "stderr"))
	// The verbs write to os.Stdout rather than to run's writer, and openCorpus
	// writes its own notices to os.Stderr, so both streams are redirected
	// rather than only the writers passed in.
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errOut
	code := run(args, out, errOut)
	os.Stdout, os.Stderr = origOut, origErr

	for _, f := range []*os.File{out, errOut} {
		if err := f.Close(); err != nil {
			t.Fatalf("close %s: %v", f.Name(), err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "code"), []byte(strconv.Itoa(code)), 0o644); err != nil {
		t.Fatalf("write the exit status: %v", err)
	}
}

func createFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	return f
}

// freshRun is what one child process did.
type freshRun struct {
	stdout, stderr string
	code           int
}

// freshRecall runs one recall command in a new process, inheriting this
// test's environment. It exists because archive's agent selection is a
// process-global with no way to unset it: every test that sets one leaves it
// set, so what a caller with no explicit selection at all actually reads can
// only be observed in a process that has never had one. Pinning the global to
// the expected answer instead would be a test that passes with the whole
// selection path deleted.
func freshRecall(t *testing.T, args ...string) freshRun {
	t.Helper()
	dir := t.TempDir()
	spec, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("encode the child's arguments: %v", err)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestRecallInAFreshProcess$", "-test.count=1")
	child.Env = append(os.Environ(), freshArgsEnv+"="+string(spec), freshDirEnv+"="+dir)
	if report, err := child.CombinedOutput(); err != nil {
		t.Fatalf("the child process running `recall %s` failed: %v\n%s", strings.Join(args, " "), err, report)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read the child's %s: %v", name, err)
		}
		return string(b)
	}
	code, err := strconv.Atoi(read("code"))
	if err != nil {
		t.Fatalf("the child's exit status does not parse: %v", err)
	}
	return freshRun{stdout: read("stdout"), stderr: read("stderr"), code: code}
}

// clearAgentDetection empties every variable archive.Select probes, so a run
// under this test decides nothing from the developer's own shell — which does
// carry CLAUDECODE when the tests are run from inside Claude Code.
func clearAgentDetection(t *testing.T) {
	t.Helper()
	for _, key := range []string{"RECALL_AGENT", "CLAUDECODE", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "GEMINI_CLI", "CURSOR_AGENT"} {
		t.Setenv(key, "")
	}
}

// TestSearchingVerbsReadClaudeCodeWithNothingSelectedOrDetected is the
// unchanged default: no --provider, no RECALL_AGENT, nothing detected. The
// answer has to come from the claude-code corpus, out of the archive
// directory claude-code has always used — the root itself, never a
// per-agent subdirectory, because moving it would rebuild every archive that
// already exists.
func TestSearchingVerbsReadClaudeCodeWithNothingSelectedOrDetected(t *testing.T) {
	_, home := harnessAt(t)
	clearAgentDetection(t)
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-codex"))

	got := freshRecall(t, "find", fixtures.NeedleMultiTierText, "--all", "--json")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
	if ids := answeredSessionIDs(t, got.stdout); !slices.Contains(ids, fixtures.SessNeedle) {
		t.Errorf("answered sessions = %v, want the claude-code session %q", ids, fixtures.SessNeedle)
	}
	if snippets := answeredSnippets(t, got.stdout); !anyContains(snippets, fixtures.NeedleMultiTierText) {
		t.Errorf("no returned snippet carries the claude-code corpus's planted token:\n%s", got.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "meta.json")); err != nil {
		t.Errorf("the claude-code store is no longer the archive root itself: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "agents")); err == nil {
		t.Errorf("a default run opened a store under %s, which only a non-claude-code agent takes",
			filepath.Join(home, "agents"))
	}
}

// TestRecallAgentEnvChoosesTheCorpusWithNoProviderFlag is the environment
// spelling of the selection, which only means anything in a process that has
// not been told otherwise.
func TestRecallAgentEnvChoosesTheCorpusWithNoProviderFlag(t *testing.T) {
	harness(t)
	codex := fixtures.MaterializeCodex(t)
	clearAgentDetection(t)
	t.Setenv("RECALL_AGENT", "codex")

	got := freshRecall(t, "find", fixtures.NeedleCodexConversation, "--all", "--json")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
	if snippets := answeredSnippets(t, got.stdout); !anyContains(snippets, fixtures.NeedleCodexConversation) {
		t.Errorf("RECALL_AGENT=codex did not answer from the Codex corpus:\n%s", got.stdout)
	}
	if want, ids := codexNeedleThread(t, codex), answeredSessionIDs(t, got.stdout); !slices.Contains(ids, want) {
		t.Errorf("answered sessions = %v, want the Codex rollout %q among them", ids, want)
	}

	elsewhere := freshRecall(t, "find", fixtures.NeedleMultiTierText, "--all", "--json")
	if elsewhere.code != 1 {
		t.Errorf("exit %d for a token planted only in the claude-code corpus, want 1 (nothing matched)\nstdout: %s",
			elsewhere.code, elsewhere.stdout)
	}
	if ids := answeredSessionIDs(t, elsewhere.stdout); len(ids) != 0 {
		t.Errorf("RECALL_AGENT=codex answered %v for %q, a token planted only in the claude-code corpus",
			ids, fixtures.NeedleMultiTierText)
	}
}

// TestAFallenBackSelectionIsReportedOnStderrOnly covers the caller running
// inside an agent whose session store is not on this machine: the run says
// which corpus it actually read, on stderr, and stdout stays the answer a
// pipe can parse.
func TestAFallenBackSelectionIsReportedOnStderrOnly(t *testing.T) {
	harness(t)
	clearAgentDetection(t)
	t.Setenv("CODEX_THREAD_ID", "0198f0e4-1c2b-7a3d-9f10-5b6c7d8e9f00")
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing-codex"))

	got := freshRecall(t, "find", fixtures.NeedleMultiTierText, "--all", "--json")
	if got.code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
	for _, want := range []string{"codex", "claude-code", "session root does not exist"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr does not say %q; a fallback the caller cannot see is a corpus swapped silently:\n%s",
				want, got.stderr)
		}
	}
	// decodeJSON fails outright if any of that reason reached stdout.
	if ids := answeredSessionIDs(t, got.stdout); !slices.Contains(ids, fixtures.SessNeedle) {
		t.Errorf("answered sessions = %v, want the claude-code session %q it fell back to", ids, fixtures.SessNeedle)
	}
}
