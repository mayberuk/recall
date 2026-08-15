package repo

import (
	"os"
	"strings"
)

// originURL is remote.origin.url from a git config file, empty when the file is
// absent, unreadable, or carries no such key. Include directives are not
// followed: no repo on this machine uses one for a remote, and a missed url
// yields the honest "repo, no remote" rather than a wrong repo.
func originURL(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var section, subsection, url string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			section, subsection = parseSection(line)
			continue
		}
		if section != "remote" || subsection != "origin" {
			continue
		}
		// git config --get answers with the last value, so later entries win.
		if key, value, ok := parseEntry(line); ok && key == "url" {
			url = value
		}
	}
	return url
}

func parseSection(line string) (section, subsection string) {
	body := line
	if i := strings.Index(body, "]"); i >= 0 {
		body = body[:i+1]
	}
	body = strings.TrimSuffix(strings.TrimPrefix(body, "["), "]")
	body = strings.TrimSpace(body)
	if q := strings.Index(body, `"`); q >= 0 {
		name := strings.ToLower(strings.TrimSpace(body[:q]))
		return name, unquote(body[q:])
	}
	// The deprecated [section.subsection] form lowercases the subsection; the
	// quoted form above does not.
	name, sub, found := strings.Cut(body, ".")
	if !found {
		return strings.ToLower(strings.TrimSpace(name)), ""
	}
	return strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(sub))
}

func parseEntry(line string) (key, value string, ok bool) {
	rawKey, rawValue, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(rawKey)), unquote(strings.TrimSpace(rawValue)), true
}

// unquote applies git's config value rules: double quotes protect whitespace and
// comment characters, a backslash escapes the next character.
func unquote(s string) string {
	var out strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'b':
				out.WriteByte('\b')
			default:
				out.WriteByte(s[i])
			}
		case c == '"':
			inQuote = !inQuote
		case (c == '#' || c == ';') && !inQuote:
			return strings.TrimSpace(out.String())
		default:
			out.WriteByte(c)
		}
	}
	return strings.TrimSpace(out.String())
}
