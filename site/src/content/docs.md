# Documentation

recall searches every past coding-agent session on your machine. Claude Code and Codex CLI, from
any checkout of a repo, in about 30 milliseconds, with no index and nothing running in the
background.

This page is the whole reference. It is also served as
[plain markdown](/docs.md) if you would rather read it that way, or point an agent at it.

## Install

```
curl -fsSL https://raw.githubusercontent.com/mayberuk/recall/main/install.sh | sh
```

macOS, Linux and Windows. The script resolves the latest release, verifies its sha256 against the
published `checksums.txt`, and puts the binary in `~/.local/bin`. Set `RECALL_INSTALL_DIR` to put
it somewhere else, or `RECALL_VERSION` to pin a release.

It registers recall with nothing. Reaching your agent is a separate, deliberate step, described
under [Model Context Protocol](#model-context-protocol) below.

From a checkout instead:

```
go install github.com/mayberuk/recall/cmd/recall@latest
```

The first search builds an archive under `$XDG_DATA_HOME/recall`, or `~/.local/share/recall`. Set
`RECALL_HOME` to move it. It is a data directory rather than a cache: it outlives the raw session
store, which is the entire point, because agents delete old transcripts and the conclusions in
them are what you came back for.

## Which command answers which question

| You want to know | Command |
|---|---|
| which session was that | `recall find <query>` |
| what did we conclude | `recall turns <query>` |
| what was said around it | `recall show <session> [query]` |
| when did this come up | `recall when <query>` |
| have I hit this before, anywhere | `recall find <query> --all` |
| is the archive sound | `recall doctor` |
| am I on the latest release | `recall update --check` |

`recall guide` prints this same map at the terminal, plus how a query is read. It is one screen and
costs no search, which is why it is the one thing worth running before your first query.

## The commands

### `recall find`

Which sessions talked about something, and how much. Ranked by concentration, meaning hits per
conversation turn, so a session that was mostly about your topic beats one that mentioned it once
in passing.

```
recall find idempotency
recall find "build number" --since 2w
recall find bitrise --results --author assistant
recall find wallet --brief --all
recall find wallet --ids | head -1 | xargs recall show
```

### `recall turns`

The passages themselves, ranked across every session at once rather than session by session. Use
it when you want the answer, not the session that holds it. A turn's own worth leads the ranking;
the session's concentration breaks ties.

```
recall turns "why did we pick bitrise"
recall turns agvtool --all --limit 3
recall turns codepush --author human --since 1m
```

### `recall show`

One session, with the turns around a match, so a conclusion arrives with the reasoning that led to
it. Session ids match on any unique prefix.

```
recall show 5fd86b00
recall show 5fd86b00 "build number"
recall show 5fd86b00 --turn a1db2039
recall show 5fd86b00 bitrise --results --around 5
```

### `recall when`

Places a topic in time: first said, last said, and a per-month histogram of the months between.
Chronological rather than ranked, for answering "when did we first hit this" rather than "who said
it best".

```
recall when flipper
recall when "retry budget" --all
```

### `recall doctor`

Archive integrity, coverage boundaries, and format drift. Reports what the archive holds, how
current it is, and any record types this build has never seen. Run it when an answer looks wrong.

### `recall update`

Replaces this binary with the latest published release, verifying its sha256 against the release's
own `checksums.txt` first. `--check` reports what is available and installs nothing.

```
recall update
recall update --check
```

A binary built from a checkout reports `dev` rather than a version, and `recall update` declines to
replace it: it was compiled on purpose, and overwriting it would throw away whatever was being
tested.

### `recall guide`

The map above, plus query semantics, printed to the terminal. Written for an agent that will not
open a documentation page but will run one command whose output then sits in its context for the
rest of the session.

## How a query is read

Terms are **ANDed**. When no turn carries them all you get the turns carrying the most, and a line
saying which terms were dropped. A query that misses does not come back silently empty.

- `"quoted words"` match as a phrase.
- `--all-terms` requires every term, returning nothing rather than the best partial match.
- `--not <term>` skips turns carrying that term. Repeatable.
- `--exact` matches literally, without stem expansion.

Session ids match on **any unique prefix**, so `recall show 5fd86b00` is enough.

## What is searched, and what is not

By default recall reads **conversation only**: what you and your agent said to each other. That is
about 3% of what Claude Code writes to disk, and almost always the part you want. The rest is tool
output stored twice and thinking-block signatures.

| Flag | Adds |
|---|---|
| `--results` | tool output |
| `--tools` | tool invocation lines |
| `--all` | every repo on the machine, not just this one |
| `--include-self` | the session asking the question |
| `--include-recall` | recall's own recorded commands and output |

Scope is **the current repo, across all its checkouts**. recall resolves a repo by its git remote
rather than its path, so one query reaches every clone and worktree. This is the reason the tool
exists: coding agents key transcripts by checkout path, so one logical repository sprawls across
directories and a path-scoped search silently misses.

## The coverage contract

Every searching command ends in a `──` footer naming what it left out: tier, scope, exclusions, how
current the archive is, and what the answer cost to produce.

```
── 3 sessions · 3 searched · conversation only — tool output NOT searched (--results)
── live to 2026-05-12 · archived before that · refreshed just now
── scanned 3.0 KB · 14 turns · 7 ms
── 1.0 KB · ~264 tokens
```

**If the footer does not mention a narrowing, that narrowing did not happen.** That is a contract,
not a courtesy. It is the reason you can trust a zero-result answer: a miss means the thing is not
there, not that recall quietly declined to look.

The last line prices the answer in tokens, because checking an old decision should be cheaper than
redoing it. `--budget` shapes the output to roughly a token count instead of refusing;
`--max-bytes` refuses outright above a byte ceiling.

## Filtering

These apply to `find`, `turns` and `when`.

| Flag | Effect |
|---|---|
| `--since`, `--until` | `2w`, `3d`, `12h` or a date |
| `--author` | `human`, `assistant`, `agent` or `system` |
| `--mine` | only turns you typed; the same as `--author human` |
| `--agent` | only turns from a subagent whose name contains this |
| `--branch` | only turns recorded on this git branch |
| `--repo` | search this repo identity instead of the current one |
| `--session` | one session, by id or unique prefix |
| `--sort recent` | override the verb's own order |

## Shaping the output

| Flag | Effect |
|---|---|
| `--limit` | most sessions, or passages for `turns` |
| `--hits` | most matched turns per session |
| `--brief` | one line per session, no snippets |
| `--chars` | most characters quoted per turn; `0` for the whole turn |
| `--around` | turns of context each side of a match, for `show` |
| `--full` | the whole session, for `show` |
| `--budget` | shape output to roughly this many tokens rather than refusing |
| `--max-bytes` | refuse to emit more than this many bytes; default 65536 |
| `--color` | `auto`, `always` or `never`; auto colours a terminal and nothing else |

Colour may add to an answer but never subtract from it. Stripping the escapes reproduces the plain
bytes exactly, which is why every size recall reports stays honest with colour on. It is also
structurally unable to reach `--json`, `--format jsonl`, `--ids` or the fzf record stream.

## For calling agents

### Exit codes

| Code | Meaning |
|---|---|
| `0` | hits |
| `1` | nothing matched |
| `2` | bad usage; the message names the valid flags |
| `3` | archive problem |
| `4` | output refused, because it breached `--max-bytes` |

Exit `1` is a real answer, not an error. Branch on it rather than treating it as a failure.

### Machine-readable output

`--json` emits one object. `--format jsonl` emits one record per line, which is what you want when
piping into something that reads a stream. `--ids` prints session ids only, one per line, for
`find`, and `session:uuid` citations for `turns`.

```
recall find wallet --ids | head -1 | xargs recall show
recall turns "retry budget" --format jsonl
```

Every machine format carries the same coverage block the text footer states. It is a field, not a
comment, so a caller can assert on it.

### Choosing a provider

`--provider` picks whose transcripts are read at all: `auto`, an agent name, or `all`. Every verb
resolves through it. `--agent` is a different thing: it filters by subagent name *within* whatever
the provider selected.

| Agent | Session store | Status |
|---|---|---|
| Claude Code | `~/.claude/projects` | read |
| Codex CLI | `~/.codex/sessions`, or `$CODEX_HOME` | read |
| Gemini CLI, Cursor | not read | detected, falls back to Claude Code and says so |

## Model Context Protocol

Your agent can search its own history without you shelling out.

```
recall mcp install <client>
```

Supported clients: `claude-code`, `codex`, `gemini`, `copilot`, `cursor`, `opencode`, `windsurf`.

`recall mcp config <client>` prints the same entry and **writes nothing**, if you would rather
paste it yourself. `--dry-run` prints every path an install would create or modify and writes none
of them. Any file recall merges into is copied to `<path>.recall-backup` first.

An install stops rather than overwrite a `recall` entry that already exists and differs from the
one it would write, because that entry may be pointing at a build you chose. `--force` replaces it.

### The five tools

| Tool | Answers |
|---|---|
| `recall_guide` | how a query is read, and which tool answers which question |
| `recall_find` | which sessions talked about something, and how much |
| `recall_turns` | the passages themselves, ranked across every session at once |
| `recall_show` | what was concluded, with the turns around it |
| `recall_when` | first said, last said, and the months between |

All five are **read-only**, and each runs the CLI verb it is named after, so a tool call and a
command line can never disagree about what the corpus holds. Every tool result carries the same
coverage block, so an agent can read what was skipped rather than assume nothing was.

Search defaults to the current repo. Pass `all: true` to reach every repo on the machine.

## Staying current

recall makes **no network request of any kind** except from two verbs you type for that purpose:

| Verb | What it does |
|---|---|
| `recall update` | resolves the latest release, verifies its sha256, replaces the binary |
| `recall update --check` | resolves the latest release and stops |
| `recall doctor` | refreshes the cached version number, silently, at most once a day |

Nothing else opens a socket. A search reads a small file next to the archive and never asks
anybody, which is why a search still costs what the benchmarks say it costs, and why there is still
no daemon and no background process.

When that cached file records a newer release, commands print one line about it **on stderr**, only
when stderr is a terminal, and at most once a day. It never touches stdout, so it cannot land in a
piped answer, a `--json` document, or an agent's context.

What a check sends is an ordinary HTTPS GET to `api.github.com`, which reveals your IP address and
the request's user agent, the same as visiting the releases page in a browser. It sends nothing
about your machine, your repos, or anything recall has read. Set `RECALL_NO_UPDATE_CHECK` to any
non-empty value and neither the check nor the notice happens at all.

Package managers work too: re-running `install.sh` replaces the binary the same way.

## Interactive, with fzf

```
source shell/recall.zsh
recall-fzf idempotency
```

Type and the list re-searches. `ctrl-o` opens the whole session in your pager; `enter` prints its
id and exits, so it composes with everything above. fzf's own matching and sorting are turned off,
because recall did both already and letting fzf re-sort would discard concentration ranking.

## Environment

| Variable | Effect |
|---|---|
| `RECALL_HOME` | where the archive lives; defaults to `$XDG_DATA_HOME/recall` or `~/.local/share/recall` |
| `RECALL_INSTALL_DIR` | where `install.sh` puts the binary; defaults to `~/.local/bin` |
| `RECALL_VERSION` | pin `install.sh` to a release instead of the latest |
| `RECALL_AGENT` | default provider, overridden by `--provider` |
| `NO_COLOR` | set and non-empty disables colour, as does `TERM=dumb` |
| `RECALL_NO_UPDATE_CHECK` | set and non-empty disables the version check and its notice |

## Questions

### What is recall?

recall is a Go command line tool that searches every past coding-agent session transcript stored on
your machine, from Claude Code and Codex CLI, and returns the sessions or the passages that talked
about a topic. It is also a Model Context Protocol server, so a coding agent can search that
history itself, mid-task, without you pasting anything.

### How fast is recall?

A typical search takes about 30 milliseconds against a store of 143 real conversations totalling
1.52 GB, measured start to exit. Including tool output takes about 93 ms, and a search that matches
nothing takes about 113 ms, because every byte has to be read before it can be ruled out. The
reproducible figures, against a corpus generated from a fixed seed, are on the
[benchmarks page](https://recall.mayberuk.com/bench/).

### Does recall need an index?

No. There is no index to build, rebuild or keep current. recall reads the whole store on every
query, which is why a conversation from ten minutes ago is findable immediately, why there is
nothing to go stale, and why nothing runs in the background.

### Does recall send my conversations anywhere?

No. Every search runs on your machine against your own files. There is no account, no upload, no
telemetry and no background process. The one network call in the whole binary is `recall update`,
which asks the GitHub releases API which version is current, and only when you type it. Setting
`RECALL_NO_UPDATE_CHECK` to any non-empty value turns that off too, after which recall opens no
socket at all.

### Which coding agents does recall work with?

It registers itself as an MCP server with Claude Code, Codex CLI, Gemini CLI, GitHub Copilot CLI,
Cursor, OpenCode and Windsurf, using `recall mcp install <client>`. The transcripts it reads are
written by Claude Code and Codex CLI; other clients call it as a server rather than being read from.

### What does recall search by default?

Conversation turns only, meaning what you and your agent said to each other. Tool output is most of
a session store and is searched only with `--results`; tool command lines only with `--tools`.
Every answer ends with a footer stating exactly which of those narrowings were applied, so a
narrowing you did not ask for cannot happen quietly.

### Does recall work from any checkout of a repository?

Yes, and it is the reason the tool exists. Coding agents file transcripts by checkout path, so one
repository sprawls across clones and worktrees and a path-scoped search silently misses. recall
resolves a repository by its git remote instead, so every checkout of it is one history, including
branches you have since deleted.

### What does recall cost?

Nothing. It is free and open source under the MIT licence, with no paid tier and nothing to sign
up for.

## Reading further

- [Worked examples](https://github.com/mayberuk/recall/blob/main/docs/examples.md), real output for
  all six commands, reproducible with `scripts/demo.sh`
- [MCP integration](https://github.com/mayberuk/recall/blob/main/docs/mcp.md), every client's config
  path and exactly what `install` writes
- [For calling agents](https://github.com/mayberuk/recall/blob/main/docs/agents.md), the contract in
  full
- [Benchmarks](https://github.com/mayberuk/recall/blob/main/bench/RESULTS.md), 32 micro benchmarks
  and 48 end-to-end scenarios against a corpus you can regenerate
- [Changelog](https://github.com/mayberuk/recall/blob/main/CHANGELOG.md), worth reading before an
  upgrade
