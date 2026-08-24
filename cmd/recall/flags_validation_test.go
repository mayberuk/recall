package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
)

// Every searching verb validates its globals and its search flags before it
// ever touches the archive, so these run with no corpus at all. Each one
// pins that the verb's own flag-validation error surfaces rather than a
// generic one, and that it happens before any I/O.

func TestFindRejectsInvalidGlobalsBeforeOpeningTheArchive(t *testing.T) {
	var out, errOut bytes.Buffer
	err := find([]string{"agvtool", "--max-bytes", "0"}, &out, &errOut)
	requireArgError(t, err)
}

func TestFindRejectsInvalidSearchFlagsBeforeOpeningTheArchive(t *testing.T) {
	var out, errOut bytes.Buffer
	err := find([]string{"agvtool", "--limit", "0"}, &out, &errOut)
	requireArgError(t, err)
}

func TestFindRejectsFZFCombinedWithAnotherFormat(t *testing.T) {
	var out, errOut bytes.Buffer
	err := find([]string{"agvtool", "--fzf", "--json"}, &out, &errOut)
	requireArgError(t, err)
}

func TestFindRejectsAnEmptyQuery(t *testing.T) {
	var out, errOut bytes.Buffer
	err := find([]string{"--all"}, &out, &errOut)
	requireArgError(t, err)
}

func TestShowRejectsInvalidGlobalsBeforeOpeningTheArchive(t *testing.T) {
	var out bytes.Buffer
	err := show([]string{"5fd86b00", "--max-bytes", "0"}, &out)
	requireArgError(t, err)
}

func TestShowRejectsANegativeAround(t *testing.T) {
	var out bytes.Buffer
	err := show([]string{"5fd86b00", "--around", "-1"}, &out)
	requireArgError(t, err)
}

func TestShowRejectsANegativeChars(t *testing.T) {
	var out bytes.Buffer
	err := show([]string{"5fd86b00", "--chars", "-1"}, &out)
	requireArgError(t, err)
}

func TestShowRejectsNoSessionID(t *testing.T) {
	var out bytes.Buffer
	err := show(nil, &out)
	requireArgError(t, err)
}

func TestTurnsRejectsInvalidGlobalsBeforeOpeningTheArchive(t *testing.T) {
	var out bytes.Buffer
	err := turns([]string{"agvtool", "--max-bytes", "0"}, &out)
	requireArgError(t, err)
}

func TestTurnsRejectsInvalidSearchFlagsBeforeOpeningTheArchive(t *testing.T) {
	var out bytes.Buffer
	err := turns([]string{"agvtool", "--limit", "0"}, &out)
	requireArgError(t, err)
}

func TestTurnsRejectsANegativeChars(t *testing.T) {
	var out bytes.Buffer
	err := turns([]string{"agvtool", "--chars", "-1"}, &out)
	requireArgError(t, err)
}

func TestTurnsRejectsAnEmptyQuery(t *testing.T) {
	var out bytes.Buffer
	err := turns([]string{"--all"}, &out)
	requireArgError(t, err)
}

func TestWhenRejectsInvalidGlobalsBeforeOpeningTheArchive(t *testing.T) {
	var out bytes.Buffer
	err := when([]string{"agvtool", "--max-bytes", "0"}, &out)
	requireArgError(t, err)
}

func TestWhenRejectsInvalidSearchFlagsBeforeOpeningTheArchive(t *testing.T) {
	var out bytes.Buffer
	err := when([]string{"agvtool", "--limit", "0"}, &out)
	requireArgError(t, err)
}

func TestWhenRejectsAnEmptyQuery(t *testing.T) {
	var out bytes.Buffer
	err := when([]string{"--all"}, &out)
	requireArgError(t, err)
}

func TestDoctorRejectsInvalidGlobalsBeforeOpeningTheArchive(t *testing.T) {
	var out, errOut bytes.Buffer
	err := doctor([]string{"--max-bytes", "0"}, &out, &errOut)
	requireArgError(t, err)
}

func TestGuideRejectsAnUnknownFlag(t *testing.T) {
	var out bytes.Buffer
	err := guide([]string{"--no-such-flag"}, &out)
	requireArgError(t, err)
}

func requireArgError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("invalid input was accepted")
	}
	var coded *fperr.Error
	if !errors.As(err, &coded) || coded.Code != fperr.ArgError {
		t.Errorf("error = %v, want code %s", err, fperr.ArgError)
	}
}
