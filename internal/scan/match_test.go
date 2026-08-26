package scan

import (
	"reflect"
	"testing"

	"github.com/mayberuk/recall/internal/schema"
)

func TestClassifyReadsCaseFromRawNotFolded(t *testing.T) {
	cases := []struct {
		name           string
		raw            string
		offset, length int
		want           schema.MatchKind
	}{
		{"non-leading segment, raw has the hump", "rateLimiter", 4, 7, schema.MatchWord},
		{"same span, no case in raw", "ratelimiter", 4, 7, schema.MatchInside},
		{"interior substring, no transition at either edge", "know", 1, 2, schema.MatchInside},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			folded := fold(nil, tc.raw)
			if got := classify(folded, raw, tc.offset, tc.length); got != tc.want {
				t.Errorf("classify(%q, %d, %d) = %q, want %q", tc.raw, tc.offset, tc.length, got, tc.want)
			}
		})
	}
}

func TestATermIsFoundAndLocatedThroughAnyOfItsNeedles(t *testing.T) {
	typed := newTerm(rawTerm{text: "settlemint"}, true)
	widened := typed
	widened.alt = []variant{newVariant("settlement", false), newVariant("batch", false)}

	for _, c := range []struct {
		name  string
		t     term
		text  string
		found bool
		index int
	}{
		{"typed needle only, present", typed, "the settlemint cleared", true, 4},
		{"typed needle only, absent", typed, "the settlement cleared", false, -1},
		{"substituted needle present", widened, "the settlement cleared", true, 4},
		{"neither needle present", widened, "nothing of the sort", false, -1},
		{"earliest needle wins, not the first listed", widened, "the batch and the settlement", true, 4},
		{"typed needle behind a substituted one", widened, "batch then settlemint", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			folded := fold(nil, c.text)
			if got := c.t.found(folded); got != c.found {
				t.Errorf("found(%q) = %v, want %v", c.text, got, c.found)
			}
			if got := c.t.index(folded); got != c.index {
				t.Errorf("index(%q) = %d, want %d", c.text, got, c.index)
			}
		})
	}
}

func TestNewTermAddsSynonymVariantsForATableWord(t *testing.T) {
	long := newTerm(rawTerm{text: "database"}, false)
	if len(long.alt) != 1 || string(long.alt[0].needle) != "db" {
		t.Fatalf("database.alt = %+v, want one variant needle \"db\"", long.alt)
	}
	if !long.found(fold(nil, "back up the db before the migration")) {
		t.Error("a term for \"database\" did not find a turn carrying only \"db\"")
	}

	short := newTerm(rawTerm{text: "db"}, false)
	if len(short.alt) != 1 || string(short.alt[0].needle) != "database" {
		t.Fatalf("db.alt = %+v, want one variant needle \"database\"", short.alt)
	}
}

func TestNewTermAddsNoAltForAWordNotInTheTable(t *testing.T) {
	if got := newTerm(rawTerm{text: "wallet"}, false); len(got.alt) != 0 {
		t.Errorf("wallet.alt = %+v, want none", got.alt)
	}
}

func TestNewTermSuppressesSynonymsUnderExactAndInAPhrase(t *testing.T) {
	if got := newTerm(rawTerm{text: "database"}, true); len(got.alt) != 0 {
		t.Errorf("database.alt under --exact = %+v, want none", got.alt)
	}
	if got := newTerm(rawTerm{text: "database", quoted: true}, false); len(got.alt) != 0 {
		t.Errorf("database.alt inside a quoted phrase = %+v, want none", got.alt)
	}
}

// collect needs the expanded signal to sort one term's spans: two needles can
// otherwise return them out of offset order.
func TestCompileMarksTheMatcherExpandedForATableWord(t *testing.T) {
	if m := compile(Query{Text: "database"}); !m.expanded {
		t.Error("compiling a table word did not set matcher.expanded")
	}
	if m := compile(Query{Text: "wallet"}); m.expanded {
		t.Error("compiling a non-table word set matcher.expanded")
	}
}

func TestASynonymReachesATurnTheTypedSpellingNeverUses(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "back up the db before the migration runs")}
	res := Search(turns, Query{Text: "database"})
	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1 — the turn only ever says \"db\"", len(res.Hits))
	}
	want := []Expansion{{Term: "database", Variants: []string{"db"}, Synonym: true}}
	if !reflect.DeepEqual(res.Match.Expanded, want) {
		t.Errorf("Expanded %+v, want %+v", res.Match.Expanded, want)
	}

	turns2 := []schema.Turn{turn("s1", schema.TierConversation, "restore the database from the nightly snapshot")}
	res2 := Search(turns2, Query{Text: "db"})
	if len(res2.Hits) != 1 {
		t.Fatalf("%d hits, want 1 — the turn only ever says \"database\"", len(res2.Hits))
	}
	want2 := []Expansion{{Term: "db", Variants: []string{"database"}, Synonym: true}}
	if !reflect.DeepEqual(res2.Match.Expanded, want2) {
		t.Errorf("Expanded %+v, want %+v", res2.Match.Expanded, want2)
	}
}

