package render

import "strings"

// Resume is recall resume's result: reopen argv plus what the shell line can't say.
type Resume struct {
	Session string   `json:"session"`
	Agent   string   `json:"agent"`
	CWD     string   `json:"cwd"`
	Argv    []string `json:"argv"`

	Notes []string `json:"notes,omitempty"`
}

func (r Resume) JSONL() ([]byte, error) {
	return JSON(r)
}

// RenderResume drops the cd prefix only when no cwd was recorded, not when a
// recorded one has since vanished.
func RenderResume(r Resume) []byte {
	var b strings.Builder
	if r.CWD != "" {
		b.WriteString("cd ")
		b.WriteString(shQuote(r.CWD))
		b.WriteString(" && ")
	}
	for i, arg := range r.Argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shWord(arg))
	}
	b.WriteByte('\n')
	return []byte(b.String())
}

// shWord quotes an argument a shell would otherwise act on. A session id
// reaches here from a store file's own sessionId field, which nothing
// validates, and this line is documented to be run through eval.
func shWord(s string) string {
	if shSafe(s) {
		return s
	}
	return shQuote(s)
}

// shSafe reports whether s survives a shell unquoted. The empty string does
// not: bare, it would vanish rather than pass an empty argument.
func shSafe(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == '/', c == ':', c == '=', c == '@', c == '+', c == ',':
		default:
			return false
		}
	}
	return true
}

// shQuote POSIX single-quotes s, closing and reopening the quote around each
// embedded single quote.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
