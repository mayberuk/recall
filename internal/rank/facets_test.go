package rank_test

import (
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/rank"
	"github.com/mayberuk/recall/internal/schema"
)

func TestFacetsCountHitsAndTheSessionsThoseHitsFallIn(t *testing.T) {
	hits := []schema.Hit{
		hit("s1", "u1", repo(fixtures.RepoRemote)),
		hit("s1", "u2", repo(fixtures.RepoRemote)),
		hit("s2", "u3", repo(fixtures.RepoRemote)),
		hit("s3", "u4", repo(fixtures.RepoNoRemote)),
	}

	got := rank.Rank(hits, map[string]int{"s1": 10, "s2": 10, "s3": 10}, rank.Concentration).Facets

	want := []rank.Facet{
		{Value: fixtures.RepoRemote, Hits: 3, Sessions: 2},
		{Value: fixtures.RepoNoRemote, Hits: 1, Sessions: 1},
	}
	assertFacets(t, "repo", got.Repo, want)
}

func TestFacetsCoverAuthorTierAndBranch(t *testing.T) {
	hits := []schema.Hit{
		hit("s", "u1", author(schema.AuthorHuman), branch("main")),
		hit("s", "u2", author(schema.AuthorAgent), tier(schema.TierResult), branch("main")),
		hit("s", "u3", author(schema.AuthorAgent), tier(schema.TierResult), branch("feature")),
	}

	got := rank.Rank(hits, map[string]int{"s": 10}, rank.Concentration).Facets

	assertFacets(t, "author", got.Author, []rank.Facet{
		{Value: string(schema.AuthorAgent), Hits: 2, Sessions: 1},
		{Value: string(schema.AuthorHuman), Hits: 1, Sessions: 1},
	})
	assertFacets(t, "tier", got.Tier, []rank.Facet{
		{Value: string(schema.TierResult), Hits: 2, Sessions: 1},
		{Value: string(schema.TierConversation), Hits: 1, Sessions: 1},
	})
	assertFacets(t, "branch", got.Branch, []rank.Facet{
		{Value: "main", Hits: 2, Sessions: 1},
		{Value: "feature", Hits: 1, Sessions: 1},
	})
}

// TestFacetsKeepAnAbsentFieldValue holds the line against inventing a label:
// a record with no gitBranch is counted under the empty value, where a renderer
// can name it, rather than dropped from the summary.
func TestFacetsKeepAnAbsentFieldValue(t *testing.T) {
	hits := []schema.Hit{
		hit("s", "u1", branch("")),
		hit("s", "u2", branch("main")),
	}

	got := rank.Rank(hits, map[string]int{"s": 10}, rank.Concentration).Facets

	assertFacets(t, "branch", got.Branch, []rank.Facet{
		{Value: "", Hits: 1, Sessions: 1},
		{Value: "main", Hits: 1, Sessions: 1},
	})
}

func TestFacetsCountEachRecordOnceAcrossFiles(t *testing.T) {
	m := fixtures.Materialize(t).Manifest
	dup := m.DupUUIDs[0]
	copied := hit(dup.Session, dup.UUID, author(schema.AuthorHuman))

	got := rank.Rank([]schema.Hit{copied, copied}, map[string]int{dup.Session: 30}, rank.Concentration).Facets

	assertFacets(t, "author", got.Author, []rank.Facet{
		{Value: string(schema.AuthorHuman), Hits: 1, Sessions: 1},
	})
}

// assertFacets compares in order: facets are sorted by hits then value, since a
// summary printed above the results must not reshuffle between runs.
func assertFacets(t *testing.T, name string, got, want []rank.Facet) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s facets %+v, want %+v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s facet %d is %+v, want %+v", name, i, got[i], want[i])
		}
	}
}
