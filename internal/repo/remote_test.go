package repo

import "testing"

func TestNormalizeRemote(t *testing.T) {
	cases := []struct {
		raw  string
		id   string
		name string
	}{
		{"ssh://git@git.example.com:10022/acme/api-server.git", "git.example.com/acme/api-server", "api-server"},
		{"git@git.example.com:acme/api-server.git", "git.example.com/acme/api-server", "api-server"},
		{"https://git.example.com/acme/api-server", "git.example.com/acme/api-server", "api-server"},
		{"https://git.example.com/acme/api-server/", "git.example.com/acme/api-server", "api-server"},
		{"ssh://git@github.com/example/toolkit", "github.com/example/toolkit", "toolkit"},
		{"https://GitHub.com/Example/Toolkit.git", "github.com/example/toolkit", "toolkit"},
		{"file:///srv/git/app.git", "srv/git/app", "app"},
		{"/srv/git/app.git", "srv/git/app", "app"},
		{"", "", ""},
		{"   ", "", ""},
		{"https://@", "", ""},
	}

	for _, tc := range cases {
		id, name := normalizeRemote(tc.raw)
		if id != tc.id || name != tc.name {
			t.Errorf("normalizeRemote(%q) = (%q, %q), want (%q, %q)", tc.raw, id, name, tc.id, tc.name)
		}
	}
}

// TestProtocolFormsFoldTogether is the folding property the whole tool rests
// on: two checkouts cloned over different protocols are one repo.
func TestProtocolFormsFoldTogether(t *testing.T) {
	forms := []string{
		"ssh://git@git.example.com:10022/acme/api-server.git",
		"git@git.example.com:acme/api-server.git",
		"https://git.example.com/acme/api-server.git",
	}
	want, _ := normalizeRemote(forms[0])
	for _, form := range forms[1:] {
		if got, _ := normalizeRemote(form); got != want {
			t.Errorf("normalizeRemote(%q) = %q, want %q", form, got, want)
		}
	}
}

// TestDifferentReposDoNotFold guards the direction that hides a session under a
// repo nobody will look in.
func TestDifferentReposDoNotFold(t *testing.T) {
	distinct := []string{
		"git@github.com:acme/tools.git",
		"git@gitlab.com:acme/tools.git",
		"git@github.com:other/tools.git",
		"git@github.com:acme/tools-2.git",
	}
	seen := make(map[string]string, len(distinct))
	for _, raw := range distinct {
		id, _ := normalizeRemote(raw)
		if prior, dup := seen[id]; dup {
			t.Errorf("%q and %q both normalize to %q", prior, raw, id)
		}
		seen[id] = raw
	}
}