// "id" inside "video" is not a hit: a needle the caller never typed earns a
// match only where it stands as its own word. Dropping that rule inflated
// "identifier" from 141 hits to 49,524.
func TestSynonymNeedleInsideALongerWordIsNotAHit(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "check the video for calibration before the demo")}
	res := Search(turns, Query{Text: "identifier"})
	if len(res.Hits) != 0 {
		t.Fatalf("%d hits, want 0 — \"id\" only ever sits inside \"video\", never as its own word", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want none — the interior match must not be declared as a search that contributed", res.Match.Expanded)
	}
}

func TestSynonymNeedleAsAStandaloneWordIsAHit(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "grab the id from the response before you log it")}
	res := Search(turns, Query{Text: "identifier"})
	if len(res.Hits) != 1 {
		t.Fatalf("%d hits, want 1 — \"id\" appears as its own word", len(res.Hits))
	}
	want := []Expansion{{Term: "identifier", Variants: []string{"id"}, Synonym: true}}
	if !reflect.DeepEqual(res.Match.Expanded, want) {
		t.Errorf("Expanded %+v, want %+v", res.Match.Expanded, want)
	}
}

func TestExactSuppressesSynonymExpansion(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "back up the db before the migration runs")}
	res := Search(turns, Query{Text: "database", Exact: true})
	if len(res.Hits) != 0 {
		t.Errorf("%d hits under --exact, want 0 — \"database\" never appears literally", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v under --exact, want none", res.Match.Expanded)
	}
}

func TestQuotedPhraseSuppressesSynonymExpansion(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "back up the db before the migration runs")}
	res := Search(turns, Query{Text: `"database"`})
	if len(res.Hits) != 0 {
		t.Errorf("%d hits for a quoted phrase, want 0 — \"database\" never appears literally", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v for a quoted phrase, want none", res.Match.Expanded)
	}
}

func TestANonTableWordAddsNoExpansion(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet button")}
	res := Search(turns, Query{Text: "wallet"})
	if len(res.Hits) == 0 {
		t.Fatal("0 hits for a term the turn carries literally")
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want none — \"wallet\" is not a table word", res.Match.Expanded)
	}
}

func TestASynonymThatMatchesNothingReportsNoExpansion(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet button")}
	res := Search(turns, Query{Text: "database"})
	if len(res.Hits) != 0 {
		t.Fatalf("%d hits, want 0 — neither \"database\" nor \"db\" appears", len(res.Hits))
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want none on a total miss", res.Match.Expanded)
	}
}

func TestASynonymTermAbsentFromTheReturnedTurnsIsNotDeclared(t *testing.T) {
	turns := []schema.Turn{turn("s1", schema.TierConversation, "the wallet button")}
	res := Search(turns, Query{Text: "database wallet"})
	if len(res.Hits) == 0 {
		t.Fatal("0 hits, want the relaxed match on \"wallet\" alone")
	}
	if len(res.Match.Expanded) != 0 {
		t.Errorf("Expanded %+v, want none — the returned turn carries only \"wallet\"", res.Match.Expanded)
	}
}

// caseBoundary in isolation, including the guards that keep it inert outside
// ASCII and off invalid indices.
func TestCaseBoundary(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		i    int
		want bool
	}{
		{"camel hump: lower then upper", "eL", 1, true},
		{"camel hump: digit then upper", "2L", 1, true},
		{"acronym to word: upper, upper, lower follows", "PSe", 1, true},
		// A mutation dropping the look-ahead would misclassify this as a boundary too.
		{"all-caps run: upper, upper, upper follows", "ABC", 1, false},
		{"letter after digit", "2t", 1, true},
		{"digit after letter", "t2", 1, true},
		{"no transition: two lower-case letters", "no", 1, false},
		{"start of buffer: no byte to compare against", "ab", 0, false},
		{"end of buffer: no byte at i", "ab", 2, false},
		// A UTF-8 continuation byte must never be read as a case boundary.
		{"byte before i is part of a multi-byte rune", "caféBar", 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := caseBoundary([]byte(tc.raw), tc.i); got != tc.want {
				t.Errorf("caseBoundary(%q, %d) = %v, want %v", tc.raw, tc.i, got, tc.want)
			}
		})
	}
}

// rawBytes's correctness argument: classify must never diverge between it and
// a plain []byte(s) copy.
func FuzzClassifyAgreesWithASafeCopy(f *testing.F) {
	f.Add("rateLimiter", 4, 7)
	f.Add("HTTPServer", 4, 6)
	f.Add("v2beta", 2, 4)
	f.Add("caféBar", 5, 3)
	f.Add("", 0, 0)

	f.Fuzz(func(t *testing.T, s string, offset, length int) {
		// Normalized into range: not a shape collect would ever hand classify.
		if len(s) == 0 {
			offset, length = 0, 0
		} else {
			offset = ((offset % (len(s) + 1)) + (len(s) + 1)) % (len(s) + 1)
			length = ((length % (len(s) - offset + 1)) + (len(s) - offset + 1)) % (len(s) - offset + 1)
		}
		folded := fold(nil, s)
		want := classify(folded, []byte(s), offset, length)
		if got := classify(folded, rawBytes(s), offset, length); got != want {
			t.Fatalf("classify(rawBytes(%q), %d, %d) = %q, want %q (safe copy)", s, offset, length, got, want)
		}
	})
}
