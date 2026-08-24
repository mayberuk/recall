# recall, for agents

Written for a coding agent calling `recall` — the exit codes, the machine-readable output forms,
and the coverage contract that makes an answer trustworthy. If you are an agent working *on*
recall's own source, you want [AGENTS.md](../AGENTS.md) instead.

- Run `recall guide` once at the start of a session that might need it — or call `recall_guide`,
  which returns the same page — one screen, no query cost, maps each question shape to its
  command.
- Exit codes: `0` hits, `1` the search ran and matched nothing, `2` bad usage (a wrong flag
  prints the valid ones), `3` the archive could not be read, `4` the answer was refused for
  being over `--max-bytes`, `5` the archive write failed, `6` a required external tool was
  missing, `7` anything else. Every non-zero exit also prints `ERROR_CODE=<slug>` on stderr.
- `--format jsonl` emits one JSON object per match plus a trailing coverage record, for a caller
  that wants to parse rather than read. `--json` emits a single object with the same information
  plus fields the text form only summarizes.
- Every searching command declares what it did **not** search (tier, scope, exclusions) in a
  `──` footer. If the footer doesn't mention a narrowing, that narrowing didn't happen — treat
  the footer as the coverage contract, not decoration.
- The footer's `── scanned …` line is what the search cost: bytes, turns, and wall clock.
  `--words` adds the words scanned and the lines carrying them. It is opt-in because counting
  them is a second pass over bytes already read; without it the line says nothing about words
  rather than reporting a zero you could mistake for an empty corpus.
- `RECALL_NO_STATS`, set to any non-empty value, leaves that line off the footer entirely and
  the `stats` object out of `--json` and `--format jsonl`. That is for a caller diffing two
  runs against each other, where a wall-clock figure can never be byte-identical.

## Choosing whose transcripts to read

Two agents are registered: **Claude Code** (`~/.claude/projects`) and **Codex CLI**
(`~/.codex/sessions`, or `$CODEX_HOME/sessions`). A Codex archive lives separately, under
`agents/codex/` inside the archive directory, so an existing Claude Code archive never needs to
rebuild to pick up the second agent.

By default `recall` detects which agent is asking, cheapest and most certain signal first:
`CODEX_THREAD_ID` or `CODEX_SESSION_ID`, then `GEMINI_CLI`, then `CURSOR_AGENT`, else Claude
Code. Gemini CLI and Cursor are detected but have no reader yet — a run that detects either falls
back to reading Claude Code's archive and says so in the coverage footer, rather than guessing.

`--provider` overrides detection: `auto` (the default) lets the environment decide, an agent name
(`claude-code`, `codex`) pins one, and `all` reads every registered agent whose session store
exists. `RECALL_AGENT` is the same choice as an environment variable, for a caller that can't
pass a flag — the flag wins if both are set. `--provider` is a different flag from `--agent`:
`--agent` narrows results to a subagent nickname *within* whichever corpus was already chosen, it
does not choose the corpus.

`find`, `turns`, `when`, `show` and `doctor` all read whichever agent
`--provider`/`RECALL_AGENT` resolves to, `all` included — one query then searches every
registered agent's archive. What is refused is naming
an agent this build has no reader for: `--provider gemini` exits 2 with
`ERROR_CODE=arg_error` rather than answering out of Claude Code's corpus under Gemini's name. An
agent that was merely *detected* is judged differently — an environment that says Codex with no
Codex session store on disk falls back to Claude Code and prints why on stderr, because nothing
was promised about an agent nobody asked for by name.
