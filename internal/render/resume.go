package render

import "strings"

// Resume is the whole result of `recall resume`: the argv that reopens a
// session in its own agent, plus what could not be stated as part of the
// shell-ready line itself.
type Resume struct {
	Session string   `json:"session"`
	Agent   string   `json:"agent"`
	CWD     string   `json:"cwd"`
	Argv    []string `json:"argv"`

	// Notes carries what the human line cannot: a recorded cwd that no longer
	// exists, or a session with none recorded at all. The line itself has to
	// stay exactly one command, because a caller composes it with
	// `eval "$(recall resume <id>)"` — a second line there is a second command
	// eval would run.
	Notes []string `json:"notes,omitempty"`
}

// JSONL is Resume's one-line-per-record form. The whole answer is already one
// object, so it is --json's object again, newline-terminated the same way.
func (r Resume) JSONL() ([]byte, error) {
	return JSON(r)
}

// RenderResume is the human form: one shell-ready line, so
// `eval "$(recall resume <id>)"` composes from any directory. The `cd &&`
// prefix is left off only when the session recorded no working directory at
// all — a recorded directory that has since been removed still gets a `cd`,
// because a caller who understands the gap may still want to try it.
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

// shQuote is POSIX single-quoting: every byte inside is literal except a
// single quote, which has to close the quoted string, escape itself, then
// reopen it.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
