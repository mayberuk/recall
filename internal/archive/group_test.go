package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

// stubbedProvider drives a store through the provider seam with the same
// decoding the legacy tests use, so a case can vary one thing — the agent, the
// root, whether the decoder needs the file's head — without restating how a
// record becomes a turn.
type stubbedProvider struct {
	agent  schema.Agent
	root   string
	head   bool
	decode func(rel string) Decoder
}

func (p stubbedProvider) Agent() schema.Agent          { return p.agent }
func (p stubbedProvider) Root() (string, error)        { return p.root, nil }
func (p stubbedProvider) IsTranscript(rel string) bool { return filepath.Ext(rel) == ".jsonl" }
func (p stubbedProvider) NeedsHead() bool              { return p.head }

func (p stubbedProvider) Decoder(rel string) Decoder {
	if p.decode == nil {
		return stripDecoder(stubStrip)
	}
	return p.decode(rel)
}

func providerStore(t *testing.T, base string, p Provider) *Store {
	t.Helper()
	s, err := Open(Options{Dir: base, Provider: p, Resolve: stubResolve})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// oneRecordCorpus writes a single-record store, so a test can pin the exact
// timestamps two agents interleave at.
func oneRecordCorpus(t *testing.T, session, uuid, ts, text string) string {
	t.Helper()
	return tinyCorpus(t, map[string]string{
		"-p/" + session + ".jsonl": grownRecord(session, uuid, ts, text),
	})
}

func mustGroupTurns(t *testing.T, g *Group) []schema.Turn {
	t.Helper()
	turns, err := g.Turns()
	if err != nil {
		t.Fatalf("Group.Turns: %v", err)
	}
	return turns
}

// A selection can resolve to nothing — asking for every agent on a machine
// that runs none of them does. A group of no stores answers every query with
// no matches, which is indistinguishable from a search that ran.
func TestOpenGroupRefusesASelectionWithNoAgents(t *testing.T) {
	_, err := OpenGroup(Selection{Reason: "explicit selection: all registered agents whose root exists"},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err == nil {
		t.Fatal("OpenGroup accepted a selection naming no agent")
	}
	if !strings.Contains(err.Error(), "session store") {
		t.Errorf("OpenGroup error = %q, want it to say no agent has a session store", err)
	}
}

// claude-code's archive stays at the root of the archive directory and every
// other agent takes a subdirectory of it. Moving claude-code's files would
// rebuild every archive that already exists.
func TestOpenGroupGivesEachAgentItsOwnDirectory(t *testing.T) {
	base := t.TempDir()
	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: t.TempDir()},
		stubbedProvider{agent: schema.AgentCodex, root: t.TempDir()},
	)

	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: base, Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}

	want := map[schema.Agent]string{
		schema.AgentClaudeCode: base,
		schema.AgentCodex:      filepath.Join(base, "agents", "codex"),
	}
	for _, s := range g.Stores() {
		if got := s.Dir(); got != want[s.Agent()] {
			t.Errorf("%s store is at %s, want %s", s.Agent(), got, want[s.Agent()])
		}
	}
}

