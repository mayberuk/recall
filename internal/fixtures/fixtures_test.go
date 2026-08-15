package fixtures

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mayberuk/recall/internal/jsonl"
)

// readAll walks the materialized corpus with the scaffold's own reader, which is
// the only parser any package is allowed to use on raw transcripts.
func readAll(t *testing.T, c Corpus) (recs []jsonl.Record, files []string, tally jsonl.Tally) {
	t.Helper()
	err := filepath.Walk(c.Root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		rel, _ := filepath.Rel(c.Root, path)
		files = append(files, rel)
		r, err := jsonl.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		for r.Next() {
			rec, ok := r.Record()
			if !ok {
				tally.ObserveMalformed()
				continue
			}
			tally.Observe(rec)
			// Values survive the buffer; the line bytes do not, so re-parse from a copy.
			cp := append([]byte(nil), rec.Raw()...)
			held, _ := jsonl.Parse(jsonl.Line{Bytes: cp, Length: len(cp)})
			recs = append(recs, held)
		}
		return r.Err()
	})
	if err != nil {
		t.Fatalf("walking the corpus: %v", err)
	}
	return recs, files, tally
}

func TestMaterializeMatchesManifest(t *testing.T) {
	c := Materialize(t)
	m := c.Manifest
	recs, files, tally := readAll(t, c)

	t.Run("every manifest file exists", func(t *testing.T) {
		for _, rel := range []string{
			FileNeedle, FileSubagent, FileDupA, FileDupB, FileMultiSession,
			FileUnknownType, FileNoPromptSource, FileEmptyThinking, FileHugeResult,
			FileOrphan, FileRemoteless, FileSubdir, FileRelocated, FileSkew,
		} {
			if _, err := os.Stat(c.Path(rel)); err != nil {
				t.Errorf("%s: %v", rel, err)
			}
		}
		if len(files) != len(m.Sessions)+1 {
			t.Errorf("%d files for %d sessions plus one subagent transcript", len(files), len(m.Sessions))
		}
	})

	t.Run("sessions are not files", func(t *testing.T) {
		ids := map[string]bool{}
		for _, rec := range recs {
			if id := rec.SessionID(); id != "" {
				ids[id] = true
			}
		}
		if len(ids) != len(m.Sessions) {
			t.Errorf("%d distinct sessionIds, want %d", len(ids), len(m.Sessions))
		}
		for _, want := range m.Sessions {
			if !ids[want] {
				t.Errorf("session %s is missing from the corpus", want)
			}
		}
		if len(m.MultiSessionIDs) != 2 {
			t.Fatalf("manifest names %d ids in the multi-session file, want 2", len(m.MultiSessionIDs))
		}
		inFile := map[string]bool{}
		for _, rec := range recordsIn(t, c, m.MultiSessionFile) {
			inFile[rec.SessionID()] = true
		}
		for _, want := range m.MultiSessionIDs {
			if !inFile[want] {
				t.Errorf("%s does not carry session %s", m.MultiSessionFile, want)
			}
		}
	})

	t.Run("needles are planted where the manifest says", func(t *testing.T) {
		for _, n := range m.Needles {
			hits := 0
			for _, rec := range recs {
				if strings.Contains(string(rec.Raw()), n.Token) {
					hits++
					if rec.UUID() != n.UUID {
						t.Errorf("%s: found on uuid %s, manifest says %s", n.Token, rec.UUID(), n.UUID)
					}
					if id := rec.SessionID(); id != n.Session {
						t.Errorf("%s: found in session %s, manifest says %s", n.Token, id, n.Session)
					}
				}
			}
			if hits != len(n.Files) {
				t.Errorf("%s appears in %d records, manifest names %d file(s)", n.Token, hits, len(n.Files))
			}
			for _, f := range n.Files {
				if !fileContains(t, c.Path(f), n.Token) {
					t.Errorf("%s is not in %s", n.Token, f)
				}
			}
		}
	})

	t.Run("duplicated uuids sit in two files", func(t *testing.T) {
		for _, d := range m.DupUUIDs {
			if len(d.Files) != 2 {
				t.Fatalf("%s: manifest names %d files, want 2", d.UUID, len(d.Files))
			}
			for _, f := range d.Files {
				if !fileContains(t, c.Path(f), d.UUID) {
					t.Errorf("uuid %s is not in %s", d.UUID, f)
				}
			}
		}
		seen := map[string]int{}
		for _, rec := range recs {
			if u := rec.UUID(); u != "" {
				seen[u]++
			}
		}
		for _, d := range m.DupUUIDs {
			if seen[d.UUID] != 2 {
				t.Errorf("uuid %s appears %d times, want 2 — the dedup fixture is gone", d.UUID, seen[d.UUID])
			}
		}
	})

	t.Run("human turns", func(t *testing.T) {
		records, distinct := 0, map[string]bool{}
		args := 0
		for _, rec := range recs {
			if src, ok := rec.PromptSource(); ok && src == "typed" {
				records++
				distinct[rec.UUID()] = true
			}
			body := rec.Message().Get("content").String()
			if i := strings.Index(body, "<command-args>"); i >= 0 {
				if j := strings.Index(body, "</command-args>"); j > i+len("<command-args>") {
					args++
				}
			}
		}
		if records != m.TypedTurnRecords {
			t.Errorf("%d typed records, manifest says %d", records, m.TypedTurnRecords)
		}
		if len(distinct) != m.TypedTurns {
			t.Errorf("%d distinct typed uuids, manifest says %d", len(distinct), m.TypedTurns)
		}
		if args != m.CommandArgTurns {
			t.Errorf("%d non-empty command-args records, manifest says %d", args, m.CommandArgTurns)
		}
		if m.HumanTurns != m.TypedTurns+m.CommandArgTurns {
			t.Errorf("HumanTurns %d != TypedTurns %d + CommandArgTurns %d",
				m.HumanTurns, m.TypedTurns, m.CommandArgTurns)
		}
		if records == len(distinct) {
			t.Error("no typed record is duplicated, so nothing pins uuid dedup for turn counts")
		}
	})

	t.Run("unknown record types are countable", func(t *testing.T) {
		got := tally.Unknown
		if len(got) != len(m.UnknownTypes) {
			t.Errorf("tally saw %v, manifest says %v", got, m.UnknownTypes)
		}
		for typ, want := range m.UnknownTypes {
			if got[typ] != want {
				t.Errorf("unknown type %q counted %d, manifest says %d", typ, got[typ], want)
			}
		}
		if tally.Malformed != 0 {
			t.Errorf("%d malformed lines in the corpus, want 0", tally.Malformed)
		}
	})

	t.Run("empty thinking carries a long signature", func(t *testing.T) {
		found := false
		for _, rec := range recordsIn(t, c, FileEmptyThinking) {
			for _, b := range rec.Message().Get("content").Array() {
				if b.Get("type").String() != "thinking" {
					continue
				}
				found = true
				if b.Get("thinking").String() != "" {
					t.Error("thinking text is not empty; 94.5% of real blocks are")
				}
				if got := len(b.Get("signature").String()); got < 1000 {
					t.Errorf("signature is %d bytes; the pathology is that it dwarfs the text", got)
				}
			}
		}
		if !found {
			t.Error("no thinking block in the empty-thinking fixture")
		}
	})

	t.Run("huge result is over 20 KB", func(t *testing.T) {
		var got int
		for _, rec := range recordsIn(t, c, FileHugeResult) {
			for _, b := range rec.Message().Get("content").Array() {
				if b.Get("type").String() == "tool_result" {
					got = len(b.Get("content").String())
				}
			}
		}
		if got != m.HugeResultBytes {
			t.Errorf("tool result is %d bytes, manifest says %d", got, m.HugeResultBytes)
		}
		if got <= 20<<10 {
			t.Errorf("tool result is %d bytes; the fixture exists to exceed 20 KB", got)
		}
	})

	t.Run("subagent transcript is a sidechain under subagents/", func(t *testing.T) {
		if !strings.Contains(FileSubagent, "/subagents/") {
			t.Errorf("%s is not under a subagents/ directory", FileSubagent)
		}
		any := false
		for _, rec := range recordsIn(t, c, FileSubagent) {
			any = true
			if !rec.IsSidechain() {
				t.Errorf("record %s is not isSidechain", rec.UUID())
			}
			if rec.SessionID() != SessNeedle {
				t.Errorf("subagent record folds into session %s, want %s", rec.SessionID(), SessNeedle)
			}
		}
		if !any {
			t.Error("the subagent transcript is empty")
		}
	})

	t.Run("relocated record carries relocatedCwd and no cwd", func(t *testing.T) {
		found := false
		for _, rec := range recordsIn(t, c, FileRelocated) {
			if rec.Type() != "relocated" {
				continue
			}
			found = true
			if rec.CWD() != "" {
				t.Errorf("cwd = %q, want empty", rec.CWD())
			}
			if got, want := rec.RelocatedCWD(), c.ScratchPath(ScratchGone); got != want {
				t.Errorf("relocatedCwd = %q, want %q", got, want)
			}
		}
		if !found {
			t.Error("no relocated record in the relocated fixture")
		}
	})

	t.Run("scratch token is fully substituted", func(t *testing.T) {
		for _, rel := range files {
			if fileContains(t, c.Path(rel), ScratchToken) {
				t.Errorf("%s still holds %s", rel, ScratchToken)
			}
		}
	})

	t.Run("mtime skew is 55 days", func(t *testing.T) {
		fi, err := os.Stat(c.Path(m.SkewFile))
		if err != nil {
			t.Fatal(err)
		}
		content, err := time.Parse(time.RFC3339, m.SkewContentTS)
		if err != nil {
			t.Fatal(err)
		}
		gap := fi.ModTime().UTC().Sub(content)
		if want := time.Duration(m.SkewDays) * 24 * time.Hour; gap != want {
			t.Errorf("mtime is %v after the content date, want %v", gap, want)
		}
		for _, rec := range recordsIn(t, c, m.SkewFile) {
			if ts := rec.Timestamp(); ts != "" && !strings.HasPrefix(ts, "2026-06-07") {
				t.Errorf("skew record timestamp %q is not the June content date", ts)
			}
		}
	})
}

