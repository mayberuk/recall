package scan

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// corpus strips the shared fixtures in path order, which is the order the
// archive walk produces and therefore the order Search sees.
func corpus(t *testing.T) ([]schema.Turn, fixtures.Manifest) {
	t.Helper()
	c := fixtures.Materialize(t)

	var files []string
	err := filepath.WalkDir(c.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	sort.Strings(files)

	s := strip.New()
	var turns []schema.Turn
	for _, path := range files {
		r, err := jsonl.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				continue
			}
			out, _ := s.Strip(rec)
			turns = append(turns, out...)
		}
		if err := r.Err(); err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		_ = r.Close()
	}
	if len(turns) == 0 {
		t.Fatal("the shared corpus stripped to nothing")
	}
	return turns, c.Manifest
}

func tierComplement(tier schema.Tier) []schema.Tier {
	var out []schema.Tier
	for _, candidate := range allTiers {
		if candidate != tier {
			out = append(out, candidate)
		}
	}
	return out
}

// Every token the manifest plants comes back, in the tier and session the
// manifest names, once per file carrying it. This is the dealbreaker as a test:
// reporting nothing when the thing is present.
func TestEveryPlantedTokenIsFoundInItsTier(t *testing.T) {
	turns, m := corpus(t)

	for _, needle := range m.Needles {
		tier := schema.Tier(needle.Tier)
		res := Search(turns, Query{Text: needle.Token, Tiers: allTiers})

		if len(res.Hits) != len(needle.Files) {
			t.Errorf("%s: %d hits, want %d (one per file carrying it)",
				needle.Token, len(res.Hits), len(needle.Files))
			continue
		}
		for _, h := range res.Hits {
			if h.Tier != tier {
				t.Errorf("%s: tier %q, want %q", needle.Token, h.Tier, tier)
			}
			if h.Session != needle.Session {
				t.Errorf("%s: session %q, want %q", needle.Token, h.Session, needle.Session)
			}
			if h.UUID != needle.UUID {
				t.Errorf("%s: uuid %q, want %q", needle.Token, h.UUID, needle.UUID)
			}
			if got := h.Text[h.Offset : h.Offset+h.Length]; !strings.EqualFold(got, needle.Token) {
				t.Errorf("%s: hit locates %q", needle.Token, got)
			}
		}

		scoped := Search(turns, Query{Text: needle.Token, Tiers: []schema.Tier{tier}})
		if len(scoped.Hits) != len(needle.Files) {
			t.Errorf("%s: %d hits with only its own tier searched, want %d",
				needle.Token, len(scoped.Hits), len(needle.Files))
		}
	}
}

// The other half of the same guarantee: a tier that was not searched yields
// nothing and is named as unsearched. Silence with a declaration is honest
// coverage; silence without one is the dealbreaker.
func TestATokenIsInvisibleOutsideItsTier(t *testing.T) {
	turns, m := corpus(t)

	for _, needle := range m.Needles {
		tier := schema.Tier(needle.Tier)
		res := Search(turns, Query{Text: needle.Token, Tiers: tierComplement(tier)})
		if len(res.Hits) != 0 {
			t.Errorf("%s: %d hits with tier %q excluded, want 0", needle.Token, len(res.Hits), tier)
		}
		named := false
		for _, skipped := range res.Unsearched() {
			named = named || skipped == tier
		}
		if !named {
			t.Errorf("%s: tier %q was not searched and not declared unsearched (%v)",
				needle.Token, tier, res.Unsearched())
		}
	}
}

// Acceptance case a6: a token present only in tool output is not returned by
// default, is returned with the result tier enabled, and the default run said
// which tier it left out.
func TestA6TokenOnlyInToolOutput(t *testing.T) {
	turns, m := corpus(t)

	var token string
	for _, needle := range m.Needles {
		if needle.Tier == fixtures.TierResult {
			token = needle.Token
		}
	}
	if token == "" {
		t.Fatal("the manifest plants no result-tier token, so a6 cannot be pinned here")
	}

	byDefault := Search(turns, Query{Text: token})
	if len(byDefault.Hits) != 0 {
		t.Errorf("default search returned %d hits for a tool-output token", len(byDefault.Hits))
	}
	declared := false
	for _, skipped := range byDefault.Unsearched() {
		declared = declared || skipped == schema.TierResult
	}
	if !declared {
		t.Errorf("default search did not declare the result tier unsearched: %v", byDefault.Unsearched())
	}

	withResults := Search(turns, Query{Text: token, Tiers: Tiers(true, false)})
	if len(withResults.Hits) != 1 {
		t.Fatalf("--results returned %d hits, want 1", len(withResults.Hits))
	}
	if withResults.Hits[0].Tier != schema.TierResult {
		t.Errorf("hit tier %q, want %q", withResults.Hits[0].Tier, schema.TierResult)
	}
}

