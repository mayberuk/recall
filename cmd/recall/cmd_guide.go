package main

import (
	"io"
	"os"
)

func init() {
	Register("guide", func(args []string) error { return guide(args, os.Stdout) })
	Describe("guide", "", "read this first: which command answers which question", newFlags("guide"),
		"recall guide")
}

// guide is the one page a caller reads before its first query. It exists
// because an agent will not open docs/ but will run one command whose output
// then sits in its context for the rest of the session, and because the
// observed failure was an agent guessing at semantics rather than being told
// them.
func guide(args []string, out io.Writer) error {
	fs := newFlags("guide")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	_, err := io.WriteString(out, guideText)
	return err
}

const guideText = `recall — what was said in any past session of the selected agent, on this machine.
One query reaches every checkout of a repo, not just the one you are standing in.

WHICH COMMAND
  which session was that          recall find <query>
  what did we conclude            recall turns <query>        the passages themselves
                                  recall show <session> <q>   that session, in context
  when did this come up           recall when <query>
  have I hit this before          recall find <query> --all
  is the archive healthy          recall doctor

HOW A QUERY IS READ
  Terms are ANDed. If no turn carries them all, you get the turns carrying the
  most, and a line says which terms those are. So a long query degrades instead
  of returning nothing.
  "quoted words"    one phrase, matched together
  --all-terms       require every term; return nothing rather than a partial match
  --not <term>      skip turns carrying it; repeatable
  --exact           no stem expansion
  Common words are dropped from queries longer than two terms, and it says so.
  Matching is case-insensitive and matches inside words: "build" finds "iosBuild",
  ranked below a whole-word match.

WHAT IS SEARCHED, AND WHAT IS NOT
  Conversation only, by default. Tool output is 58% of the store and is only
  searched with --results; tool command lines with --tools.
  Only the repo you are in, across all its checkouts. --all reaches the machine.
  Your own session and recall's own past output are excluded, because both
  answer the question with itself. --include-self and --include-recall undo that.
  Every one of these narrowings is printed in the ── footer. If the footer does
  not mention it, it did not happen.
  Both numbers in "N sessions · M searched" are within the scope you asked for,
  so they change with --all, --repo and --session. They are not corpus totals.

NARROWING
  --since 2w --until 3d      also 12h, 2026-08-01
  --author human|assistant|agent|system     --mine is --author human
  --branch <name>   --agent <name>   --session <id>   --repo <name>

CONTEXT COST
  --brief            one line per session, roughly a third of the bytes
  --hits N           matched turns shown per session (default 3)
  --limit N          sessions shown (default 10)
  --budget 2000      shape the answer down to roughly that many tokens
  --max-bytes N      refuse above N bytes (default 65536)
  Every answer ends with its own size and rough token count.

MACHINE FORMS
  --json             one object: everything the text form shows, and more
  --format jsonl     one object per line: each match, then a coverage record
  --ids              session ids only, one per line

SESSION IDS
  Any unique prefix works: recall show 5fd86b00
  recall turns stamps each passage session:uuid, and recall show <session>
  --turn <uuid> jumps straight back to it.
  show quotes turns whole, so one window can be tens of kilobytes: --chars N
  caps each turn, --around N narrows the window, --budget N shapes the answer.

EXIT CODES
  0  hits
  1  the search ran and matched nothing
  2  bad usage — a wrong flag prints the valid ones
  3  the archive could not be read
  4  the answer was refused for being over --max-bytes

RECIPES
  recall find "build number" --since 2w --not testbuild --author assistant
  recall turns "why did we pick bitrise" --all
  recall find agvtool --ids | head -1 | xargs recall show
  recall when codepush --brief
`
