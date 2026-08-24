# recall

`recall` searches every past session transcript on your machine from a supported coding agent —
not just the ones under the directory you happen to be sitting in. Two agents are supported today,
Claude Code and Codex CLI. Six commands answer four questions — which session, what was
concluded, when, and have I hit this before — without loading a transcript into the asking
agent's context.

## The problem it solves

Claude Code stores sessions under `~/.claude/projects/`, keyed by **checkout path**. One logical
repository can span many directories — clones, worktrees, relocations — so a search scoped to
the directory you happen to be in misses. The failure this exists to fix: a session was hunted
for in one checkout of a repo and turned out to live in a different checkout of the *same* repo.
`recall` searches every checkout, and every other repo on the machine, in one query.

## Install

```sh
go install github.com/mayberuk/recall/cmd/recall@latest
```

Or build from source:

```sh
git clone https://github.com/mayberuk/recall.git
cd recall
go build -o ~/.local/bin/recall ./cmd/recall
```

`recall` is a static binary (`CGO_ENABLED=0`) with two dependencies. There is no per-project setup
and no enablement step — a directory that was never initialized is exactly the one you will
search. The first run builds an archive from the whole corpus (a little over a second); later
runs read only the tiers a query touches and update incrementally.

## Which agent, and how it's chosen

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

## The four questions, and which command answers each

| Question | Command |
|---|---|
| Which session was that? | `recall find <query>` |
| What did I conclude, and why? | `recall turns <query>` / `recall show <session>` |
| When did I first say this? | `recall when <query>` |
| Have I hit this before? | `recall find <query> --all` |

