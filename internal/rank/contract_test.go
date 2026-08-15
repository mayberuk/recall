package rank_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/schema"
)

// walletRow is one row of docs/design.md §Ranking evidence, transcribed. Density
// is the document's own ratio column — the rule that fails in both directions.
type walletRow struct {
	session string
	hits    int
	turns   int
	density float64
	note    string
}

var walletTable = []walletRow{
	{"4fa40cc0", 31, 192, 0.1615, "short, genuinely about wallet"},
	{"e5f9a621", 1, 13, 0.0769, "one passing mention — outranks the real one"},
	{"6941d8f9", 554, 7944, 0.0697, "the corpus's most wallet-intensive session"},
	{"0b040c29", 1, 7181, 0.000, "huge session, one mention — correctly sunk"},
}

// wantOrder is what the contract requires of the ranking: the 554-hit session
// above the one-mention-in-13 session, and the one-mention-in-7181 still sunk,
// with the short genuinely-topical session on top.
var wantOrder = []string{"4fa40cc0", "6941d8f9", "e5f9a621", "0b040c29"}

func row(t *testing.T, session string) walletRow {
	t.Helper()
	for _, r := range walletTable {
		if r.session == session {
			return r
		}
	}
	t.Fatalf("no row %q in the contract table", session)
	return walletRow{}
}

var base = time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)

func hit(session, uuid string, opts ...func(*schema.Hit)) schema.Hit {
	h := schema.Hit{
		Session: session,
		UUID:    uuid,
		TS:      base.Format(time.RFC3339),
		Tier:    schema.TierConversation,
		Author:  schema.AuthorAssistant,
		Repo:    fixtures.RepoRemote,
		Branch:  "main",
		Text:    "wallet",
		Length:  6,
	}
	for _, o := range opts {
		o(&h)
	}
	return h
}

func at(minutes int) func(*schema.Hit) {
	return func(h *schema.Hit) { h.TS = base.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339) }
}

func tier(v schema.Tier) func(*schema.Hit) {
	return func(h *schema.Hit) { h.Tier = v }
}

func author(v schema.Author) func(*schema.Hit) {
	return func(h *schema.Hit) { h.Author = v }
}

func text(v string) func(*schema.Hit) {
	return func(h *schema.Hit) { h.Text = v }
}

func offset(v int) func(*schema.Hit) {
	return func(h *schema.Hit) { h.Offset = v }
}

func branch(v string) func(*schema.Hit) {
	return func(h *schema.Hit) { h.Branch = v }
}

func repo(v string) func(*schema.Hit) {
	return func(h *schema.Hit) { h.Repo = v }
}

// spread builds n hits in one session, each on its own record uuid and minute,
// so nothing collapses under dedup and every hit occupies a distinct turn.
func spread(session string, n int, opts ...func(*schema.Hit)) []schema.Hit {
	out := make([]schema.Hit, 0, n)
	for i := range n {
		o := append([]func(*schema.Hit){at(i)}, opts...)
		out = append(out, hit(session, fmt.Sprintf("%s-%05d", session, i), o...))
	}
	return out
}

func sessionIDs(sessions []rank.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func sessionByID(t *testing.T, r rank.Result, id string) rank.Session {
	t.Helper()
	for _, s := range r.Sessions {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %q is missing from the result", id)
	return rank.Session{}
}

func needleFor(t *testing.T, m fixtures.Manifest, token string) fixtures.Needle {
	t.Helper()
	for _, n := range m.Needles {
		if n.Token == token {
			return n
		}
	}
	t.Fatalf("fixtures manifest carries no needle %q", token)
	return fixtures.Needle{}
}
