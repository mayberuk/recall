package strip

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// TestProviderMatchesStripperOnTheFixtureCorpus proves the provider seam does
// not change a single decoding rule: every turn ClaudeCodeProvider produces
// over the shared corpus must equal, in the same order, what Stripper.Strip
// produces directly for the same records, record by record including the
// produced flag, not just in their final aggregate.
func TestProviderMatchesStripperOnTheFixtureCorpus(t *testing.T) {
	c := fixtures.Materialize(t)

	p := ClaudeCode()
	s := New()
	var rels []string
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(c.Root, path)
		if err != nil {
			return err
		}
		if p.IsTranscript(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	sort.Strings(rels)
	if len(rels) == 0 {
		t.Fatal("fixture corpus holds no transcript files; this test would pass vacuously")
	}

	var got, want []schema.Turn
	for _, rel := range rels {
		r, err := jsonl.Open(c.Path(rel))
		if err != nil {
			t.Fatalf("open %s: %v", rel, err)
		}
		dec := p.Decoder(rel)
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				continue
			}
			gotTurns, gotOK := dec.Turns(rec)
			wantTurns, wantOK := s.Strip(rec)
			if gotOK != wantOK {
				t.Errorf("%s: Decoder.Turns produced=%v, Stripper.Strip produced=%v for the same record", rel, gotOK, wantOK)
			}
			got = append(got, gotTurns...)
			want = append(want, wantTurns...)
		}
		_ = r.Close()
	}
	if len(want) == 0 {
		t.Fatal("fixture corpus yielded no turns; this test would pass vacuously")
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider produced %d turns, Stripper.Strip produced %d turns over the same corpus; want deep equality",
			len(got), len(want))
	}
}

// TestProviderMatchesStripperOnARecordWithMoreThanOneTier proves the parity
// above cannot pass if Turns silently truncates every record to its first
// tier. The record is built here rather than read from the shared corpus so
// that the proof holds whatever that corpus happens to contain.
func TestProviderMatchesStripperOnARecordWithMoreThanOneTier(t *testing.T) {
	line := `{"type":"assistant","uuid":"multi-tier","sessionId":"s1","message":{"role":"assistant","content":[{"type":"text","text":"running the check now"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`
	rec := parseLine(t, line)

	want, wantOK := New().Strip(rec)
	if len(want) < 2 {
		t.Fatalf("fixture record yielded %d turns, want more than one so truncation is observable", len(want))
	}

	got, gotOK := ClaudeCode().Decoder("multi-tier.jsonl").Turns(rec)
	if gotOK != wantOK {
		t.Errorf("Decoder.Turns produced=%v, Stripper.Strip produced=%v", gotOK, wantOK)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decoder.Turns = %#v, want %#v", got, want)
	}
}

// The real store is the only place the 24 versions of the format appear. This
// asserts internal consistency, never a byte total: the corpus grows daily.
func TestRealCorpusInternalConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the real-corpus smoke in short mode")
	}
	files := realCorpus(t)

	s := New()
	bytes := map[schema.Tier]int64{}
	turns := map[schema.Tier]int{}
	authors := map[schema.Author]int{}
	human := map[string]bool{}
	var raw int64

	for _, path := range files {
		r, err := jsonl.Open(path)
		if err != nil {
			continue
		}
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				continue
			}
			raw += int64(len(rec.Raw()))
			out, produced := s.Strip(rec)
			if produced != (len(out) > 0) {
				t.Fatalf("%s: Strip reported produced=%v with %d turns", path, produced, len(out))
			}
			for _, turn := range out {
				bytes[turn.Tier] += int64(len(turn.Text))
				turns[turn.Tier]++
				authors[turn.Author]++
				if turn.UUID == "" || turn.Session == "" || turn.Text == "" {
					t.Fatalf("%s: turn %#v is missing an identifying field", path, turn)
				}
				if turn.Repo != "" {
					t.Fatalf("%s: turn %s carries a repo, which internal/repo fills", path, turn.UUID)
				}
				if turn.Author == schema.AuthorHuman {
					human[turn.UUID] = true
				}
			}
		}
		_ = r.Close()
	}

	obs := s.Observation()
	t.Logf("conversation %d turns / %.1f MB · invocation %d / %.1f MB · result %d / %.1f MB",
		turns[schema.TierConversation], mb(bytes[schema.TierConversation]),
		turns[schema.TierInvocation], mb(bytes[schema.TierInvocation]),
		turns[schema.TierResult], mb(bytes[schema.TierResult]))
	t.Logf("authors %v · human uuids %d · typed %d · command-args %d · human-shaped main %d",
		authors, len(human), obs.Typed, obs.CommandArgs, obs.HumanShapedMain)
	t.Logf("records %d over %.0f MB raw · unknown types %v · malformed %d",
		obs.Tally.Lines, mb(raw), obs.Tally.UnknownCounts(), obs.Tally.Malformed)

	if obs.Typed == 0 {
		t.Fatal("no typed labels in the real corpus, so the human rule has nothing to stand on")
	}
	if obs.TypedLabelsMissing() {
		t.Error("the degradation warning fired on a corpus that carries typed labels")
	}
	// The funnel's premise: the conversation tier searched by default is a small
	// slice of the store. If it ever approaches the raw size, something payload
	// shaped has leaked into it.
	if conv := bytes[schema.TierConversation]; conv > raw/20 {
		t.Errorf("conversation tier is %.1f MB of %.0f MB raw, over the 5%% the funnel measures", mb(conv), mb(raw))
	}
	if len(human) > obs.Typed+obs.CommandArgs {
		t.Errorf("%d human turns from %d typed + %d command-argument records", len(human), obs.Typed, obs.CommandArgs)
	}
	if authors[schema.AuthorAgent] == 0 {
		t.Error("no agent-authored turns, but 947 of 1,077 files are subagent transcripts")
	}
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }
