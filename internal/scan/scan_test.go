package scan

import (
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func turn(session string, tier schema.Tier, text string) schema.Turn {
	return schema.Turn{
		Session: session,
		UUID:    session + "-" + string(tier),
		TS:      "2026-08-01T10:00:00Z",
		Tier:    tier,
		Author:  schema.AuthorAssistant,
		Repo:    "remote",
		Branch:  "main",
		Text:    text,
	}
}

// Offsets are counted by hand off the literal, not read back from a run: a hit
// that reports where it matched is what lets a renderer highlight without
// searching again.
func TestMatchOffsetsLocateTheTermInTheHitText(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		query  string
		offset int
		length int
		extent string
	}{
		{"start", "wallet button", "wallet", 0, 6, "wallet"},
		{"mid-string", "the Wallet button", "wallet", 4, 6, "Wallet"},
		{"upper query lower text", "the wallet button", "WALLET", 4, 6, "wallet"},
		{"inside a longer word", "walletbutton", "wallet", 0, 6, "wallet"},
		{"non-ascii keeps byte offsets", "an ÉCLAIR here", "éclair", 3, 7, "ÉCLAIR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Search([]schema.Turn{turn("s1", schema.TierConversation, tc.text)}, Query{Text: tc.query})
			if len(res.Hits) != 1 {
				t.Fatalf("%d hits, want 1", len(res.Hits))
			}
			h := res.Hits[0]
			if h.Offset != tc.offset || h.Length != tc.length {
				t.Fatalf("offset %d length %d, want %d and %d", h.Offset, h.Length, tc.offset, tc.length)
			}
			if h.Text != tc.text {
				t.Fatalf("hit text %q, want the whole turn text %q", h.Text, tc.text)
			}
			if got := h.Text[h.Offset : h.Offset+h.Length]; got != tc.extent {
				t.Fatalf("hit locates %q, want %q", got, tc.extent)
			}
		})
	}
}

func TestEveryTermMustMatch(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet button was renamed")}
	if res := Search(turns, Query{Text: "wallet renamed"}); len(res.Hits) != 2 {
		t.Errorf("both terms present: %d hits, want one per term", len(res.Hits))
	}
	if res := Search(turns, Query{Text: "wallet zebra", AllTerms: true}); len(res.Hits) != 0 {
		t.Errorf("--all-terms with one term absent: %d hits, want 0", len(res.Hits))
	}
}

// A query no turn carries in full degrades to the turns carrying the most of
// it, because an agent that gets nothing back reads it as "the tool cannot find
// this" and stops. Widening is also the safe direction for the no-false-negative
// dealbreaker.
func TestBestPartialMatchIsKeptWhenNoTurnCarriesEveryTerm(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the wallet button was renamed"),
		turn("s1", schema.TierConversation, "the wallet balance and the wallet header"),
		turn("s1", schema.TierConversation, "nothing relevant here"),
	}
	res := Search(turns, Query{Text: "wallet zebra"})
	if len(res.Hits) != 3 {
		t.Fatalf("%d hits, want 3 — one per occurrence of the one carried term", len(res.Hits))
	}
	if got, want := res.Match.Required, 1; got != want {
		t.Errorf("Required %d, want %d", got, want)
	}
	if got, want := res.Match.Total, 2; got != want {
		t.Errorf("Total %d, want %d", got, want)
	}
	if !res.Match.Relaxed() {
		t.Error("Relaxed false, want true — the caller has to be told the query was not met in full")
	}
	if got := res.Match.Carried; len(got) != 1 || got[0] != "wallet" {
		t.Errorf("Carried %v, want [wallet]", got)
	}
}

// The best level wins outright: once one turn carries more terms, everything
// collected at a lower level is obsolete, however early it arrived.
func TestABetterMatchDiscardsWhatCameBefore(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "wallet alone"),
		turn("s2", schema.TierConversation, "wallet and zebra together"),
		turn("s3", schema.TierConversation, "wallet again"),
	}
	res := Search(turns, Query{Text: "wallet zebra"})
	if len(res.Hits) != 2 {
		t.Fatalf("%d hits, want 2 — both terms of the one turn carrying both", len(res.Hits))
	}
	for _, h := range res.Hits {
		if h.Session != "s2" {
			t.Errorf("hit from %s, want only s2", h.Session)
		}
	}
	if res.Match.Relaxed() {
		t.Error("Relaxed true, want false — a turn carried every term")
	}
}

func TestNotExcludesATurn(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "wallet in the testbuild skill preamble"),
		turn("s2", schema.TierConversation, "wallet balance rounding"),
	}
	res := Search(turns, Query{Text: "wallet", Not: []string{"testbuild"}})
	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1", len(res.Hits))
	}
	if res.Hits[0].Session != "s2" {
		t.Errorf("kept %s, want s2", res.Hits[0].Session)
	}
}

func TestKeepNarrowsWhatIsScannedWithoutHidingTheTotal(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "wallet one"),
		turn("s2", schema.TierConversation, "wallet two"),
	}
	res := Search(turns, Query{Text: "wallet", Keep: func(t *schema.Turn) bool { return t.Session == "s2" }})
	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1", len(res.Hits))
	}
	if res.Sessions != 2 || res.SessionsScanned != 1 {
		t.Errorf("%d sessions with %d searched, want 2 and 1 — the coverage line states both",
			res.Sessions, res.SessionsScanned)
	}
	if res.Turns != 2 || res.TurnsScanned != 1 {
		t.Errorf("%d turns with %d searched, want 2 and 1", res.Turns, res.TurnsScanned)
	}
}

// A quoted phrase is one term. Without this the quotes an agent types become
// separate words that match anywhere in a turn.
func TestQuotedPhraseIsOneTerm(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "the build number was bumped"),
		turn("s2", schema.TierConversation, "the number of builds we ship"),
	}
	res := Search(turns, Query{Text: `"build number"`})
	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1 — only the turn carrying the phrase", len(res.Hits))
	}
	if res.Hits[0].Session != "s1" {
		t.Errorf("matched %s, want s1", res.Hits[0].Session)
	}
	if got, want := res.Match.Total, 1; got != want {
		t.Errorf("Total %d, want %d — a phrase is one term", got, want)
	}
}

// A term every turn carries adds nothing to the count that ranks a turn, so a
// long query spends its terms on words that discriminate.
func TestCommonWordsAreDroppedFromALongQueryAndDeclared(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet balance")}
	res := Search(turns, Query{Text: "what is the wallet balance"})
	if got, want := res.Match.Total, 2; got != want {
		t.Errorf("Total %d, want %d — only wallet and balance discriminate", got, want)
	}
	if len(res.Match.Dropped) != 3 {
		t.Errorf("Dropped %v, want three common words", res.Match.Dropped)
	}
}

func TestATwoTermQueryKeepsItsCommonWords(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet")}
	res := Search(turns, Query{Text: "the wallet"})
	if got, want := res.Match.Total, 2; got != want {
		t.Errorf("Total %d, want %d — a short query is all the caller gave us", got, want)
	}
	if len(res.Match.Dropped) != 0 {
		t.Errorf("Dropped %v, want none", res.Match.Dropped)
	}
}

// Substring matches are kept — a term inside an identifier is a real hit — so
// the distinction is carried on the hit and spent on ranking instead.
func TestMatchKindDistinguishesAWordFromAnIdentifierInterior(t *testing.T) {
	cases := []struct {
		text  string
		query string
		want  schema.MatchKind
	}{
		{"the build number", "build", schema.MatchWord},
		{"iosbuild is bumped", "build", schema.MatchInside},
		{"the wallets moved", "wallet", schema.MatchPrefix},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			res := Search([]schema.Turn{turn("s1", schema.TierConversation, tc.text)},
				Query{Text: tc.query, Exact: true})
			if len(res.Hits) == 0 {
				t.Fatal("no hits")
			}
			if got := res.Hits[0].Match; got != tc.want {
				t.Errorf("match kind %q, want %q", got, tc.want)
			}
		})
	}
}

