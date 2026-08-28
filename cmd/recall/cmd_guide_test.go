package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

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

func TestGuidesDoNotClaimLearnedOrPerCorpusSynonyms(t *testing.T) {
	for name, text := range map[string]string{"guideText": guideText, "guideBrief": guideBrief} {
		if strings.Contains(text, "learned") || strings.Contains(text, "per-corpus") {
			t.Errorf("%s claims a learned or per-corpus synonym table, which this build does not have", name)
		}
	}
}

// The ratio, not a fixed byte count, is the deliverable — either text drifts.
func TestGuideBriefIsRoughlyAThirdOfGuideTextsSize(t *testing.T) {
	ratio := float64(len(guideBrief)) / float64(len(guideText))
	if ratio < 0.2 || ratio > 0.55 {
		t.Errorf("guideBrief is %d bytes against guideText's %d (ratio %.2f), want roughly a third",
			len(guideBrief), len(guideText), ratio)
	}
}

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