// The rejected head+tail index would have caught 19% of tool-result text. The
// planted result token sits outside a 1 KB head and a 1 KB tail, so a scan that
// truncates cannot pass the property test above.
func TestTheResultNeedleLiesBeyondAHeadAndTail(t *testing.T) {
	turns, m := corpus(t)

	const window = 1 << 10
	for _, needle := range m.Needles {
		if needle.Tier != fixtures.TierResult {
			continue
		}
		res := Search(turns, Query{Text: needle.Token, Tiers: []schema.Tier{schema.TierResult}})
		if len(res.Hits) != 1 {
			t.Fatalf("%s: %d hits, want 1", needle.Token, len(res.Hits))
		}
		h := res.Hits[0]
		tail := len(h.Text) - h.Offset - h.Length
		if h.Offset <= window || tail <= window {
			t.Errorf("%s sits %d bytes from the head and %d from the tail of a %d byte turn; "+
				"a %d byte head and tail would still find it, so the fixture does not pin truncation",
				needle.Token, h.Offset, tail, len(h.Text), window)
		}
	}
}

func TestPlantedTokensAreFoundWhateverTheCase(t *testing.T) {
	turns, m := corpus(t)

	for _, needle := range m.Needles {
		for _, form := range []string{strings.ToUpper(needle.Token), capitalize(needle.Token)} {
			res := Search(turns, Query{Text: form, Tiers: allTiers})
			if len(res.Hits) != len(needle.Files) {
				t.Errorf("%s: %d hits, want %d", form, len(res.Hits), len(needle.Files))
			}
		}
	}
}

// Scan reports one hit per matching turn and leaves uuid dedup to ranking: the
// duplicated token is carried by one record uuid in two files, so scan says two
// and the contract's single logical turn is ranking's to compute.
func TestADuplicatedRecordIsOneHitPerFile(t *testing.T) {
	turns, m := corpus(t)

	res := Search(turns, Query{Text: fixtures.NeedleDuplicated, Tiers: allTiers})
	if len(res.Hits) != 2 {
		t.Fatalf("%d hits, want 2 — one per file carrying the record", len(res.Hits))
	}
	if res.Hits[0].UUID != res.Hits[1].UUID {
		t.Errorf("uuids %q and %q differ, so this is not the duplicated record",
			res.Hits[0].UUID, res.Hits[1].UUID)
	}
	var dup fixtures.DupUUID
	for _, d := range m.DupUUIDs {
		if d.UUID == res.Hits[0].UUID {
			dup = d
		}
	}
	if len(dup.Files) != 2 {
		t.Errorf("uuid %q is not a manifest duplicate", res.Hits[0].UUID)
	}
}

// Coverage counts are what a response declares about itself, so they are checked
// against a count taken the naive way rather than against the optimized one.
func TestCoverageCountsMatchANaiveCount(t *testing.T) {
	turns, _ := corpus(t)

	all := map[string]bool{}
	conversation := map[string]bool{}
	convTurns := 0
	for _, turn := range turns {
		all[turn.Session] = true
		if turn.Tier == schema.TierConversation {
			conversation[turn.Session] = true
			convTurns++
		}
	}

	res := Search(turns, Query{Text: "nothing-matches-this-token"})
	if res.Turns != len(turns) || res.TurnsScanned != convTurns {
		t.Errorf("turns %d scanned %d, want %d and %d",
			res.Turns, res.TurnsScanned, len(turns), convTurns)
	}
	if res.Sessions != len(all) || res.SessionsScanned != len(conversation) {
		t.Errorf("sessions %d scanned %d, want %d and %d",
			res.Sessions, res.SessionsScanned, len(all), len(conversation))
	}

	full := Search(turns, Query{Text: "nothing-matches-this-token", Tiers: allTiers})
	if full.SessionsScanned != full.Sessions || full.TurnsScanned != full.Turns {
		t.Errorf("all tiers searched but %d/%d sessions and %d/%d turns counted as scanned",
			full.SessionsScanned, full.Sessions, full.TurnsScanned, full.Turns)
	}
}

func capitalize(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}