// Ranking keys a hit on session, uuid, tier, offset, length and text, so two
// matches at different offsets in one turn are two hits. Collapsing them here
// would undercount a session the query is genuinely about.
func TestEveryOccurrenceIsItsOwnHit(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		query string
		spans []span
	}{
		{"same term twice", "wallet and wallet again", "wallet",
			[]span{{offset: 0, length: 6}, {offset: 11, length: 6}}},
		{"two terms, offset order", "wallet and wallet again", "again wallet",
			[]span{{offset: 0, length: 6}, {offset: 11, length: 6}, {offset: 18, length: 5}}},
		{"occurrences do not overlap", "aaaa", "aa",
			[]span{{offset: 0, length: 2}, {offset: 2, length: 2}}},
		{"two terms with one stem are one match", "the wallet button", "wallet wallets",
			[]span{{offset: 4, length: 6}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Search([]schema.Turn{turn("s1", schema.TierConversation, tc.text)}, Query{Text: tc.query})
			if len(res.Hits) != len(tc.spans) {
				t.Fatalf("%d hits, want %d", len(res.Hits), len(tc.spans))
			}
			for i, want := range tc.spans {
				if got := res.Hits[i]; got.Offset != want.offset || got.Length != want.length {
					t.Errorf("hit %d at offset %d length %d, want %d and %d",
						i, got.Offset, got.Length, want.offset, want.length)
				}
			}
		})
	}
}

// The concentration denominator ranking divides by counts conversation turns
// only, and counts them whatever tier the search covered.
func TestConversationTurnsAreCountedPerSession(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "one"),
		turn("s1", schema.TierResult, "two"),
		turn("s1", schema.TierConversation, "three"),
		turn("s2", schema.TierResult, "four"),
		turn("s1", schema.TierConversation, "five"),
	}
	for _, tiers := range [][]schema.Tier{nil, allTiers, {schema.TierResult}} {
		got := Search(turns, Query{Text: "nothing", Tiers: tiers}).TurnsBySession
		if got["s1"] != 3 || got["s2"] != 0 {
			t.Errorf("tiers %v: conversation turns s1=%d s2=%d, want 3 and 0",
				tiers, got["s1"], got["s2"])
		}
		if len(got) != 2 {
			t.Errorf("tiers %v: %d sessions counted, want 2", tiers, len(got))
		}
	}
}

// An empty query matching everything would hand back the whole corpus, which is
// the bounded-output dealbreaker. It matches nothing instead.
func TestEmptyQueryMatchesNothing(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "anything at all")}
	for _, q := range []string{"", "   ", "\t\n"} {
		res := Search(turns, Query{Text: q})
		if len(res.Hits) != 0 {
			t.Errorf("query %q: %d hits, want 0", q, len(res.Hits))
		}
		if len(res.Terms) != 0 {
			t.Errorf("query %q: %d term reports, want 0", q, len(res.Terms))
		}
		if res.TurnsScanned != 1 {
			t.Errorf("query %q: %d turns scanned, want 1", q, res.TurnsScanned)
		}
	}
}

func TestDefaultSearchesConversationOnly(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "spoken"),
		turn("s1", schema.TierInvocation, "spoken"),
		turn("s1", schema.TierResult, "spoken"),
	}
	res := Search(turns, Query{Text: "spoken"})

	if len(res.Hits) != 1 || res.Hits[0].Tier != schema.TierConversation {
		t.Fatalf("default search returned %d hits %v, want one conversation hit", len(res.Hits), res.Hits)
	}
	if len(res.Tiers) != 1 || res.Tiers[0] != schema.TierConversation {
		t.Errorf("declared tiers %v, want conversation only", res.Tiers)
	}
	want := []schema.Tier{schema.TierInvocation, schema.TierResult}
	got := res.Unsearched()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("unsearched %v, want %v", got, want)
	}
	if res.Turns != 3 || res.TurnsScanned != 1 {
		t.Errorf("turns %d scanned %d, want 3 and 1", res.Turns, res.TurnsScanned)
	}
}