func TestMaterializeBuildsAllFourGitShapes(t *testing.T) {
	c := Materialize(t)

	remoteOf := func(dir string) (string, error) {
		cmd := exec.Command("git", "remote", "get-url", "origin")
		cmd.Dir = dir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	toplevelOf := func(dir string) (string, error) {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		cmd.Dir = dir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}

	t.Run("normal repo has an origin", func(t *testing.T) {
		got, err := remoteOf(c.ScratchPath(ScratchNormal))
		if err != nil {
			t.Fatal(err)
		}
		if got != OriginURL {
			t.Errorf("origin = %q, want %q", got, OriginURL)
		}
	})

	t.Run("subdirectory resolves to the same repo", func(t *testing.T) {
		got, err := toplevelOf(c.ScratchPath(ScratchAndroid))
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(c.ScratchPath(ScratchNormal))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("toplevel = %q, want %q", got, want)
		}
	})

	// This is the pathology: the .git file survives, the gitdir it names does
	// not, and git fails with 128 rather than reporting "not a repository".
	t.Run("orphan worktree has a dangling gitdir pointer", func(t *testing.T) {
		orphan := c.ScratchPath(ScratchOrphan)
		dotgit, err := os.ReadFile(filepath.Join(orphan, ".git"))
		if err != nil {
			t.Fatalf("the orphan worktree has no .git file: %v", err)
		}
		pointer := strings.TrimSpace(strings.TrimPrefix(string(dotgit), "gitdir:"))
		if _, err := os.Stat(pointer); !os.IsNotExist(err) {
			t.Errorf("gitdir %q still exists, so the fixture pins nothing", pointer)
		}
		if _, err := remoteOf(orphan); err == nil {
			t.Error("git remote get-url origin succeeded in the orphaned worktree")
		} else if code := exitCode(err); code != 128 {
			t.Errorf("git exited %d, want 128 — the measured failure mode", code)
		}
	})

	t.Run("remoteless repo is a repo with no origin", func(t *testing.T) {
		dir := c.ScratchPath(ScratchRemoteless)
		if _, err := toplevelOf(dir); err != nil {
			t.Fatalf("not a git repo: %v", err)
		}
		if got, err := remoteOf(dir); err == nil {
			t.Errorf("origin = %q, want none", got)
		}
	})

	t.Run("relocated cwd is not on disk", func(t *testing.T) {
		if _, err := os.Stat(c.ScratchPath(ScratchGone)); !os.IsNotExist(err) {
			t.Errorf("%s exists; the relocated fixture needs a path that does not", ScratchGone)
		}
	})

	t.Run("every manifest shape names a session in the corpus", func(t *testing.T) {
		for _, s := range c.Manifest.CWDShapes {
			found := false
			for _, id := range c.Manifest.Sessions {
				if id == s.Session {
					found = true
				}
			}
			if !found {
				t.Errorf("shape %s names session %s, which the corpus does not contain", s.Name, s.Session)
			}
			if s.Identity == RepoRemote && s.Remote == "" {
				t.Errorf("shape %s resolves to a remote identity with no remote recorded", s.Name)
			}
			if s.Identity == RepoNoRemote && s.Toplevel == "" {
				t.Errorf("shape %s is remoteless but has no toplevel to key on", s.Name)
			}
		}
	})
}