Plus `recall doctor` for archive integrity and `recall guide` — read that one first. An agent
reaches the same four answers over the Model Context Protocol instead of a shell; see
[Reaching it from an agent](#reaching-it-from-an-agent).

## Worked examples

Every command below is a real invocation against a real archive, with its real output pasted
in (home-directory paths shortened to `~`, and one project directory name replaced where a real
one would have named a private checkout) — except the first, which demonstrates the headline
claim above and is labeled as a generated demo corpus rather than a real session history.

**`recall find <query>` across two checkouts of one repo** — the property recall exists for,
shown rather than asserted. Built with `internal/corpusgen` (`corpusgen.Small()`) into a
temporary directory, never a real session store: two checkouts of one repo, `repo00-1` and
`repo00-2`, and a term planted only in a session recorded under `repo00-2`. The search below
runs from `repo00-1`, with `CLAUDE_PROJECTS_DIR` and `RECALL_HOME` pointed at that corpus and
at an archive beside it:

```
$ recall find zq16ddfwutxgjte
1 session · 1 hit for "zq16ddfwutxgjte"
35c32001-6640-43fd-9b2f-d83ff13572c1  2026-01-02  git.invalid/corpusgen/repo00  main  1 hit of 40 turns
  assistant  …check walk value before return depends because inside. the «zq16ddfwutxgjte» path is the one we settled on
── 6 sessions · 6 searched · conversation only — tool output NOT searched (--results)
── live to 2026-08-21 · archived before that · refreshed just now
── scanned 247.8 KB · 239 turns · 1 ms
── 509 B · ~128 tokens
```

The returned session was recorded under the generated checkout `repo00-2`; the command ran from
`repo00-1`. Both checkouts share one repo identity (`git.invalid/corpusgen/repo00`), which is
what makes a hit recorded in one visible from the other. `zq16ddfwutxgjte` is a term the
generator plants at a known location for this proof, not a real keyword; the corpus is
reproducible from its seed, so the session id above is the same one a fresh generation writes.
The live boundary is the day the corpus was generated, because that is the mtime every file in
it carries.

**`recall find <query>`** — which sessions talked about something, and how much:

```
$ recall find gjson
1 session · 6 hits for "gjson"
048519dd-a321-4d81-b69e-d79688b1a4a5  2026-08-17  github.com/mayberuk/recall  initiative/agents-and-mcp  6 hits in 3 turns of 378 turns  4 from subagents
  agent      …(BLOCKER — hardcodes allowed=\"github.com/tidwall/«gjson»\" and fails CI on any second direct dependency; must become a… ×2
  agent      …"evidence": "scripts/deps-gate.sh: `allowed=\"github.com/tidwall/«gjson»\"` … `echo \"deps-gate: direct dependency other than ${allowed}… ×2
  system     …"evidence": "scripts/deps-gate.sh: `allowed=\"github.com/tidwall/«gjson»\"` … `echo \"deps-gate: direct dependency other than ${allowed}… ×2
── 2 sessions · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-07-18 · archived before that · refreshed just now
── 1010 turns of your own session were skipped (--include-self)
── scanned 908.8 KB · 378 turns · 477 ms
── 970 B · ~243 tokens
```

**`recall turns <query>`** — the passages themselves, ranked across every session:

```
$ recall turns "no index" --limit 2
2 of 18 matched turns for "no index"

048519dd-a321-4d81-b69e-d79688b1a4a5:675726c4-7f84-4e1a-a353-e7e1c5ebac6e  2026-08-17  github.com/mayberuk/recall  perf-wave-1  assistant  ×3
    Now that's a clear and somewhat surprising picture. Within the scan package:

    | path | cum | note |
    |---|---:|---|
    | `gather` → `tokenize` (miss-path term survey) | **36.4%** | `wordByte` alone is 15.0% |
    | `scanRange` (the hit path) | 14.6% | of which `fold` is 11.9% — **81% of it** |
    | `indexNeedle` (substring search) | 1.4% | |

    Let me check the archive side before deciding what to build.

048519dd-a321-4d81-b69e-d79688b1a4a5:c44ec573-32ad-4d46-8c98-3379f29cfa66  2026-08-17  github.com/mayberuk/recall  perf-wave-1  agent/a3000c6517100938f  ×11
    …and docs/sandbox.md, docs/config.md. Also check whether there is any general "I am an agent" flag.
    2. Where session transcripts ("rollouts") live on disk: the CODEX_HOME default (~/.codex), the sessions/ layout (year/month/day nesting?), the rollout filename pattern, per-OS differences (Linux/macOS/Windows), and whether there is a session_index or history.jsonl and what each contains. Also how sessions map to a project/repo/cwd.
    3. The rollout file format: JSONL? What are the top-level record types (e.g. session_meta, response_item, event_msg, turn_context, compacted)? What are the key fields (timestamp format, type/payload envelope, id, cwd, originator, cli_version, git info,…
    … 3325 bytes in this turn; `recall show 048519dd --turn c44ec573-32ad-4d46-8c98-3379f29cfa66` for all of it
── 2 sessions · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-07-18 · archived before that · refreshed just now
── showing 2 of 18 matched turns (--limit)
── 1010 turns of your own session were skipped (--include-self)
── scanned 908.8 KB · 378 turns · 118 ms
── 1.9 KB · ~488 tokens
```

**`recall show <session>`** — recover a conclusion with the turn it came from:

```
$ recall show 048519dd --turn 675726c4-7f84-4e1a-a353-e7e1c5ebac6e --around 0
048519dd-a321-4d81-b69e-d79688b1a4a5  github.com/mayberuk/recall  main  378 turns (conversation)

turns 17-17 of 378
> 2026-08-17  assistant
    Now that's a clear and somewhat surprising picture. Within the scan package:

    | path | cum | note |
    |---|---:|---|
    | `gather` → `tokenize` (miss-path term survey) | **36.4%** | `wordByte` alone is 15.0% |
    | `scanRange` (the hit path) | 14.6% | of which `fold` is 11.9% — **81% of it** |
    | `indexNeedle` (substring search) | 1.4% | |

    Let me check the archive side before deciding what to build.
── 1 session · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-07-18 · archived before that · refreshed just now
── showing 1 of 378 turns (--around)
── scanned 908.8 KB · 378 turns · 10 ms
── 856 B · ~214 tokens
```

**`recall when <query>`** — place a topic in time:

```
$ recall when provider
"provider"  first 2026-08-17 · last 2026-08-17 · 362 hits in 1 session
  2026-08   362 hits · 1 session

oldest first
048519dd-a321-4d81-b69e-d79688b1a4a5  2026-08-17  github.com/mayberuk/recall  initiative/agents-and-mcp  362 hits in 37 turns of 378 turns  188 from subagents
  assistant  …concentrated in `internal/jsonl` + `internal/strip`, so the «provider» seam for part 3 is narrower than it might sound. I'll pick up the…
  agent      Now finding 9 — the `«provider»` tool input in phase 2.
  assistant  «Provider»s amendments applied and lint-clean. One more (mcp-server)…
── 2 sessions · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-07-18 · archived before that · refreshed just now
── showing 3 of 37 matched turns (--hits)
── 1010 turns of your own session were skipped (--include-self)
── scanned 908.8 KB · 378 turns · 10 ms
── 952 B · ~238 tokens
```

**`recall doctor`** — archive integrity, coverage boundaries, format drift:

```
$ recall doctor
archive    ~/.local/share/recall
integrity  ok · 82591 turns · 56 sessions · 122.3 MB
  conversation  ok     ·   22.4 MB ·    8383 turns
  invocation    ok     ·   15.2 MB ·   37344 turns
  result        ok     ·   84.7 MB ·   36864 turns
  meta.json     ok
  cursor        ok
coverage   live to 2026-07-19 · content 2026-07-19 to 2026-08-21
skew       15 days on «a-project-directory»/defc1576-a807-438b-8287-6dd9dadb3012.jsonl
corpus     ~/.claude/projects · 675 files · 0 vanished · 0 unreadable
records    118378 lines · 0 malformed · 0 untyped · 9 of an unknown type
  unknown type atis-latch  9
dedup      175 records collapsed on (session, uuid) at ingest
authorship 1095 human-shaped · 345 typed · 71 command-args
warning    9 records carry a type this build has never seen; they are archived and searchable, but nothing interprets their fields
```

**`recall guide`** — the on-ramp; run this before anything else:

```
$ recall guide
recall — what was said in any past session of the selected agent, on this machine.
One query reaches every checkout of a repo, not just the one you are standing in.

WHICH COMMAND
  which session was that          recall find <query>
  what did we conclude            recall turns <query>        the passages themselves
                                  recall show <session> <q>   that session, in context
  when did this come up           recall when <query>
  have I hit this before          recall find <query> --all
  is the archive healthy          recall doctor
```

(truncated here — the real output continues with query syntax, narrowing flags, context-cost
flags, machine output forms, exit codes, and worked recipes.)

## Why there is no index

Of everything Claude Code writes to disk, the actual conversation — the words you and the
assistant typed — is about **3%**: 47.8 MB of a 1.52 GB session store. The rest is tool output
stored twice, opaque thinking-block signatures, and attachments.

Searching that 3% costs **30 ms end to end**, process start to rendered output, over a store of
143 sessions and 192,000 turns. Searching *everything*, tool output included, costs 93 ms.
So there is nothing for an index to speed up that a linear scan does not already do fast enough,
and no staleness or corruption class for it to introduce.

See `docs/design.md` for the full measurement and the rejected alternatives, including a working
SQLite FTS5 prototype that lost on those grounds and not on capability. `bench/RESULTS.md` carries
the reproducible numbers, taken against a corpus generated from a seed rather than anyone's
private store.

## Reaching it from an agent

`recall mcp serve` speaks the Model Context Protocol over stdio, revision `2026-07-28`, and
exposes five read-only tools: `recall_find`, `recall_guide`, `recall_show`, `recall_turns` and
`recall_when`. Each one runs the CLI verb it is named after and hands back the same view
`--format json` prints, coverage footer included, so a tool call and a command line cannot
disagree about what the corpus holds or about what was left out of it. `recall doctor` is
deliberately not a tool: it answers a question about archive integrity rather than about past
sessions, and every listed tool spends a session's roster budget whether it is called or not.

You never run `recall mcp serve` yourself — a client spawns it. Getting recall into a client's
config is two commands, in this order. Paths in the two blocks below come from a real run with
the home directory rewritten to `/home/you`, and the binary was at `~/.local/bin/recall`.

### `recall mcp config <client>` — writes nothing

It prints the file the entry belongs in, a second place that client also reads one from, the
client's own registration command where there is one, the entry itself, and a line saying
nothing was written:

```
$ recall mcp config claude-code
claude-code — Claude Code
  file   /home/you/.claude.json
  also   or .mcp.json at a repo root, which shares the entry with everyone who checks that repo out
  add    claude mcp add recall -- /home/you/.local/bin/recall mcp serve

{
  "mcpServers": {
    "recall": {
      "command": "/home/you/.local/bin/recall",
      "args": [
        "mcp",
        "serve"
      ],
      "env": {}
    }
  }
}

nothing was written. `recall mcp install claude-code` is the opt-in that puts it in place.
```

The snippet is the whole file a client would hold if recall were its only server, so it pastes
into an empty config or reads off as the one key to merge by hand. The path is absolute because
a client spawns the server from its own working directory, and a relative command there
resolves against whichever directory that happens to be.

### `recall mcp install <client>` — the opt-in

This one writes, and only when asked. Where the client ships its own registration command it
runs that rather than touching the file, because the vendor owns its config format and a merge
recall writes can be wrong in ways only that vendor's parser would notice. Where there is none
— Cursor, and OpenCode, whose `mcp add` is an interactive wizard with no documented flag syntax
— recall merges the single `recall` key in itself and carries every other key across as the
bytes it arrived as. **It copies any file it merges into to `<path>.recall-backup` first**; a
file that does not exist yet gets no backup, because an empty one could later be restored over
a good config. A `recall` entry that is already there and differs stops the install and names
`--force`; one that matches is a skip rather than a rewrite. A vendor command that is not on
`PATH` stops the install too, and prints the entry so the next move is on the screen rather than
in these docs. `--dry-run` prints every path the run would create or modify and writes none of
them:

```
$ recall mcp install claude-code --dry-run
recall mcp install claude-code --dry-run — Claude Code, nothing below was written
  run    claude mcp add recall -- /home/you/.local/bin/recall mcp serve
  mkdir  /home/you/.claude/skills/recall
  write  /home/you/.claude/skills/recall/SKILL.md  (recall's own instruction file)
```

An install also puts recall's `SKILL.md` where that client reads skills from. That half is what
gets the tools reached for rather than merely listed: the skill's description fires on the shape
of the question — *what did we decide about this*, *have we hit this before* — rather than on
recall's name, which is not a word anyone thinks of mid-task.

### The clients

| `<client>` | config file | what `install` does | instruction file |
|---|---|---|---|
| `claude-code` | `~/.claude.json`, or `.mcp.json` at a repo root — that one is shared with everyone who checks the repo out | runs `claude mcp add` | `~/.claude/skills/recall/SKILL.md` |
| `codex` | `~/.codex/config.toml` (`$CODEX_HOME/config.toml` if set); `codex mcp add` always writes the global file, so a project's own `.codex/config.toml` is a hand edit | runs `codex mcp add` | `~/.agents/skills/recall/SKILL.md` |
| `gemini` | `~/.gemini/settings.json` | runs `gemini mcp add` | `~/.gemini/skills/recall/SKILL.md` |
| `copilot` | `~/.copilot/mcp-config.json` | runs `copilot mcp add` | none — it has custom agents rather than skills, in a shape recall ships no asset for, and it reads `AGENTS.md` and `CLAUDE.md` from the repo anyway |
| `cursor` | `~/.cursor/mcp.json` | merges recall's key, after a backup | printed, not written: `.cursor/rules/recall.mdc` in the workspace |
| `opencode` | `~/.config/opencode/opencode.json` (`$XDG_CONFIG_HOME/opencode/opencode.json` if set) | merges recall's key, after a backup | `~/.agents/skills/recall/SKILL.md` — OpenCode reads that directory and `~/.claude/skills` natively, so it needs no copy of its own |
| `windsurf` | `~/.codeium/mcp_config.json` | nothing: it prints the entry | printed, not written: `.windsurf/rules/recall.md` in the workspace, though current builds prefer `.devin/rules/` |

**Windsurf is print-only.** `recall mcp install windsurf` writes nothing and prints the same
entry `recall mcp config windsurf` does. The vendor's own page says the config file is
`~/.codeium/mcp_config.json`; several third-party guides say `~/.codeium/windsurf/mcp_config.json`,
one directory deeper. Writing to the wrong one is the worst outcome on offer — an install that
reports success and a client that never sees recall — so recall prints the entry with the
documented path and leaves the one judgement it cannot make where it belongs.

**Cursor's rule file is print-only too, for a different reason** — its config entry is merged
normally, it is only the rule that is printed. Cursor documents `.cursor/rules` at the project
level only, and its global User Rules live in its settings rather than in a directory it scans,
so a file written to a guessed path under `~` would be read by nothing. recall prints the rule
with the documented workspace path and leaves the placing to you. Windsurf's rule is printed on
the same grounds.

### The plugin and extension route

For Claude Code, Codex and Gemini there is a second way in that does not involve
`recall mcp install` at all. `integrations/` in this repo is a checked-in export of the same
assets — a plugin marketplace for Claude Code, a plugin for Codex, an extension for Gemini —
and `recall mcp export <dir>` regenerates it from the binary. Each is consumed by that
*client's own* plugin command: recall installs no marketplace entry and knows nothing about one.

Those manifests register the server as a bare `"command": "recall"` with
`"args": ["mcp", "serve"]`, so **`recall` has to be on your `PATH`** for that route to work — a
file checked into a repo cannot know where the binary will sit on your machine. `recall mcp
install` is the other case: it resolves its own absolute path with `os.Executable`, so it names
the exact binary you ran it from.

## For agents

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

## Design

`docs/design.md` has every decision behind this tool, each with its measurement and the
alternatives it beat. `docs/requirements.md` has the constraints that shaped it. Start with
design if you want to know why something works the way it does.
