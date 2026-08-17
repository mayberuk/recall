package archive

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mayberuk/recall/internal/fixtures"
	"github.com/mayberuk/recall/internal/jsonl"
	"github.com/mayberuk/recall/internal/schema"
)

// framed wraps hand-built frame bytes in a tier file's header, declaring one
// block over them, so a test that needs a deliberately malformed body does not
// have to restate the header format to get there.
func framed(body []byte, turns int) []byte {
	b := append([]byte(tierMagic), 1)
	b = binary.AppendUvarint(b, 0)
	b = binary.AppendUvarint(b, uint64(turns))
	return append(b, body...)
}

// stubStrip stands in for internal/strip, which is built in parallel with this
// package. It keeps the two properties the archive depends on: one record can
// yield several turns, and a record that yields none still consumes its uuid.
func stubStrip(rec jsonl.Record) ([]schema.Turn, bool) {
	typ := rec.Type()
	if typ != "user" && typ != "assistant" {
		return nil, false
	}
	base := schema.Turn{
		Session: rec.SessionID(),
		UUID:    rec.UUID(),
		TS:      rec.Timestamp(),
		Tier:    schema.TierConversation,
		Author:  stubAuthor(rec, typ),
		Agent:   rec.AgentID(),
		Branch:  rec.GitBranch(),
		CWD:     rec.CWD(),
	}

	content := rec.Message().Get("content")
	if !content.IsArray() {
		if content.String() == "" {
			return nil, false
		}
		base.Text = content.String()
		return []schema.Turn{base}, true
	}

	var turns []schema.Turn
	for _, block := range content.Array() {
		var text string
		switch block.Get("type").String() {
		case "text":
			text = block.Get("text").String()
		case "thinking":
			text = block.Get("thinking").String()
		}
		if text == "" {
			continue
		}
		t := base
		t.Text = text
		turns = append(turns, t)
	}
	return turns, len(turns) > 0
}

func stubAuthor(rec jsonl.Record, typ string) schema.Author {
	switch {
	case rec.IsSidechain():
		return schema.AuthorAgent
	case typ == "assistant":
		return schema.AuthorAssistant
	}
	if src, ok := rec.PromptSource(); ok && src == "typed" {
		return schema.AuthorHuman
	}
	return schema.AuthorSystem
}

func stubResolve(cwd string) string {
	if cwd == "" {
		return ""
	}
	return "repo(" + filepath.Base(cwd) + ")"
}

func newStore(t *testing.T, root string) *Store {
	t.Helper()
	s, err := Open(Options{Dir: t.TempDir(), Root: root, Strip: stubStrip, Resolve: stubResolve})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustUpdate(t *testing.T, s *Store) Result {
	t.Helper()
	res, err := s.Update()
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	return res
}

func mustTurns(t *testing.T, s *Store) []schema.Turn {
	t.Helper()
	turns, err := s.Turns()
	if err != nil {
		t.Fatalf("Turns: %v", err)
	}
	return turns
}

// archived concatenates every tier file, so a byte-identity assertion covers
// the whole archive rather than one tier of it.
func archived(t *testing.T, s *Store) []byte {
	t.Helper()
	var out []byte
	for _, tier := range tierFiles {
		b, err := os.ReadFile(s.TierPath(tier))
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read the %s tier: %v", tier, err)
		}
		out = append(out, b...)
	}
	return out
}

// storeFiles digests every file in the store directory. a9's clause is over the
// archive content, which is all of them and not the tier files alone.
func storeFiles(t *testing.T, s *Store) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("read %s: %v", s.Dir(), err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir(), e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		sum := sha256.Sum256(data)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

// sameFiles reports every file that differs, so a failure names them rather than
// saying only that something moved.
func sameFiles(t *testing.T, what string, a, b map[string]string) {
	t.Helper()
	for name, digest := range a {
		switch other, present := b[name]; {
		case !present:
			t.Errorf("%s: %s disappeared", what, name)
		case other != digest:
			t.Errorf("%s: %s changed content (%s -> %s)", what, name, digest[:16], other[:16])
		}
	}
	for name := range b {
		if _, present := a[name]; !present {
			t.Errorf("%s: %s appeared", what, name)
		}
	}
	if len(a) < 5 {
		t.Errorf("%s: only %d files digested (%v); the store writes three tiers, a cursor, metadata and checksums",
			what, len(a), slices.Sorted(maps.Keys(a)))
	}
}

func tierBytes(t *testing.T, s *Store, tier schema.Tier) []byte {
	t.Helper()
	b, err := os.ReadFile(s.TierPath(tier))
	if err != nil {
		t.Fatalf("read the %s tier: %v", tier, err)
	}
	return b
}

// appendRecord adds one well-formed transcript line to a fixture file.
func appendRecord(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(body + "\n"); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

func corpus(t *testing.T) fixtures.Corpus {
	t.Helper()
	return fixtures.Materialize(t)
}

// tinyCorpus writes a throwaway store of hand-written transcripts. The shared
// fixture has no uuid spanning two sessions, so the fork case is built here
// rather than added to internal/fixtures.
func tinyCorpus(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}