func TestOptInTiersAreSearchedAndDeclared(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "nothing here"),
		turn("s1", schema.TierInvocation, "rg -n spoken internal/"),
		turn("s1", schema.TierResult, "spoken twice"),
	}
	res := Search(turns, Query{Text: "spoken", Tiers: Tiers(true, true)})

	if len(res.Hits) != 2 {
		t.Fatalf("%d hits, want 2", len(res.Hits))
	}
	if res.Hits[0].Tier != schema.TierInvocation || res.Hits[1].Tier != schema.TierResult {
		t.Errorf("tiers %q and %q, want invocation then result", res.Hits[0].Tier, res.Hits[1].Tier)
	}
	if len(res.Unsearched()) != 0 {
		t.Errorf("unsearched %v, want none", res.Unsearched())
	}
	if res.TurnsScanned != 3 {
		t.Errorf("%d turns scanned, want 3", res.TurnsScanned)
	}
}

// The declared tier list is what a coverage line prints, so it is normalized to
// schema order and never aliases the caller's slice.
func TestDeclaredTiersAreNormalized(t *testing.T) {
	asked := []schema.Tier{schema.TierResult, schema.TierConversation, schema.TierResult, "nonsense"}
	res := Search(nil, Query{Text: "x", Tiers: asked})

	want := []schema.Tier{schema.TierConversation, schema.TierResult}
	if len(res.Tiers) != len(want) || res.Tiers[0] != want[0] || res.Tiers[1] != want[1] {
		t.Fatalf("tiers %v, want %v", res.Tiers, want)
	}
	res.Tiers[0] = "rewritten"
	if asked[0] != schema.TierResult {
		t.Error("Result.Tiers aliases the caller's slice")
	}
	if unknown := Search(nil, Query{Text: "x", Tiers: []schema.Tier{"nonsense"}}); len(unknown.Tiers) != 1 ||
		unknown.Tiers[0] != schema.TierConversation {
		t.Errorf("unrecognised tiers alone declared %v, want the conversation default", unknown.Tiers)
	}
}

func TestSessionAndTurnCountsCoverBothHalves(t *testing.T) {
	turns := []schema.Turn{
		turn("s1", schema.TierConversation, "one"),
		turn("s1", schema.TierResult, "two"),
		turn("s2", schema.TierResult, "three"),
		turn("s1", schema.TierConversation, "four"),
		turn("s3", schema.TierConversation, "five"),
	}
	res := Search(turns, Query{Text: "nothing"})

	if res.Turns != 5 || res.TurnsScanned != 3 {
		t.Errorf("turns %d scanned %d, want 5 and 3", res.Turns, res.TurnsScanned)
	}
	if res.Sessions != 3 || res.SessionsScanned != 2 {
		t.Errorf("sessions %d scanned %d, want 3 and 2", res.Sessions, res.SessionsScanned)
	}
}

func TestStemExpansionWidensAndExactDoesNot(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet button")}

	if res := Search(turns, Query{Text: "wallets"}); len(res.Hits) != 1 {
		t.Errorf("expanded query: %d hits, want 1", len(res.Hits))
	} else if res.Hits[0].Length != 6 {
		t.Errorf("expanded hit length %d, want the stem's 6", res.Hits[0].Length)
	}
	if res := Search(turns, Query{Text: "wallets", Exact: true}); len(res.Hits) != 0 {
		t.Errorf("exact query: %d hits, want 0", len(res.Hits))
	}
}

func TestStemKeepsShortTermsWhole(t *testing.T) {
	cases := map[string]string{
		"wallets":   "wallet",
		"libraries": "librar",
		"changes":   "chang",
		"story":     "stor",
		"hits":      "hits",
		"used":      "used",
		"body":      "body",
		"cursor":    "cursor",
	}
	for in, want := range cases {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTiersFollowTheFlags(t *testing.T) {
	cases := []struct {
		results, tools bool
		want           []schema.Tier
	}{
		{false, false, []schema.Tier{schema.TierConversation}},
		{true, false, []schema.Tier{schema.TierConversation, schema.TierResult}},
		{false, true, []schema.Tier{schema.TierConversation, schema.TierInvocation}},
		{true, true, []schema.Tier{schema.TierConversation, schema.TierInvocation, schema.TierResult}},
	}
	for _, tc := range cases {
		got := Tiers(tc.results, tc.tools)
		if len(got) != len(tc.want) {
			t.Fatalf("Tiers(%v,%v) = %v, want %v", tc.results, tc.tools, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("Tiers(%v,%v) = %v, want %v", tc.results, tc.tools, got, tc.want)
			}
		}
	}
}
