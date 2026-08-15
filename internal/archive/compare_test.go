package archive

import (
	"strings"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

// TestCompareBreaksTiesFieldByField pins the archive's total order: entries
// tied on every field ahead of one are ordered by that field alone. Each case
// changes exactly one field from the base entry so a wrong tie-break in any
// other field cannot make the case pass for the wrong reason.
func TestCompareBreaksTiesFieldByField(t *testing.T) {
	base := entry{
		Turn: schema.Turn{
			Session: "session-a",
			UUID:    "uuid-a",
			TS:      "2026-08-09T10:00:00.000Z",
			Tier:    schema.TierConversation,
			Author:  schema.AuthorHuman,
			Agent:   "agent-a",
			Repo:    "repo-a",
			Branch:  "branch-a",
			CWD:     "/cwd/a",
			Text:    "text-a",
		},
		Seq: 0,
	}

	cases := []struct {
		name   string
		modify func(e entry) entry
	}{
		{"session", func(e entry) entry { e.Session = "session-b"; return e }},
		{"uuid", func(e entry) entry { e.UUID = "uuid-b"; return e }},
		{"timestamp", func(e entry) entry { e.TS = "2026-08-09T11:00:00.000Z"; return e }},
		{"tier", func(e entry) entry { e.Tier = schema.TierResult; return e }},
		{"author", func(e entry) entry { e.Author = schema.AuthorSystem; return e }},
		{"agent", func(e entry) entry { e.Agent = "agent-b"; return e }},
		{"repo", func(e entry) entry { e.Repo = "repo-b"; return e }},
		{"branch", func(e entry) entry { e.Branch = "branch-b"; return e }},
		{"cwd", func(e entry) entry { e.CWD = "/cwd/b"; return e }},
		{"text", func(e entry) entry { e.Text = "text-b"; return e }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			b := tc.modify(base)

			if got := compare(a, a); got != 0 {
				t.Fatalf("compare(a, a) = %d, want 0", got)
			}
			gotAB := compare(a, b)
			gotBA := compare(b, a)
			if gotAB == 0 {
				t.Fatalf("compare found no difference after changing %s", tc.name)
			}
			if (gotAB < 0) == (gotBA < 0) {
				t.Fatalf("compare(a, b) = %d and compare(b, a) = %d do not disagree in sign", gotAB, gotBA)
			}

			wantSign := fieldOrder(base, b, tc.name)
			if (gotAB < 0) != (wantSign < 0) {
				t.Errorf("compare(a, b) sign = %d, want the sign of the %s comparison alone (%d)", gotAB, tc.name, wantSign)
			}
		})
	}
}

// TestCompareOrdersBySeqWhenEveryOtherFieldTies covers the one tie-break that
// is not a string field: a record's turns keep the order they were stripped
// in, which is what keeps its thinking ahead of its own prose.
func TestCompareOrdersBySeqWhenEveryOtherFieldTies(t *testing.T) {
	base := entry{Turn: schema.Turn{Session: "s", UUID: "u", TS: "2026-08-09T10:00:00.000Z"}}
	first := base
	first.Seq = 0
	second := base
	second.Seq = 1

	if got := compare(first, second); got >= 0 {
		t.Errorf("compare(seq 0, seq 1) = %d, want negative", got)
	}
	if got := compare(second, first); got <= 0 {
		t.Errorf("compare(seq 1, seq 0) = %d, want positive", got)
	}
}

// fieldOrder is strings.Compare applied to whichever field the case names,
// which is the requirement compare implements for that field — the expected
// sign is derived from the field values, not from calling compare itself.
func fieldOrder(a, b entry, field string) int {
	switch field {
	case "session":
		return strings.Compare(a.Session, b.Session)
	case "uuid":
		return strings.Compare(a.UUID, b.UUID)
	case "timestamp":
		return strings.Compare(a.TS, b.TS)
	case "tier":
		return strings.Compare(string(a.Tier), string(b.Tier))
	case "author":
		return strings.Compare(string(a.Author), string(b.Author))
	case "agent":
		return strings.Compare(a.Agent, b.Agent)
	case "repo":
		return strings.Compare(a.Repo, b.Repo)
	case "branch":
		return strings.Compare(a.Branch, b.Branch)
	case "cwd":
		return strings.Compare(a.CWD, b.CWD)
	case "text":
		return strings.Compare(a.Text, b.Text)
	}
	panic("unknown field " + field)
}