// Every search takes the one-store path, and merging a single sorted slice is
// a copy of it for no gain.
func TestGroupOfOneStoreReturnsItsTurnsWithoutMerging(t *testing.T) {
	const (
		session = "5d0b7c46-8d05-4e93-a712-00000000000f"
		uuid    = "5d0b7c46-0000-4000-8000-000000000001"
		ts      = "2026-08-09T10:00:00.000Z"
	)
	withProviders(t, stubbedProvider{
		agent: schema.AgentClaudeCode,
		root:  oneRecordCorpus(t, session, uuid, ts, "alpha"),
	})
	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}
	if _, err := g.Update(); err != nil {
		t.Fatalf("Group.Update: %v", err)
	}

	only := g.Stores()[0]
	direct := testing.AllocsPerRun(4, func() {
		if _, err := only.Turns(); err != nil {
			t.Fatalf("Store.Turns: %v", err)
		}
	})
	grouped := testing.AllocsPerRun(4, func() {
		if _, err := g.Turns(); err != nil {
			t.Fatalf("Group.Turns: %v", err)
		}
	})
	if grouped != direct {
		t.Errorf("Group.Turns allocated %v against the store's own %v; a group of one must hand the slice straight back",
			grouped, direct)
	}

	// The allocation count above cannot see a mutation that edits the
	// returned turns' fields in place rather than allocating a copy, so the
	// content is pinned separately against the values the fixture wrote,
	// never against a second decode of the same store.
	got := mustGroupTurns(t, g)
	if len(got) != 1 {
		t.Fatalf("Group.Turns returned %d turns, want 1", len(got))
	}
	want := schema.Turn{
		Session: session,
		UUID:    uuid,
		TS:      ts,
		Tier:    schema.TierConversation,
		Author:  schema.AuthorAssistant,
		Branch:  "main",
		CWD:     "/nowhere/normal",
		Repo:    stubResolve("/nowhere/normal"),
		Origin:  schema.AgentClaudeCode,
		Text:    "alpha",
	}
	if got[0] != want {
		t.Errorf("Group.Turns returned %+v, want %+v: a group of one must return the store's own turn untouched", got[0], want)
	}
}

// The archive sorts on the timestamp first, so two agents' turns have to
// interleave by time rather than arriving one corpus after the other.
func TestGroupOfTwoStoresInterleavesInTheArchivesOrder(t *testing.T) {
	const (
		sessA = "5d0b7c46-8d05-4e93-a712-00000000000a"
		sessB = "5d0b7c46-8d05-4e93-a712-00000000000b"
	)
	rootA := tinyCorpus(t, map[string]string{
		"-p/" + sessA + ".jsonl": strings.Join([]string{
			grownRecord(sessA, "5d0b7c46-0000-4000-8000-00000000000a", "2026-08-09T10:00:00.000Z", "first"),
			grownRecord(sessA, "5d0b7c46-0000-4000-8000-00000000000c", "2026-08-09T10:00:02.000Z", "third"),
		}, "\n"),
	})
	rootB := tinyCorpus(t, map[string]string{
		"-p/" + sessB + ".jsonl": strings.Join([]string{
			grownRecord(sessB, "5d0b7c46-0000-4000-8000-00000000000b", "2026-08-09T10:00:01.000Z", "second"),
			grownRecord(sessB, "5d0b7c46-0000-4000-8000-00000000000d", "2026-08-09T10:00:03.000Z", "fourth"),
		}, "\n"),
	})
	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: rootA},
		stubbedProvider{agent: schema.AgentCodex, root: rootB},
	)

	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}
	if _, err := g.Update(); err != nil {
		t.Fatalf("Group.Update: %v", err)
	}

	var got []string
	for _, turn := range mustGroupTurns(t, g) {
		got = append(got, turn.Text)
	}
	want := []string{"first", "second", "third", "fourth"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Group.Turns read %v, want %v in timestamp order", got, want)
	}
}

// Two turns tied on the timestamp still have to land in a fixed order, and the
// session is the next field both compareTurns and compare carry: this is only
// observable across a merge of two stores, since a single store's own turns
// never need comparing against each other to keep their order.
func TestGroupOfTwoStoresOrdersATimestampTieBySession(t *testing.T) {
	const (
		ts       = "2026-08-09T10:00:00.000Z"
		sessLow  = "5d0b7c46-8d05-4e93-a712-00000000000a"
		sessHigh = "5d0b7c46-8d05-4e93-a712-00000000000b"
	)
	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: oneRecordCorpus(t,
			sessHigh, "5d0b7c46-0000-4000-8000-00000000000b", ts, "from-high-session")},
		stubbedProvider{agent: schema.AgentCodex, root: oneRecordCorpus(t,
			sessLow, "5d0b7c46-0000-4000-8000-00000000000a", ts, "from-low-session")},
	)

	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}
	if _, err := g.Update(); err != nil {
		t.Fatalf("Group.Update: %v", err)
	}

	var got []string
	for _, turn := range mustGroupTurns(t, g) {
		got = append(got, turn.Text)
	}
	want := []string{"from-low-session", "from-high-session"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Group.Turns read %v, want %v ordered by session once the timestamp ties", got, want)
	}
}

