package archive

import (
	"os"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// tieredStrip emits one turn per tier from every record, which stubStrip does
// not: it only ever produces conversation turns, so nothing else in this package
// exercises the partition.
func tieredStrip(rec jsonl.Record) ([]schema.Turn, bool) {
	base, ok := stubStrip(rec)
	if !ok {
		return nil, false
	}
	text := base[0].Text
	out := make([]schema.Turn, 0, 3)
	for _, tier := range tierFiles {
		t := base[0]
		t.Tier = tier
		t.Text = string(tier) + ": " + text
		out = append(out, t)
	}
	return out, true
}

func storeWith(t *testing.T, root, dir string, strip func(jsonl.Record) ([]schema.Turn, bool)) *Store {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	s, err := Open(Options{Dir: dir, Root: root, Strip: strip, Resolve: stubResolve})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func threeRecordCorpus(t *testing.T) string {
	t.Helper()
	const sess = "5d0b7c46-8d05-4e93-a712-00000000000f"
	return tinyCorpus(t, map[string]string{
		"-p/" + sess + ".jsonl": strings.Join([]string{
			grownRecord(sess, "5d0b7c46-0000-4000-8000-000000000001", "2026-08-09T10:00:00.000Z", "alpha"),
			grownRecord(sess, "5d0b7c46-0000-4000-8000-000000000002", "2026-08-09T10:00:01.000Z", "bravo"),
		}, "\n"),
	})
}

func TestEachTierIsStoredInItsOwnFile(t *testing.T) {
	s := storeWith(t, threeRecordCorpus(t), "", tieredStrip)
	res := mustUpdate(t, s)

	if res.Coverage.Turns != 6 {
		t.Fatalf("archived %d turns, want 2 records in 3 tiers", res.Coverage.Turns)
	}
	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	for _, tier := range tierFiles {
		state := m.Tiers[string(tier)]
		if state.Turns != 2 || state.Bytes == 0 || state.Checksum == "" {
			t.Errorf("%s tier: %d turns, %d bytes, checksum %q; want 2 turns and a covered file",
				tier, state.Turns, state.Bytes, state.Checksum)
		}
		turns, err := s.Turns(tier)
		if err != nil {
			t.Fatalf("Turns(%s): %v", tier, err)
		}
		if len(turns) != 2 {
			t.Errorf("Turns(%s) returned %d turns, want 2", tier, len(turns))
		}
		for _, turn := range turns {
			if turn.Tier != tier {
				t.Errorf("the %s file holds a %s turn", tier, turn.Tier)
			}
		}
	}

	all, err := s.Turns()
	if err != nil {
		t.Fatalf("Turns(): %v", err)
	}
	if len(all) != 6 {
		t.Errorf("Turns() with no tier named returned %d turns, want every one of 6", len(all))
	}
}

// The whole point of the split: a conversation-tier read must not depend on the
// result tier, which is four fifths of the bytes on the real corpus.
func TestAConversationReadDoesNotTouchTheOtherTiers(t *testing.T) {
	s := storeWith(t, threeRecordCorpus(t), "", tieredStrip)
	mustUpdate(t, s)

	for _, tier := range []schema.Tier{schema.TierInvocation, schema.TierResult} {
		if err := os.WriteFile(s.TierPath(tier), []byte("not a tier file"), 0o644); err != nil {
			t.Fatalf("corrupt the %s tier: %v", tier, err)
		}
	}

	turns, err := s.Turns(schema.TierConversation)
	if err != nil {
		t.Fatalf("a conversation read failed because another tier is corrupt: %v", err)
	}
	if len(turns) != 2 {
		t.Errorf("Turns(conversation) returned %d turns, want 2", len(turns))
	}
	if _, err := s.Turns(); err == nil {
		t.Error("Turns() returned no error over a corrupt tier")
	}
}

func TestVerifyCoversEveryTierNotJustTheFirst(t *testing.T) {
	s := storeWith(t, threeRecordCorpus(t), "", tieredStrip)
	mustUpdate(t, s)

	blob := tierBytes(t, s, schema.TierResult)
	if err := os.WriteFile(s.TierPath(schema.TierResult), blob[:len(blob)-4], 0o644); err != nil {
		t.Fatalf("truncate the result tier: %v", err)
	}

	rep, err := s.Verify()
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.OK {
		t.Fatal("Verify passed an archive whose result tier is truncated")
	}
	if !hasProblem(rep, string(schema.TierResult)) {
		t.Errorf("problems %v never name the result tier", rep.Problems)
	}
}

// A tier this build does not know goes in the conversation file, which every
// search reads, so it is over-searched rather than dropped.
func TestAnUnknownTierIsFiledWhereEverySearchLooks(t *testing.T) {
	const mystery = schema.Tier("mystery")
	strip := func(rec jsonl.Record) ([]schema.Turn, bool) {
		turns, ok := stubStrip(rec)
		if !ok {
			return nil, false
		}
		turns[0].Tier = mystery
		return turns, true
	}
	s := storeWith(t, threeRecordCorpus(t), "", strip)
	mustUpdate(t, s)

	m, ok := s.loadMeta()
	if !ok {
		t.Fatal("metadata did not load")
	}
	if got := m.Tiers[string(schema.TierConversation)].Turns; got != 2 {
		t.Errorf("the conversation file holds %d turns, want the 2 unknown-tier ones", got)
	}
	for _, tier := range []schema.Tier{schema.TierInvocation, schema.TierResult} {
		if got := m.Tiers[string(tier)].Turns; got != 0 {
			t.Errorf("%d unknown-tier turns landed in the %s file, which a default find never reads", got, tier)
		}
	}

	turns, err := s.Turns(schema.TierConversation)
	if err != nil {
		t.Fatalf("Turns(conversation): %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("a %s turn reached %d results in the conversation read, want 2", mystery, len(turns))
	}
	for _, turn := range turns {
		if turn.Tier != mystery {
			t.Errorf("tier label became %q; the record's own tier must survive the file it was filed in", turn.Tier)
		}
	}
}

// Re-framing 208 MB of result tier because a conversation turn arrived is the
// cost this skip removes.
func TestATierThatGainedNothingIsNotRewritten(t *testing.T) {
	root := threeRecordCorpus(t)
	dir := t.TempDir()
	mustUpdate(t, storeWith(t, root, dir, tieredStrip))

	before := map[schema.Tier]os.FileInfo{}
	for _, tier := range tierFiles {
		fi, err := os.Stat(storeWith(t, root, dir, tieredStrip).TierPath(tier))
		if err != nil {
			t.Fatalf("stat the %s tier: %v", tier, err)
		}
		before[tier] = fi
	}

	const sess = "5d0b7c46-8d05-4e93-a712-00000000000f"
	appendRecord(t, root+"/-p/"+sess+".jsonl",
		grownRecord(sess, "5d0b7c46-0000-4000-8000-000000000003", "2026-08-09T10:00:02.000Z", "charlie"))

	conversationOnly := storeWith(t, root, dir, stubStrip)
	res := mustUpdate(t, conversationOnly)
	if res.TurnsAdded != 1 {
		t.Fatalf("added %d turns, want 1", res.TurnsAdded)
	}

	for _, tier := range []schema.Tier{schema.TierInvocation, schema.TierResult} {
		fi, err := os.Stat(conversationOnly.TierPath(tier))
		if err != nil {
			t.Fatalf("stat the %s tier: %v", tier, err)
		}
		if !fi.ModTime().Equal(before[tier].ModTime()) {
			t.Errorf("the %s tier was rewritten though it gained nothing", tier)
		}
	}
	fi, err := os.Stat(conversationOnly.TierPath(schema.TierConversation))
	if err != nil {
		t.Fatalf("stat the conversation tier: %v", err)
	}
	if fi.ModTime().Equal(before[schema.TierConversation].ModTime()) {
		t.Error("the conversation tier gained a turn and was not rewritten")
	}
}