// TestMaterializeSkipsRatherThanFailsWhenGitIsMissing is the decision from the
// doc comment: a repo-identity test that passes because git never ran would
// be a worse failure than one that never executes.
func TestMaterializeSkipsRatherThanFailsWhenGitIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var sub *testing.T
	t.Run("no git on PATH", func(st *testing.T) {
		sub = st
		Materialize(st)
	})
	if sub == nil || !sub.Skipped() {
		t.Error("Materialize did not skip when git was not on PATH")
	}
}

// TestFixturesHelpersFailLoudlyOnEveryErrorPath drives fixtures.go's
// t.Fatalf-guarded helpers through a real *testing.T running in a
// subprocess. A subtest that itself calls t.Fatal marks every ancestor test
// in the same process as failed — there is no way to assert "this call
// fails" from inside the same binary without corrupting this test's own
// result, so the failing call has to happen in a child process whose exit
// code and output this test reads instead.
func TestFixturesHelpersFailLoudlyOnEveryErrorPath(t *testing.T) {
	cases := []struct{ name, want string }{
		{"git-bad-subcommand", "fixtures: git"},
		{"mkdirs-blocked", "fixtures: cannot create"},
		{"writefile-missing-dir", "fixtures: cannot write"},
		{"applyskew-bad-mtime", "fixtures: bad skew mtime"},
		{"applyskew-missing-file", "fixtures: cannot set mtime"},
		{"copycorpus-unreadable", "fixtures: cannot materialize the corpus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "copycorpus-unreadable" && os.Getuid() == 0 {
				t.Skip("root ignores permission bits, so a 0000 file does not force a read failure")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestFixturesHelperSubprocess$")
			cmd.Env = append(os.Environ(), "RECALL_FIXTURES_HELPER_CASE="+tc.name)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("subprocess for %s succeeded; want the guarded helper to fail the calling test:\n%s", tc.name, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("subprocess output is missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestFixturesHelperSubprocess is never asserted on directly; it only runs as
// the child process TestFixturesHelpersFailLoudlyOnEveryErrorPath spawns, and
// its own pass/fail is read from the outside via the process exit code.
func TestFixturesHelperSubprocess(t *testing.T) {
	switch os.Getenv("RECALL_FIXTURES_HELPER_CASE") {
	case "":
		t.Skip("only runs as a subprocess of TestFixturesHelpersFailLoudlyOnEveryErrorPath")
	case "git-bad-subcommand":
		git(t, t.TempDir(), "not-a-real-git-subcommand")
	case "mkdirs-blocked":
		blocker := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mkdirs(t, filepath.Join(blocker, "child"))
	case "writefile-missing-dir":
		writeFile(t, filepath.Join(t.TempDir(), "missing-parent", "file.txt"), "body")
	case "applyskew-bad-mtime":
		path := filepath.Join(t.TempDir(), "file.jsonl")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		applySkew(t, path, "not a timestamp")
	case "applyskew-missing-file":
		applySkew(t, filepath.Join(t.TempDir(), "gone.jsonl"), "2026-06-07T00:00:00Z")
	case "copycorpus-unreadable":
		src := t.TempDir()
		unreadable := filepath.Join(src, "unreadable.jsonl")
		if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
			t.Fatal(err)
		}
		copyCorpus(t, src, t.TempDir(), "/scratch")
	default:
		t.Fatalf("unknown case %q", os.Getenv("RECALL_FIXTURES_HELPER_CASE"))
	}
}

func TestMaterializeIsIndependentPerCall(t *testing.T) {
	a := Materialize(t)
	b := Materialize(t)
	if a.Root == b.Root || a.Scratch == b.Scratch {
		t.Error("two calls shared a directory, so one test could corrupt another")
	}
}

func recordsIn(t *testing.T, c Corpus, rel string) []jsonl.Record {
	t.Helper()
	r, err := jsonl.Open(c.Path(rel))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var out []jsonl.Record
	for r.Next() {
		l := r.Line()
		cp := append([]byte(nil), l.Bytes...)
		rec, ok := jsonl.Parse(jsonl.Line{Offset: l.Offset, Length: l.Length, Bytes: cp})
		if !ok {
			t.Fatalf("%s: line at %d did not parse", rel, l.Offset)
		}
		out = append(out, rec)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fileContains(t *testing.T, path, want string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(data), want)
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