// A turn has to say which agent it came from once two of them are in one
// result, and the frames do not carry it: the store stamps it on.
func TestGroupTurnsCarryTheStoreTheyCameFrom(t *testing.T) {
	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: oneRecordCorpus(t,
			"5d0b7c46-8d05-4e93-a712-00000000000a", "5d0b7c46-0000-4000-8000-00000000000a",
			"2026-08-09T10:00:00.000Z", "from claude code")},
		stubbedProvider{agent: schema.AgentCodex, root: oneRecordCorpus(t,
			"5d0b7c46-8d05-4e93-a712-00000000000b", "5d0b7c46-0000-4000-8000-00000000000b",
			"2026-08-09T10:00:01.000Z", "from codex")},
	)

	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}
	if _, err := g.Update(); err != nil {
		t.Fatalf("Group.Update: %v", err)
	}

	want := map[string]schema.Agent{
		"from claude code": schema.AgentClaudeCode,
		"from codex":       schema.AgentCodex,
	}
	seen := 0
	for _, turn := range mustGroupTurns(t, g) {
		if turn.Origin != want[turn.Text] {
			t.Errorf("turn %q came back with origin %q, want %q", turn.Text, turn.Origin, want[turn.Text])
		}
		seen++
	}
	if seen != len(want) {
		t.Errorf("read %d turns, want %d", seen, len(want))
	}
}

// The group's coverage is the outermost boundary any store reaches and the
// sum of what they hold; the per-file skew is one file's own measurement and
// so travels with the file that carries it rather than adding up.
func TestGroupCoverageWidensToEveryStore(t *testing.T) {
	const (
		sessA = "5d0b7c46-8d05-4e93-a712-00000000000a"
		sessB = "5d0b7c46-8d05-4e93-a712-00000000000b"
	)
	rootA := oneRecordCorpus(t, sessA, "5d0b7c46-0000-4000-8000-00000000000a", "2026-08-09T10:00:00.000Z", "first")
	rootB := oneRecordCorpus(t, sessB, "5d0b7c46-0000-4000-8000-00000000000b", "2026-08-09T10:00:01.000Z", "second")
	fileA := filepath.Join(rootA, "-p", sessA+".jsonl")
	fileB := filepath.Join(rootB, "-p", sessB+".jsonl")
	touch(t, fileA, "2026-08-09T11:00:00Z")
	touch(t, fileB, "2026-08-19T10:00:01Z")

	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: rootA},
		stubbedProvider{agent: schema.AgentCodex, root: rootB},
	)
	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}
	if _, err := g.Update(); err != nil {
		t.Fatalf("Group.Update: %v", err)
	}

	cov, err := g.Coverage()
	if err != nil {
		t.Fatalf("Group.Coverage: %v", err)
	}
	if want := 2; cov.Turns != want || cov.Sessions != want || cov.LiveFiles != want {
		t.Errorf("coverage counts turns=%d sessions=%d files=%d, want %d of each",
			cov.Turns, cov.Sessions, cov.LiveFiles, want)
	}
	if want := parseTime(t, "2026-08-09T10:00:00Z"); !cov.ContentFrom.Equal(want) {
		t.Errorf("ContentFrom = %s, want the older store's oldest record %s", cov.ContentFrom, want)
	}
	if want := parseTime(t, "2026-08-09T10:00:01Z"); !cov.ContentTo.Equal(want) {
		t.Errorf("ContentTo = %s, want the newer store's newest record %s", cov.ContentTo, want)
	}
	if want := parseTime(t, "2026-08-09T11:00:00Z"); !cov.LiveFrom.Equal(want) {
		t.Errorf("LiveFrom = %s, want the oldest file mtime %s", cov.LiveFrom, want)
	}
	// The codex file was written ten days after the record it holds; the
	// claude-code file one hour after.
	if want := 10 * 24 * time.Hour; cov.MaxFileSkew != want {
		t.Errorf("MaxFileSkew = %s, want %s", cov.MaxFileSkew, want)
	}
	if want := "-p/" + sessB + ".jsonl"; cov.MaxSkewFile != want {
		t.Errorf("MaxSkewFile = %q, want %q", cov.MaxSkewFile, want)
	}
}

