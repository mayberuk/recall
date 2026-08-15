// Package integration wires strip, repo, and archive together the way
// cmd/recall does, and runs the combination once over the shared fixture
// corpus below the CLI layer.
//
// The coverage-line assertion is deliberately absent here: it names what a
// searching CLI command did not search, and this package has no CLI command —
// cmd/recall's own tests cover it.
package integration

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// wantRemoteID is fixtures.OriginURL reduced by the normalization internal/repo
// documents on Identity.ID: host without credentials or port, path without the
// .git suffix, case folded. internal/repo/repo_test.go pins the same value.
const wantRemoteID = "example.invalid/acme/normal"

// gate is the shared fixture corpus run once through the real strip and repo
// packages into a fresh archive.
type gate struct {
	corpus fixtures.Corpus
	store  *archive.Store
	result archive.Result
	turns  []schema.Turn
}

func build(t *testing.T) gate {
	t.Helper()
	corpus := fixtures.Materialize(t)
	s, err := archive.Open(archive.Options{
		Dir:     t.TempDir(),
		Root:    corpus.Root,
		Strip:   strip.New().Strip,
		Resolve: repo.New().Repo,
	})
	if err != nil {
		t.Fatalf("archive.Open: %v", err)
	}
	res, err := s.Update()
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	turns, err := s.Turns()
	if err != nil {
		t.Fatalf("Turns: %v", err)
	}
	return gate{corpus: corpus, store: s, result: res, turns: turns}
}

func (g gate) byUUIDTier(uuid string, tier schema.Tier) []schema.Turn {
	var out []schema.Turn
	for _, turn := range g.turns {
		if turn.UUID != uuid {
			continue
		}
		if tier != "" && turn.Tier != tier {
			continue
		}
		out = append(out, turn)
	}
	return out
}

func (g gate) bySession(session string) []schema.Turn {
	var out []schema.Turn
	for _, turn := range g.turns {
		if turn.Session == session {
			out = append(out, turn)
		}
	}
	return out
}

func (g gate) byTierInSession(session string, tier schema.Tier) []schema.Turn {
	var out []schema.Turn
	for _, turn := range g.bySession(session) {
		if turn.Tier == tier {
			out = append(out, turn)
		}
	}
	return out
}

// TestNeedlesAllFound is the no-false-negative property at integration level:
// every token planted in the fixture corpus (fixtures.Manifest.Needles) reaches
// the archive, in the tier and under the session the manifest names.
func TestNeedlesAllFound(t *testing.T) {
	g := build(t)
	for _, n := range g.corpus.Manifest.Needles {
		t.Run(n.Token, func(t *testing.T) {
			got := g.byUUIDTier(n.UUID, schema.Tier(n.Tier))
			if len(got) != 1 {
				t.Fatalf("needle %q: %d turns for uuid %s tier %s, want 1 (fixtures.Manifest.Needles)",
					n.Token, len(got), n.UUID, n.Tier)
			}
			turn := got[0]
			if turn.Session != n.Session {
				t.Errorf("needle %q: session = %q, want %q (fixtures.Manifest.Needles)", n.Token, turn.Session, n.Session)
			}
			if !strings.Contains(turn.Text, n.Token) {
				t.Errorf("needle %q: turn text does not contain the token: %q", n.Token, turn.Text)
			}
		})
	}
}

// TestDedupAcrossFiles is acceptance case a7 in miniature. For each uuid in
// fixtures.Manifest.DupUUIDs — a record present in two source files — exactly
// one turn per tier must survive. The corpus-wide human-turn count then proves
// dedup rather than assuming it: skipping it would land on TypedTurnRecords (14)
// plus CommandArgTurns instead of the deduplicated HumanTurns (14, via
// TypedTurns 13 + CommandArgTurns 1) — the fixture is built so the two land on
// the same total, which is exactly why the per-uuid check above is required too.
func TestDedupAcrossFiles(t *testing.T) {
	g := build(t)
	for _, d := range g.corpus.Manifest.DupUUIDs {
		t.Run(d.UUID, func(t *testing.T) {
			byTier := map[schema.Tier]int{}
			for _, turn := range g.byUUIDTier(d.UUID, "") {
				byTier[turn.Tier]++
			}
			if len(byTier) == 0 {
				t.Fatalf("uuid %s (in %v): no turn survived; dedup dropped a record instead of a duplicate", d.UUID, d.Files)
			}
			for tier, n := range byTier {
				if n != 1 {
					t.Errorf("uuid %s tier %s: %d turns survived, want exactly 1 (docs/orchestration.md Testing patterns)", d.UUID, tier, n)
				}
			}
		})
	}

	var human int
	for _, turn := range g.turns {
		if turn.Author == schema.AuthorHuman {
			human++
		}
	}
	if want := g.corpus.Manifest.HumanTurns; human != want {
		t.Errorf("human-authored turns = %d, want %d (fixtures.Manifest.HumanTurns)", human, want)
	}
}

