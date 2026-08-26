package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The sentence this guards against shipped a documented lie: recall already
// ranked a camelCase-boundary match as a whole word, not below one, and the
// guide told every agent reading it the opposite.
func TestGuideDoesNotClaimAnIdentifierMatchRanksBelowAWholeWord(t *testing.T) {
	for name, text := range map[string]string{"guideText": guideText, "guideBrief": guideBrief} {
		if strings.Contains(text, "ranked below a whole-word match") {
			t.Errorf("%s still claims an identifier match ranks below a whole word", name)
		}
		if !strings.Contains(text, "whole word") {
			t.Errorf("%s does not describe identifier-boundary ranking at all", name)
		}
	}
}

// Two semantics landed in earlier phases that an agent cannot discover on
// its own: a one-edit correction on a miss, and the shipped synonym table.
// Both must be named in both pages, along with the footer declaring them.
func TestGuidesDescribeNearNeighborCorrectionAndSynonymExpansion(t *testing.T) {
	for name, text := range map[string]string{"guideText": guideText, "guideBrief": guideBrief} {
		if !strings.Contains(text, "one-edit") {
			t.Errorf("%s does not describe one-edit near-neighbor correction", name)
		}
		if !strings.Contains(text, "two edits") {
			t.Errorf("%s does not distinguish a two-edit neighbor as suggested rather than substituted", name)
		}
		if !strings.Contains(text, "synonym") {
			t.Errorf("%s does not mention the shipped synonym table", name)
		}
		if !strings.Contains(text, "footer") {
			t.Errorf("%s does not say the footer names a correction or a synonym when one fires", name)
		}
	}
}

// The negative control for the two tests above: a query recall does not
// correct or expand must not be described as if it were.
func TestGuidesDoNotClaimLearnedOrPerCorpusSynonyms(t *testing.T) {
	for name, text := range map[string]string{"guideText": guideText, "guideBrief": guideBrief} {
		if strings.Contains(text, "learned") || strings.Contains(text, "per-corpus") {
			t.Errorf("%s claims a learned or per-corpus synonym table, which this build does not have", name)
		}
	}
}

// guideBrief exists to cut the per-session cost of reading the full guide,
// so its size relative to guideText is the deliverable, not a fixed byte
// count either would drift past.
func TestGuideBriefIsRoughlyAThirdOfGuideTextsSize(t *testing.T) {
	ratio := float64(len(guideBrief)) / float64(len(guideText))
	if ratio < 0.2 || ratio > 0.55 {
		t.Errorf("guideBrief is %d bytes against guideText's %d (ratio %.2f), want roughly a third",
			len(guideBrief), len(guideText), ratio)
	}
}

// guideBrief keeps the sections a caller cannot get elsewhere and drops the
// ones the tool descriptions, argument schemas, or the CLI itself already
// carry.
func TestGuideBriefKeepsLoadBearingSectionsAndDropsCLIOnlyOnes(t *testing.T) {
	for _, want := range []string{"HOW A QUERY IS READ", "WHAT IS SEARCHED", "EXIT CODES", "footer"} {
		if !strings.Contains(guideBrief, want) {
			t.Errorf("guideBrief is missing %q", want)
		}
	}
	for _, dontWant := range []string{"WHICH COMMAND", "NARROWING", "MACHINE FORMS", "RECIPES"} {
		if strings.Contains(guideBrief, dontWant) {
			t.Errorf("guideBrief still carries the CLI-only section %q", dontWant)
		}
	}
}

// recall guide --brief is the CLI surface for guideBrief; without it, the
// text exists but nothing reaches it.
func TestGuideBriefFlagPrintsTheCompactPage(t *testing.T) {
	var out bytes.Buffer
	if err := guide([]string{"--brief"}, &out); err != nil {
		t.Fatalf("guide --brief: %v", err)
	}
	if out.String() != guideBrief {
		t.Errorf("guide --brief printed something other than guideBrief")
	}

	var full bytes.Buffer
	if err := guide(nil, &full); err != nil {
		t.Fatalf("guide: %v", err)
	}
	if full.String() != guideText {
		t.Errorf("guide with no flag printed something other than guideText")
	}
}

// verbSearcher.Preamble is what internal/mcp's first-call mechanism calls;
// it must answer the same compact page --brief prints, not a third text.
func TestVerbSearcherPreambleAnswersGuideBrief(t *testing.T) {
	s := newVerbSearcher(nil)
	got, err := s.Preamble(context.Background())
	if err != nil {
		t.Fatalf("Preamble: %v", err)
	}
	if got != guideBrief {
		t.Errorf("Preamble returned %d bytes, want guideBrief verbatim", len(got))
	}
}
