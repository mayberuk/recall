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
	b.WriteString(strings.Join(r.Argv, " "))
	b.WriteByte('\n')
	return []byte(b.String())
}

// shQuote POSIX single-quotes s, closing and reopening the quote around each
// embedded single quote.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