// TestIdempotentSecondRunAddsNothing is acceptance case a9: two consecutive
// runs produce byte-identical archive content, checked against the files
// themselves rather than the Result summary.
func TestIdempotentSecondRunAddsNothing(t *testing.T) {
	g := build(t)

	tiers := []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}
	before := make(map[schema.Tier][]byte, len(tiers))
	for _, tier := range tiers {
		before[tier] = mustRead(t, g.store.TierPath(tier))
	}
	cursorBefore := mustRead(t, g.store.CursorPath())
	metaBefore := mustRead(t, g.store.MetaPath())

	res, err := g.store.Update()
	if err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if res.TurnsAdded != 0 {
		t.Errorf("second run added %d turns, want 0", res.TurnsAdded)
	}

	for _, tier := range tiers {
		if !bytes.Equal(before[tier], mustRead(t, g.store.TierPath(tier))) {
			t.Errorf("%s tier bytes changed on an idempotent second run", tier)
		}
	}
	if !bytes.Equal(cursorBefore, mustRead(t, g.store.CursorPath())) {
		t.Error("cursor bytes changed on an idempotent second run")
	}
	if !bytes.Equal(metaBefore, mustRead(t, g.store.MetaPath())) {
		t.Error("metadata bytes changed on an idempotent second run")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// TestRepoIdentityAllCWDShapes is acceptance case a8: the orphaned worktree,
// the remoteless repo, the subdirectory, and the relocated record each resolve
// through the real repo.Resolver to the identity fixtures.Manifest.CWDShapes
// names, never to a wrong repo.
func TestRepoIdentityAllCWDShapes(t *testing.T) {
	g := build(t)
	for _, shape := range g.corpus.Manifest.CWDShapes {
		t.Run(shape.Name, func(t *testing.T) {
			turns := g.bySession(shape.Session)
			if len(turns) == 0 {
				t.Fatalf("no archived turns for session %s (shape %q)", shape.Session, shape.Name)
			}
			want := wantRepoID(shape)
			for _, turn := range turns {
				if turn.Repo != want {
					t.Errorf("session %s cwd shape %q: repo = %q, want %q (fixtures.Manifest.CWDShapes)",
						shape.Session, shape.Name, turn.Repo, want)
				}
			}
		})
	}
}

func wantRepoID(shape fixtures.CWDShape) string {
	switch shape.Identity {
	case fixtures.RepoRemote:
		return wantRemoteID
	case fixtures.RepoNoRemote:
		return shape.Toplevel
	default:
		return fixtures.RepoNone
	}
}

// TestSessionIsTheUnitNotFile pins the finding that named the archive's own
// invariant: fixtures.Manifest.MultiSessionFile carries two sessionIds in one
// file, and fixtures.Manifest.DupUUIDs' session is split across two files, so
// neither counting files nor counting lines reproduces the session count.
func TestSessionIsTheUnitNotFile(t *testing.T) {
	g := build(t)

	cov, err := g.store.Coverage()
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	want := len(g.corpus.Manifest.Sessions)
	if cov.Sessions != want {
		t.Errorf("archive reports %d sessions, want %d (fixtures.Manifest.Sessions)", cov.Sessions, want)
	}

	seen := map[string]bool{}
	for _, turn := range g.turns {
		seen[turn.Session] = true
	}
	if len(seen) != want {
		t.Errorf("archive holds turns for %d distinct sessions, want %d (fixtures.Manifest.Sessions)", len(seen), want)
	}
	for _, id := range g.corpus.Manifest.MultiSessionIDs {
		if !seen[id] {
			t.Errorf("session %s from the multi-session file is missing from the archive (fixtures.Manifest.MultiSessionIDs)", id)
		}
	}
	for _, id := range g.corpus.Manifest.Sessions {
		if !seen[id] {
			t.Errorf("session %s is missing from the archive (fixtures.Manifest.Sessions)", id)
		}
	}
}

// TestUnknownTypesSurvive is the version-tolerance property: a record type
// this build has never seen must not crash the parser, must not silently drop
// a conversational turn from the same file, and must be tallied for `doctor`.
func TestUnknownTypesSurvive(t *testing.T) {
	g := build(t)

	want := g.corpus.Manifest.UnknownTypes
	got := g.result.Tally.Unknown
	if len(got) != len(want) {
		t.Fatalf("tallied %d unknown types, want %d (fixtures.Manifest.UnknownTypes): got %v", len(got), len(want), got)
	}
	for typ, n := range want {
		if got[typ] != n {
			t.Errorf("unknown type %q tallied %d times, want %d (fixtures.Manifest.UnknownTypes)", typ, got[typ], n)
		}
	}

	// The unknown-type fixture brackets its unrecognised records with two
	// ordinary conversation turns; losing either is the silent drop this
	// property exists to catch.
	conv := g.byTierInSession(fixtures.SessUnknownType, schema.TierConversation)
	if len(conv) != 2 {
		t.Errorf("session %s: %d conversation turns survived around the unknown records, want 2",
			fixtures.SessUnknownType, len(conv))
	}
}