func touch(t *testing.T, path, when string) {
	t.Helper()
	at := parseTime(t, when)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func parseTime(t *testing.T, when string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, when)
	if err != nil {
		t.Fatalf("parse %q: %v", when, err)
	}
	return at
}

// twoStoreGroup opens a group over one claude-code and one codex corpus of a
// single record each, with the source file mtimes pinned so every coverage
// boundary is a value the caller wrote rather than one the archive derived.
func twoStoreGroup(t *testing.T, sessA, sessB, tsA, tsB, mtimeA, mtimeB string) *Group {
	t.Helper()
	rootA := oneRecordCorpus(t, sessA, "5d0b7c46-0000-4000-8000-00000000000a", tsA, "first")
	rootB := oneRecordCorpus(t, sessB, "5d0b7c46-0000-4000-8000-00000000000b", tsB, "second")
	touch(t, filepath.Join(rootA, "-p", sessA+".jsonl"), mtimeA)
	touch(t, filepath.Join(rootB, "-p", sessB+".jsonl"), mtimeB)

	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: rootA},
		stubbedProvider{agent: schema.AgentCodex, root: rootB},
	)
	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}
	return g
}

// The pass already widened every store's coverage, so a caller that has just
// updated must never have to read the metadata back to learn it.
func TestGroupUpdateCarriesTheWidenedCoverage(t *testing.T) {
	const (
		sessA = "5d0b7c46-8d05-4e93-a712-00000000000a"
		sessB = "5d0b7c46-8d05-4e93-a712-00000000000b"
	)
	g := twoStoreGroup(t, sessA, sessB,
		"2026-08-09T10:00:00.000Z", "2026-08-09T10:00:01.000Z",
		"2026-08-09T11:00:00Z", "2026-08-19T10:00:01Z")

	res, err := g.Update()
	if err != nil {
		t.Fatalf("Group.Update: %v", err)
	}
	if want := 2; res.Coverage.Turns != want || res.Coverage.Sessions != want || res.Coverage.LiveFiles != want {
		t.Errorf("coverage counts turns=%d sessions=%d files=%d, want %d of each",
			res.Coverage.Turns, res.Coverage.Sessions, res.Coverage.LiveFiles, want)
	}
	if want := parseTime(t, "2026-08-09T10:00:00Z"); !res.Coverage.ContentFrom.Equal(want) {
		t.Errorf("ContentFrom = %s, want the older store's oldest record %s", res.Coverage.ContentFrom, want)
	}
	if want := parseTime(t, "2026-08-09T10:00:01Z"); !res.Coverage.ContentTo.Equal(want) {
		t.Errorf("ContentTo = %s, want the newer store's newest record %s", res.Coverage.ContentTo, want)
	}
	if want := parseTime(t, "2026-08-09T11:00:00Z"); !res.Coverage.LiveFrom.Equal(want) {
		t.Errorf("LiveFrom = %s, want the oldest source file mtime %s", res.Coverage.LiveFrom, want)
	}
}

// A group is only as current as its stalest store: the newest write would
// state a freshness the corpus as a whole does not have.
func TestGroupWrittenAtIsTheEarliestWriteAmongItsStores(t *testing.T) {
	const (
		earliest = "2026-08-14T09:00:00Z"
		latest   = "2026-08-17T09:00:00Z"
	)
	g := twoStoreGroup(t,
		"5d0b7c46-8d05-4e93-a712-00000000000a", "5d0b7c46-8d05-4e93-a712-00000000000b",
		"2026-08-09T10:00:00.000Z", "2026-08-09T10:00:01.000Z",
		"2026-08-09T11:00:00Z", "2026-08-09T11:00:01Z")
	if _, err := g.Update(); err != nil {
		t.Fatalf("Group.Update: %v", err)
	}

	// The claude-code store is the one written later, so a group taking the
	// first store's write, or the last, gets the wrong answer either way.
	touch(t, g.Stores()[0].MetaPath(), latest)
	touch(t, g.Stores()[1].MetaPath(), earliest)

	if want := parseTime(t, earliest); !g.WrittenAt().Equal(want) {
		t.Errorf("Group.WrittenAt = %s, want the earliest store write %s", g.WrittenAt(), want)
	}
}

