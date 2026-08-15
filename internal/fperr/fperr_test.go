package fperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestNewFormatsAndCarriesCode(t *testing.T) {
	err := New(ArgError, "--max-bytes must be positive, got %d", -1)
	if err.Code != ArgError {
		t.Errorf("Code = %q, want %q", err.Code, ArgError)
	}
	if got, want := err.Error(), "--max-bytes must be positive, got -1"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if err.Exit != 0 {
		t.Errorf("Exit = %d, want 0 so main's table supplies the default", err.Exit)
	}
}

func TestWithExitOverridesTheDefault(t *testing.T) {
	err := WithExit(BadArchive, 9, "archive checksum mismatch")
	if err.Exit != 9 {
		t.Errorf("Exit = %d, want 9", err.Exit)
	}
	if err.Code != BadArchive {
		t.Errorf("Code = %q, want %q", err.Code, BadArchive)
	}
}

func TestErrorsAsRecoversTheCode(t *testing.T) {
	wrapped := fmt.Errorf("while scanning: %w", New(CorpusUnreadable, "cannot open store"))
	var coded *Error
	if !errors.As(wrapped, &coded) {
		t.Fatal("a wrapped fperr must still be recoverable, or the ERROR_CODE line is lost")
	}
	if coded.Code != CorpusUnreadable {
		t.Errorf("Code = %q, want %q", coded.Code, CorpusUnreadable)
	}
}

// Callers parse the slug, so the spellings are part of the contract.
func TestSlugsAreStableAndDistinct(t *testing.T) {
	want := map[Code]string{
		UnknownVerb:       "unknown_verb",
		ArgError:          "arg_error",
		NotFound:          "not_found",
		CorpusUnreadable:  "corpus_unreadable",
		SourceVanished:    "source_vanished",
		BadArchive:        "bad_archive",
		OutputTooLarge:    "output_too_large",
		AtomicWriteFailed: "atomic_write_failed",
		ToolMissing:       "tool_missing",
		Internal:          "internal_error",
	}
	seen := map[string]bool{}
	for code, slug := range want {
		if string(code) != slug {
			t.Errorf("code %q, want slug %q", code, slug)
		}
		if seen[slug] {
			t.Errorf("slug %q is used twice", slug)
		}
		seen[slug] = true
	}
}
