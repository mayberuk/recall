package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The expected values state git's documented config rules; each case is then
// re-checked against git itself, so a parser that agrees only with its own
// author fails here.
var configCases = []struct {
	name string
	body string
	want string
}{
	{
		name: "quoted subsection",
		body: "[remote \"origin\"]\n\turl = ssh://git@example.invalid:10022/acme/app.git\n",
		want: "ssh://git@example.invalid:10022/acme/app.git",
	},
	{
		name: "dotted subsection form",
		body: "[remote.origin]\n\turl = https://example.invalid/acme/app\n",
		want: "https://example.invalid/acme/app",
	},
	{
		name: "section name is case insensitive",
		body: "[REMOTE \"origin\"]\n\tURL = https://example.invalid/acme/app\n",
		want: "https://example.invalid/acme/app",
	},
	{
		name: "subsection name is case sensitive",
		body: "[remote \"Origin\"]\n\turl = https://example.invalid/acme/app\n",
		want: "",
	},
	{
		name: "last value wins",
		body: "[remote \"origin\"]\n\turl = https://example.invalid/old\n\turl = https://example.invalid/new\n",
		want: "https://example.invalid/new",
	},
	{
		name: "comments and blank lines",
		body: "# a comment\n\n; another\n[remote \"origin\"]\n\turl = https://example.invalid/acme/app ; trailing\n",
		want: "https://example.invalid/acme/app",
	},
	{
		name: "quoted value keeps a hash",
		body: "[remote \"origin\"]\n\turl = \"https://example.invalid/acme/app#frag\"\n",
		want: "https://example.invalid/acme/app#frag",
	},
	{
		name: "other remotes are ignored",
		body: "[remote \"upstream\"]\n\turl = https://example.invalid/upstream\n[branch \"main\"]\n\tremote = origin\n",
		want: "",
	},
	{
		name: "origin with no url",
		body: "[core]\n\tbare = false\n[remote \"origin\"]\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n",
		want: "",
	},
}

func TestOriginURL(t *testing.T) {
	for _, tc := range configCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := originURL(path); got != tc.want {
				t.Errorf("originURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOriginURLAgreesWithGit(t *testing.T) {
	requireGit(t)
	for _, tc := range configCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, _ := exec.Command("git", "config", "--file", path, "--get", "remote.origin.url").Output()
			if got := strings.TrimRight(string(out), "\n"); got != tc.want {
				t.Errorf("git reads %q, but this case expects %q", got, tc.want)
			}
		})
	}
}

func TestOriginURLOnAnUnreadableConfig(t *testing.T) {
	if got := originURL(filepath.Join(t.TempDir(), "absent")); got != "" {
		t.Errorf("originURL = %q, want empty", got)
	}
}

// TestOriginURLSkipsALineWithNoEquals is parseEntry's !found branch: a line
// inside the origin section that is not a key=value pair is not a url and
// must not stop the scan for the real one that follows it.
func TestOriginURLSkipsALineWithNoEquals(t *testing.T) {
	body := "[remote \"origin\"]\n\tthisLineHasNoEquals\n\turl = https://example.invalid/acme/app\n"
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := originURL(path), "https://example.invalid/acme/app"; got != want {
		t.Errorf("originURL = %q, want %q", got, want)
	}
}

// TestUnquoteAppliesGitsBackslashEscapes pins the escape table git-config(1)
// documents: \n, \t, \b, and any other escaped character stands for itself.
func TestUnquoteAppliesGitsBackslashEscapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"newline", `a\nb`, "a\nb"},
		{"tab", `a\tb`, "a\tb"},
		{"backspace", `a\bb`, "a\bb"},
		{"escaped quote keeps the character literal", `a\"b`, `a"b`},
		{"escaped backslash keeps the character literal", `a\\b`, `a\b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unquote(tc.in); got != tc.want {
				t.Errorf("unquote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
