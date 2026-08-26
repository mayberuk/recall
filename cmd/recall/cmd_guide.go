package main

import (
	"context"
	"io"
	"os"
)

func init() {
	Register("guide", func(args []string) error { return guide(args, os.Stdout) })
	Describe("guide", "", "read this first: which command answers which question", newFlags("guide"),
		"recall guide",
		"recall guide --brief")
}

// guide is the one page a caller reads before its first query. It exists
// because an agent will not open docs/ but will run one command whose output
// then sits in its context for the rest of the session, and because the
// observed failure was an agent guessing at semantics rather than being told
// them.
func guide(args []string, out io.Writer) error {
	fs := newFlags("guide")
	var brief bool
	fs.BoolVar(&brief, "brief", false, "the compact page: what a caller cannot guess, none of what the tool descriptions and schemas already carry")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	text := guideText
	if brief {
		text = guideBrief
	}
	_, err := io.WriteString(out, text)
	return err
}

// Preamble implements mcp.Searcher for the first-call mechanism in
// internal/mcp/preamble.go, answering the same page --brief prints.
func (s *verbSearcher) Preamble(context.Context) (string, error) {
	return guideBrief, nil
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
  --exact           no stem expansion, no near-neighbor correction, no synonyms
  Common words are dropped from queries longer than two terms, and it says so.
  Matching is case-insensitive and matches inside words: "build" finds "iosBuild"
  and ranks it as a whole word, because a camelCase or acronym boundary counts
  as a word edge; a plain substring inside one segment still ranks lower.
  A term nothing carries may be corrected to a one-edit neighbor the corpus
  has; two edits away is only suggested, never substituted. A small shipped
  synonym table also searches the other spelling of some terms (auth,
  authentication; db, database), matched only as a whole word. The footer
  names either one when it fires.

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

// guideBrief keeps what a caller cannot guess elsewhere — how a query is
// read, what is searched, the footer contract — and drops what the tool
// descriptions, argument schemas, or the CLI itself already carry.
const guideBrief = `recall — what was said in any past session of the selected agent, on this machine.

HOW A QUERY IS READ
  Terms are ANDed; a query no turn carries in full degrades to the turns
  carrying the most of it, and the footer names which terms those were.
  "quoted words" match together. --all-terms requires every term. --not <term>
  skips turns carrying it. --exact turns off stemming, near-neighbor
  correction and the synonym table below.
  Matching is case-insensitive and matches inside words, and a camelCase or
  acronym boundary ranks as a whole word, not a lesser inside match.
  A term nothing carries may be corrected to a one-edit neighbor the corpus
  actually has; two edits away is only suggested. A small shipped synonym
  table also searches the other spelling of some terms (auth, authentication;
  db, database), matched only as a whole word.

WHAT IS SEARCHED, AND WHAT IS NOT
  Conversation only, by default; --results adds tool output, --tools adds
  command lines. Only the current repo, across its checkouts; --all reaches
  the machine. Your own session and recall's own past output are excluded;
  --include-self and --include-recall undo that.
  Every narrowing above, plus any correction or synonym substitution, is
  printed in the ── footer. If the footer does not mention it, it did not
  happen.

EXIT CODES
  0 hits   1 ran and matched nothing   2 bad usage   3 archive unreadable
  4 refused for size (--max-bytes)

recall guide — the full page: every command, every flag, recipes.
`