// A store with no metadata at all has not been written late, it has not been
// written — and a group holding one cannot claim any write time, the same way
// a single store reports the zero time rather than a guess.
// It runs for either store: an unwritten store's zero time happens to sort
// earliest, so a group that only compared write times would still answer zero
// when the unwritten store is the last one, and the written store's time when
// it is the first.
func TestGroupWrittenAtIsZeroWhenAStoreHasNeverBeenWritten(t *testing.T) {
	for _, unwritten := range []int{0, 1} {
		t.Run(map[int]string{0: "claude-code", 1: "codex"}[unwritten], func(t *testing.T) {
			g := twoStoreGroup(t,
				"5d0b7c46-8d05-4e93-a712-00000000000a", "5d0b7c46-8d05-4e93-a712-00000000000b",
				"2026-08-09T10:00:00.000Z", "2026-08-09T10:00:01.000Z",
				"2026-08-09T11:00:00Z", "2026-08-09T11:00:01Z")
			if _, err := g.Update(); err != nil {
				t.Fatalf("Group.Update: %v", err)
			}
			if err := os.Remove(g.Stores()[unwritten].MetaPath()); err != nil {
				t.Fatalf("remove the store's metadata: %v", err)
			}

			if got := g.WrittenAt(); !got.IsZero() {
				t.Errorf("Group.WrittenAt = %s, want the zero time: store %d has never been written", got, unwritten)
			}
		})
	}
}

// An agent with nothing on disk yet is a real state — its provider is
// registered and its root is empty — and it has to update to an empty store
// rather than fail the group's pass.
func TestGroupUpdateReportsOneResultPerStoreInOrder(t *testing.T) {
	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: oneRecordCorpus(t,
			"5d0b7c46-8d05-4e93-a712-00000000000a", "5d0b7c46-0000-4000-8000-00000000000a",
			"2026-08-09T10:00:00.000Z", "first")},
		stubbedProvider{agent: schema.AgentCodex, root: t.TempDir()},
	)
	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}

	res, err := g.Update()
	if err != nil {
		t.Fatalf("Group.Update: %v", err)
	}
	if len(res.Stores) != 2 {
		t.Fatalf("Update reported %d results, want one per store", len(res.Stores))
	}
	if res.Stores[0].FilesSeen != 1 {
		t.Errorf("the claude-code store saw %d files, want 1", res.Stores[0].FilesSeen)
	}
	if res.Stores[1].FilesSeen != 0 {
		t.Errorf("the codex store saw %d files in an empty root, want 0", res.Stores[1].FilesSeen)
	}
}

// A store that fails partway through really did update the stores before it,
// so Update reports those results rather than discarding them along with the
// error.
func TestGroupUpdateReturnsResultsGatheredBeforeAFailingStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, which ignores the file mode")
	}
	lockedRoot := t.TempDir()
	locked := filepath.Join(lockedRoot, "-p-locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", locked, err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	withProviders(t,
		stubbedProvider{agent: schema.AgentClaudeCode, root: oneRecordCorpus(t,
			"5d0b7c46-8d05-4e93-a712-00000000000a", "5d0b7c46-0000-4000-8000-00000000000a",
			"2026-08-09T10:00:00.000Z", "first")},
		stubbedProvider{agent: schema.AgentCodex, root: lockedRoot},
	)
	g, err := OpenGroup(Selection{Agents: []schema.Agent{schema.AgentClaudeCode, schema.AgentCodex}},
		Options{Dir: t.TempDir(), Resolve: stubResolve})
	if err != nil {
		t.Fatalf("OpenGroup: %v", err)
	}

	res, err := g.Update()
	if err == nil {
		t.Fatal("Group.Update succeeded even though the second store's root cannot be walked")
	}
	if len(res.Stores) != 1 {
		t.Fatalf("Group.Update returned %d results, want the 1 result from the store that updated before the failure", len(res.Stores))
	}
}

