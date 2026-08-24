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

`recall` is a static binary (`CGO_ENABLED=0`) with one dependency. There is no per-project setup
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

`find`, `turns`, `when` and `show` still read Claude Code's corpus only, and refuse an explicit
`--provider`/`RECALL_AGENT` naming another agent rather than silently answering from the wrong
one. `recall doctor` reads whatever `--provider` resolves to today.

## The four questions, and which command answers each

| Question | Command |
|---|---|
| Which session was that? | `recall find <query>` |
| What did I conclude, and why? | `recall turns <query>` / `recall show <session>` |
| When did I first say this? | `recall when <query>` |
| Have I hit this before? | `recall find <query> --all` |

Plus `recall doctor` for archive integrity and `recall guide` — read that one first.

## Worked examples

Every command below is a real invocation against a real archive, with its real output pasted
in (home-directory paths shortened to `~`, and one project directory name replaced where a real
one would have named a private checkout) — except the first, which demonstrates the headline
claim above and is labeled as a generated demo corpus rather than a real session history.

**`recall find <query>` across two checkouts of one repo** — the property recall exists for,
shown rather than asserted. Built with `internal/corpusgen` (`corpusgen.Small()`) under a
`HOME` pointed at a temporary directory, never a real session store: two checkouts of one repo,
`repo00-1` and `repo00-2`, and a term planted only in a session recorded under `repo00-2`. The
search below runs from `repo00-1`:

```
$ recall find zq16ddfwutxgjte
1 session · 1 hit for "zq16ddfwutxgjte"
35c32001-6640-43fd-9b2f-d83ff13572c1  2026-01-02  git.invalid/corpusgen/repo00  main  1 hit of 40 turns
  assistant  …check walk value before return depends because inside. the «zq16ddfwutxgjte» path is the one we settled on
── 6 sessions · 6 searched · conversation only — tool output NOT searched (--results)
── live to 2026-08-19 · archived before that · refreshed just now
── scanned 247.8 KB · 239 turns · 31 ms
── 510 B · ~128 tokens
```

The returned session lives under `~/demo-corpus/.claude/checkouts/repo00-2`; the command ran
from `~/demo-corpus/.claude/checkouts/repo00-1` (`~/demo-corpus` collapses the temporary
directory `HOME` pointed at for this run). Both checkouts share one repo identity
(`git.invalid/corpusgen/repo00`), which is what makes a hit recorded in one visible from the
other. `zq16ddfwutxgjte` is a term the generator plants at a known location for this proof, not
a real keyword.

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
── 745 turns of your own session were skipped (--include-self)
── scanned 908.8 KB · 378 turns · 828 ms
── 969 B · ~243 tokens
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
── 745 turns of your own session were skipped (--include-self)
── scanned 908.8 KB · 378 turns · 499 ms
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
── scanned 908.8 KB · 378 turns · 472 ms
── 857 B · ~215 tokens
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
── 745 turns of your own session were skipped (--include-self)
── scanned 908.8 KB · 378 turns · 490 ms
── 952 B · ~238 tokens
```

**`recall doctor`** — archive integrity, coverage boundaries, format drift:

```
$ recall doctor
archive    ~/.local/share/recall
integrity  ok · 79242 turns · 56 sessions · 118.5 MB
  conversation  ok     ·   21.9 MB ·    8115 turns
  invocation    ok     ·   14.6 MB ·   35803 turns
  result        ok     ·   81.9 MB ·   35324 turns
  meta.json     ok
  cursor        ok
coverage   live to 2026-07-19 · content 2026-07-19 to 2026-08-19
skew       14 days on «a-project-directory»/defc1576-a807-438b-8287-6dd9dadb3012.jsonl
corpus     ~/.claude/projects · 679 files · 0 vanished · 0 unreadable
records    114031 lines · 0 malformed · 0 untyped · 9 of an unknown type
  unknown type atis-latch  9
dedup      175 records collapsed on (session, uuid) at ingest
authorship 1088 human-shaped · 344 typed · 73 command-args
warning    9 records carry a type this build has never seen; they are archived and searchable, but nothing interprets their fields
```

**`recall guide`** — the on-ramp; run this before anything else:

```
$ recall guide
recall — what was said in any past Claude Code session on this machine.
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

## For agents

- Run `recall guide` once at the start of a session that might need it — one screen, no query
  cost, maps each question shape to its command.
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

## Design

`docs/design.md` has every decision behind this tool, each with its measurement and the
alternatives it beat. `docs/requirements.md` has the constraints that shaped it. Start with
design if you want to know why something works the way it does.
