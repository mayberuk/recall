package render

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func stamp(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad timestamp in test: %v", err)
	}
	return ts
}

// A relaxed search is the one place recall answers a question the caller did
// not quite ask, so the footer has to say so and name the way back.
func TestARelaxedQueryIsDeclaredWithTheWayBack(t *testing.T) {
	c := Coverage{Query: Query{
		Terms:    []string{"build", "number", "bitrise"},
		Required: 2,
		Total:    3,
		Carried:  []string{"build", "number"},
	}}
	line := findLine(t, c.Lines(), "no turn carried all")

	for _, want := range []string{"all 3 terms", "carrying 2", "build, number, bitrise", "--all-terms"} {
		if !strings.Contains(line, want) {
			t.Errorf("relaxation line is missing %q: %s", want, line)
		}
	}
}

// Naming a term as absent is only sound at the bottom level, where a single
// occurrence anywhere would have surfaced it.
func TestATermNothingCarriesIsNamedOnlyWhenThatIsProvable(t *testing.T) {
	bottom := Coverage{Query: Query{
		Terms: []string{"iosbuild", "floor"}, Required: 1, Total: 2, Carried: []string{"iosbuild"},
	}}
	if line := findLine(t, bottom.Lines(), "no turn carried all"); !strings.Contains(line, "no turn carries floor") {
		t.Errorf("the term nothing carries was not named: %s", line)
	}

	middle := Coverage{Query: Query{
		Terms: []string{"a", "b", "c"}, Required: 2, Total: 3, Carried: []string{"a", "b"},
	}}
	if line := findLine(t, middle.Lines(), "no turn carried all"); strings.Contains(line, "no turn carries") {
		t.Errorf("claimed a term is absent from a level that cannot prove it: %s", line)
	}
}

func TestDroppedAndExcludedTermsAreDeclared(t *testing.T) {
	c := Coverage{Query: Query{
		Terms: []string{"wallet"}, Required: 1, Total: 1,
		Dropped: []string{"what", "the"}, Excluded: []string{"testbuild"},
	}}
	lines := c.Lines()
	findLine(t, lines, "ignored as too common")
	if !strings.Contains(findLine(t, lines, "ignored as too common"), "what, the") {
		t.Error("the dropped words were not named")
	}
	if !strings.Contains(findLine(t, lines, "--not"), "testbuild") {
		t.Error("the excluded term was not named")
	}
}

// With --no-update a caller cannot otherwise tell whether it is reading data
// from a minute ago or from last week.
func TestFreshnessSaysHowStaleTheArchiveIs(t *testing.T) {
	stale := Coverage{LiveFrom: stamp(t, "2026-06-10T00:00:00Z"), Refreshed: false, RefreshedAgo: "3 h ago"}
	line := findLine(t, stale.Lines(), "live to ")
	if !strings.Contains(line, "not refreshed") {
		t.Errorf("a stale read did not say so: %s", line)
	}

	fresh := Coverage{LiveFrom: stamp(t, "2026-06-10T00:00:00Z"), Refreshed: true, RefreshedAgo: "just now"}
	if line := findLine(t, fresh.Lines(), "live to "); !strings.Contains(line, "refreshed just now") {
		t.Errorf("a fresh read did not say when: %s", line)
	}
}

// The footer counts itself, so the number it prints is the number of bytes the
// caller actually receives.
func TestTheSizeFooterCountsItself(t *testing.T) {
	body := []byte(strings.Repeat("x", 500) + "\n")
	out := WithSize(body)

	line := strings.TrimSpace(lastLine(string(out)))
	fields := strings.Fields(line)
	if len(fields) < 3 {
		t.Fatalf("size footer is malformed: %q", line)
	}
	bytesClaimed, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("size footer does not lead with a byte count: %q", line)
	}
	if bytesClaimed != len(out) {
		t.Errorf("footer claims %d bytes, output is %d", bytesClaimed, len(out))
	}
}

func TestTheSizeFooterScalesItsUnit(t *testing.T) {
	big := WithSize([]byte(strings.Repeat("x", 4096)))
	if line := lastLine(string(big)); !strings.Contains(line, "KB") {
		t.Errorf("4 KB of output reported as %q", line)
	}
}

// Triage has to be materially cheaper than reading, or a caller pays full price
// to find out which session it wants. Measured on a real machine-wide query it
// is about 2.5x; on a full page of three snippets a session it is more, which is
// the shape this asserts against.
func TestBriefIsMuchSmallerAndKeepsTheCoverageLine(t *testing.T) {
	f := fullPageFixture()
	full, brief := f.Text(), f.Brief()

	if len(brief) >= len(full)/3 {
		t.Errorf("brief is %d bytes against %d full; it is meant to be a fraction", len(brief), len(full))
	}
	for _, out := range [][]byte{full, brief} {
		if !strings.Contains(string(out), "── ") {
			t.Errorf("an output form dropped the coverage line:\n%s", out)
		}
	}
	if strings.Contains(string(brief), "snippet") {
		t.Errorf("brief printed a snippet:\n%s", brief)
	}
	if !strings.Contains(string(brief), "session-0") {
		t.Errorf("brief dropped the sessions it exists to list:\n%s", brief)
	}
}

// A default page: --limit 10 sessions at --hits 3 matched turns each, with
// snippets the width the hit line actually prints.
func fullPageFixture() Find {
	f := Find{Verb: "find", Query: "wallet", Hits: 30, Coverage: Coverage{Sessions: 10, SessionsSearched: 10}}
	for i := 0; i < 10; i++ {
		s := Session{
			ID: "session-" + strconv.Itoa(i), Repo: "gitlab.example/acme/mobile", Branch: "staging",
			Hits: 3, HitTurns: 3, Turns: 200, TurnsKnown: true, First: "2026-07-21", Last: "2026-08-13",
		}
		for j := 0; j < 3; j++ {
			s.Shown = append(s.Shown, Hit{
				UUID: "uuid-" + strconv.Itoa(j), TS: "2026-08-01T10:00:00Z",
				Tier: "conversation", Author: "assistant", Occurrences: 1, Match: "word", Terms: 1,
				Snippet: strings.Repeat("snippet text ", DefaultSnippet/13),
			})
		}
		f.Sessions = append(f.Sessions, s)
	}
	return f
}

func TestIDsAreBareAndOnePerLine(t *testing.T) {
	got := string(findFixture().IDs())
	if got != "b5ddc1af\nb16d73cc\n" {
		t.Errorf("--ids printed %q, want two bare ids", got)
	}
}

// One object per line, and the last line says what the search covered, so a
// streaming caller still learns what was not searched.
func TestJSONLIsOneObjectPerLineEndingInCoverage(t *testing.T) {
	blob, err := findFixture().JSONL()
	if err != nil {
		t.Fatalf("JSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(blob), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 2 matches and 1 coverage record:\n%s", len(lines), blob)
	}
	for i, l := range lines[:2] {
		if !strings.HasPrefix(l, `{"type":"match"`) {
			t.Errorf("line %d is not a match record: %s", i+1, l)
		}
		if !strings.Contains(l, `"snippet"`) || !strings.Contains(l, `"uuid"`) {
			t.Errorf("line %d carries no matched text or uuid: %s", i+1, l)
		}
	}
	if !strings.HasPrefix(lines[2], `{"type":"coverage"`) {
		t.Errorf("last line is not the coverage record: %s", lines[2])
	}
}

func findFixture() Find {
	hit := func(uuid, snippet string, occurrences int) Hit {
		return Hit{UUID: uuid, TS: "2026-08-01T10:00:00Z", Tier: "conversation", Author: "assistant",
			Occurrences: occurrences, Match: "word", Terms: 1, Snippet: snippet}
	}
	return Find{
		Verb: "find", Query: "agvtool", Hits: 4,
		Sessions: []Session{
			{ID: "b5ddc1af", Repo: "gitlab.example/acme/mobile", Branch: "staging",
				Hits: 3, HitTurns: 1, Turns: 119, TurnsKnown: true,
				First: "2026-07-21", Last: "2026-08-13",
				Shown: []Hit{hit("u1", "snippet one about agvtool and the build number it reads", 3)}},
			{ID: "b16d73cc", Repo: "gitlab.example/acme/mobile",
				Hits: 1, HitTurns: 1, Turns: 64, TurnsKnown: true, First: "2026-08-14",
				Shown: []Hit{hit("u2", "snippet two about agvtool shipping with Xcode itself", 1)}},
		},
		Coverage: Coverage{Sessions: 2, SessionsSearched: 2},
	}
}

func findLine(t *testing.T, lines []string, contains string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, contains) {
			return l
		}
	}
	t.Fatalf("no coverage line contains %q:\n%s", contains, strings.Join(lines, "\n"))
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}