// mergeTurns never compares two turns from the same part against each
// other — it only ever takes from the front of one — so this cannot pin
// anything about compareTurns's tie-breaking. It only pins that taking from
// the front leaves a store's own turns in the order Store.Turns produced
// them.
func TestMergeTurnsNeverReordersTurnsWithinAStore(t *testing.T) {
	same := schema.Turn{Session: "s", UUID: "u", TS: "2026-08-09T10:00:00Z", Tier: schema.TierConversation}
	first, second := same, same
	first.Text = "zebra"
	second.Text = "alpha"
	other := same
	other.TS = "2026-08-09T10:00:01Z"
	other.Text = "later"

	got := mergeTurns([][]schema.Turn{{first, second}, {other}}, 3)
	want := []string{"zebra", "alpha", "later"}
	for i, turn := range got {
		if turn.Text != want[i] {
			t.Errorf("merged turn %d is %q, want %q", i, turn.Text, want[i])
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// compareTurns is a hand-maintained mirror of compare, minus the sequence
// number a decoded turn does not carry. Nothing else checks that the two
// stay in step, so this pins that every field compareTurns does carry agrees
// in sign with compare's own comparison of the same field.
func TestCompareTurnsAgreesInSignWithCompareOnEveryFieldItCarries(t *testing.T) {
	base := schema.Turn{
		TS:      "2026-08-09T10:00:00.000Z",
		Session: "5d0b7c46-8d05-4e93-a712-00000000000a",
		UUID:    "5d0b7c46-0000-4000-8000-00000000000a",
		Tier:    schema.TierConversation,
		Author:  schema.AuthorHuman,
		Agent:   "agent-a",
		Repo:    "repo-a",
		Branch:  "branch-a",
		CWD:     "/cwd/a",
		Text:    "text-a",
	}
	cases := []struct {
		name string
		with func(schema.Turn) schema.Turn
	}{
		{"TS", func(t schema.Turn) schema.Turn { t.TS = "2026-08-09T10:00:01.000Z"; return t }},
		{"Session", func(t schema.Turn) schema.Turn { t.Session = "5d0b7c46-8d05-4e93-a712-00000000000b"; return t }},
		{"UUID", func(t schema.Turn) schema.Turn { t.UUID = "5d0b7c46-0000-4000-8000-00000000000b"; return t }},
		{"Tier", func(t schema.Turn) schema.Turn { t.Tier = schema.TierInvocation; return t }},
		{"Author", func(t schema.Turn) schema.Turn { t.Author = schema.AuthorAssistant; return t }},
		{"Agent", func(t schema.Turn) schema.Turn { t.Agent = "agent-b"; return t }},
		{"Repo", func(t schema.Turn) schema.Turn { t.Repo = "repo-b"; return t }},
		{"Branch", func(t schema.Turn) schema.Turn { t.Branch = "branch-b"; return t }},
		{"CWD", func(t schema.Turn) schema.Turn { t.CWD = "/cwd/b"; return t }},
		{"Text", func(t schema.Turn) schema.Turn { t.Text = "text-b"; return t }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			other := c.with(base)
			gotTurns := sign(compareTurns(base, other))
			gotEntries := sign(compare(entry{Turn: base}, entry{Turn: other}))
			if gotTurns == 0 {
				t.Fatalf("compareTurns(base, other) = 0, want a real difference on %s", c.name)
			}
			if gotTurns != gotEntries {
				t.Errorf("compareTurns disagrees with compare on %s: compareTurns sign %d, compare sign %d", c.name, gotTurns, gotEntries)
			}
		})
	}
}
