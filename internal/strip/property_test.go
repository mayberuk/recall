package strip

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/schema"
)

// stripCorpus strips every file under the materialized corpus, in path order.
func stripCorpus(t *testing.T, c fixtures.Corpus) ([]schema.Turn, *Stripper) {
	t.Helper()
	var rels []string
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		rel, err := filepath.Rel(c.Root, path)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	sort.Strings(rels)

	s := New()
	var all []schema.Turn
	for _, rel := range rels {
		all = append(all, stripInto(t, s, c, rel)...)
	}
	return all, s
}

// Every token planted in the corpus must come back out of the strip pass, in the
// tier and session the manifest says. This is the no-false-negative property at
// the only layer that can lose a turn.
func TestPlantedTokensSurfaceInTheirTier(t *testing.T) {
	c := fixtures.Materialize(t)
	turns, _ := stripCorpus(t, c)

	for _, needle := range c.Manifest.Needles {
		var found []schema.Turn
		for _, turn := range turns {
			if strings.Contains(turn.Text, needle.Token) {
				found = append(found, turn)
			}
		}
		if len(found) != len(needle.Files) {
			t.Errorf("%s: found in %d turns, want %d (one per file carrying it)",
				needle.Token, len(found), len(needle.Files))
			continue
		}
		for _, turn := range found {
			if string(turn.Tier) != needle.Tier {
				t.Errorf("%s: tier %q, want %q", needle.Token, turn.Tier, needle.Tier)
			}
			if turn.Session != needle.Session {
				t.Errorf("%s: session %q, want %q", needle.Token, turn.Session, needle.Session)
			}
			if turn.UUID != needle.UUID {
				t.Errorf("%s: uuid %q, want %q", needle.Token, turn.UUID, needle.UUID)
			}
		}
	}
}

// The whole human rule, over the whole corpus: promptSource == "typed" plus the
// arguments of slash-command records, deduplicated by uuid.
func TestHumanTurnsMatchTheManifest(t *testing.T) {
	c := fixtures.Materialize(t)
	turns, s := stripCorpus(t, c)

	human := map[string]bool{}
	for _, turn := range turns {
		if turn.Author == schema.AuthorHuman {
			human[turn.UUID] = true
		}
	}
	if len(human) != c.Manifest.HumanTurns {
		t.Errorf("human turns: %d distinct uuids, want %d", len(human), c.Manifest.HumanTurns)
	}

	obs := s.Observation()
	if obs.Typed != c.Manifest.TypedTurnRecords {
		t.Errorf("typed records: %d, want %d", obs.Typed, c.Manifest.TypedTurnRecords)
	}
	if obs.CommandArgs != c.Manifest.CommandArgTurns {
		t.Errorf("command-argument turns: %d, want %d", obs.CommandArgs, c.Manifest.CommandArgTurns)
	}
}

// Author covers every turn: an unattributed turn would be a turn --mine cannot
// reason about, and a system turn is still searchable.
func TestEveryTurnIsAttributed(t *testing.T) {
	c := fixtures.Materialize(t)
	turns, _ := stripCorpus(t, c)

	legal := map[schema.Author]bool{
		schema.AuthorHuman: true, schema.AuthorAssistant: true,
		schema.AuthorAgent: true, schema.AuthorSystem: true,
	}
	for _, turn := range turns {
		if !legal[turn.Author] {
			t.Errorf("turn %s carries author %q", turn.UUID, turn.Author)
		}
		if turn.UUID == "" || turn.Session == "" || turn.Text == "" {
			t.Errorf("turn %#v is missing an identifying field", turn)
		}
	}
}

// Nothing may collapse two tiers of one record into one turn, because the dedup
// key downstream is the uuid alone.
func TestUUIDAndTierIdentifyATurnWithinAFile(t *testing.T) {
	c := fixtures.Materialize(t)
	_, _ = stripCorpus(t, c)

	for _, rel := range []string{fixtures.FileNeedle, fixtures.FileHugeResult, fixtures.FileEmptyThinking} {
		turns, _ := stripFile(t, c, rel)
		seen := map[string]bool{}
		for _, turn := range turns {
			key := turn.UUID + "\x00" + string(turn.Tier)
			if seen[key] {
				t.Errorf("%s: uuid %s repeats tier %s", rel, turn.UUID, turn.Tier)
			}
			seen[key] = true
		}
	}
}

func TestDoctorWarnsWhenTypedLabelsVanish(t *testing.T) {
	c := fixtures.Materialize(t)

	full := New()
	stripInto(t, full, c, fixtures.FileNeedle)
	if obs := full.Observation(); obs.TypedLabelsMissing() {
		t.Errorf("warned on a corpus with %d typed labels", obs.Typed)
	}

	// no-promptsource carries human-shaped main-session records and no label,
	// which is exactly the corpus shape the warning exists for.
	degraded := New()
	stripInto(t, degraded, c, fixtures.FileNoPromptSource)
	obs := degraded.Observation()
	if obs.Typed != 0 {
		t.Fatalf("fixture carries %d typed labels, so it cannot pin the warning", obs.Typed)
	}
	if obs.HumanShapedMain == 0 {
		t.Fatal("fixture carries no human-shaped main-session records")
	}
	if !obs.TypedLabelsMissing() {
		t.Error("no warning on a corpus with human-shaped records and zero typed labels")
	}
}

func TestNoWarningWhenThereIsNothingHumanShaped(t *testing.T) {
	s := New()
	for _, line := range []string{
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"user","uuid":"a2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"out"}]}}`,
	} {
		s.Strip(parseLine(t, line))
	}
	if obs := s.Observation(); obs.TypedLabelsMissing() {
		t.Errorf("warned on a corpus with no human-shaped records: %#v", obs)
	}
}

// The typed count feeds TypedLabelsMissing, so a label that cannot produce a
// human turn must not count towards it: a corpus of those is still a corpus
// where the rule silently returns nothing.
func TestTypedLabelsOnlyCountWhereTheyAttribute(t *testing.T) {
	s := New()
	for _, line := range []string{
		`{"type":"assistant","uuid":"a1","promptSource":"typed","message":{"role":"assistant","content":[{"type":"text","text":"machine text"}]}}`,
		`{"type":"user","uuid":"a2","isSidechain":true,"promptSource":"typed","message":{"role":"user","content":"a prompt sent to a subagent"}}`,
		`{"type":"user","uuid":"a3","message":{"role":"user","content":"unlabelled prose"}}`,
	} {
		s.Strip(parseLine(t, line))
	}

	obs := s.Observation()
	if obs.Typed != 0 {
		t.Errorf("typed counted %d records that cannot yield a human turn", obs.Typed)
	}
	if obs.HumanShapedMain != 1 {
		t.Errorf("human-shaped main-session records %d, want 1", obs.HumanShapedMain)
	}
	if !obs.TypedLabelsMissing() {
		t.Error("no warning, though every typed label sits where the rule cannot use it")
	}
}
