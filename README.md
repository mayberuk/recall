# recall

`recall` searches every past Claude Code session transcript on your machine, not just the ones
under the directory you happen to be sitting in. Six commands answer four questions — which
session, what was concluded, when, and have I hit this before — without loading a transcript into
the asking agent's context.

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
in (home-directory paths shortened to `~`) — except the first, which demonstrates the headline
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
── live to 2026-08-15 · archived before that · refreshed just now
── 464 B · ~116 tokens
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
1 session · 74 hits for "gjson"
11b7e527-575e-48e1-8eb7-f673614d3838  2026-08-14  ~/dev/recall  main  74 hits in 44 turns of 437 turns  49 from subagents
  agent      «gjson» is already in the module cache, so the build works offline.…
  agent      Now P2 — the `jsonl` package, the only «gjson» user.
  agent      …two suspected divergences between the hand-rolled header and the «gjson» accessors, with a temporary test I will delete.
── 2 sessions · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-06-10 · archived before that · refreshed just now
── showing 3 of 44 matched turns (--hits)
── 123 turns of your own session were skipped (--include-self)
── 788 B · ~197 tokens
```

**`recall turns <query>`** — the passages themselves, ranked across every session:

```
$ recall turns "no index" --limit 2
2 of 32 matched turns for "no index"

11b7e527-575e-48e1-8eb7-f673614d3838:8f0a101f-222e-46e5-97ca-9f8798c141e8  2026-08-14  ~/dev/recall  main  assistant  ×16
    **Archive is done and committed**, verified by me: build 0, tests 0 with the real corpus, `-race` 0, `gofmt` clean, and it imports only wave-0 packages — no dependency on Strip or Repo, exactly as the amendment required.

    The performance gate is the story here. **The cold pass first measured 4.42 s against a 4 s gate — a breach, which the contract calls a FAIL, not a warning.** The remedy was parallelizing the corpus walk, not adding an index: 0.96 s now. The ratified no-index decision holds.
    …
    … 1745 bytes in this turn; `recall show 11b7e527 --turn 8f0a101f-222e-46e5-97ca-9f8798c141e8` for all of it
── 2 sessions · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-06-10 · archived before that · refreshed just now
── showing 2 of 32 matched turns (--limit)
── 123 turns of your own session were skipped (--include-self)
── 2.1 KB · ~536 tokens
```

**`recall show <session>`** — recover a conclusion with the turn it came from:

```
$ recall show 11b7e527 --turn 8f0a101f-222e-46e5-97ca-9f8798c141e8 --around 0
11b7e527-575e-48e1-8eb7-f673614d3838  ~/dev/recall  main  437 turns (conversation)

turns 85-85 of 437
> 2026-08-14  assistant
    **Archive is done and committed**, verified by me: build 0, tests 0 with the real corpus, `-race` 0, `gofmt` clean, and it imports only wave-0 packages — no dependency on Strip or Repo, exactly as the amendment required.

    The performance gate is the story here. **The cold pass first measured 4.42 s against a 4 s gate — a breach, which the contract calls a FAIL, not a warning.** The remedy was…
    … 1745 bytes in this turn; --chars 0 for all of it
── 1 session · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-06-10 · archived before that · refreshed just now
── showing 1 of 437 turns (--around)
── 864 B · ~216 tokens
```

**`recall when <query>`** — place a topic in time:

```
$ recall when chezmoi
"chezmoi"  first 2026-08-14 · last 2026-08-14 · 55 hits in 1 session
  2026-08    55 hits · 1 session

oldest first
11b7e527-575e-48e1-8eb7-f673614d3838  2026-08-14  ~/dev/recall  main  55 hits in 18 turns of 437 turns  32 from subagents
  agent      Let me read the primary target and check «chezmoi»'s ignore rules.
  agent      Plan is written. Now Phase 3 — the «chezmoi» source first.
  agent      «Chezmoi» target carries the line. Now the two agent definitions.
── 2 sessions · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-06-10 · archived before that · refreshed just now
── showing 3 of 18 matched turns (--hits)
── 123 turns of your own session were skipped (--include-self)
── 818 B · ~205 tokens
```

**`recall doctor`** — archive integrity, coverage boundaries, format drift:

```
$ recall doctor
archive    ~/.local/share/recall
integrity  ok · 181835 turns · 138 sessions · 276.2 MB
  conversation  ok     ·   46.2 MB ·   36121 turns
  invocation    ok     ·   37.1 MB ·   73740 turns
  result        ok     ·  193.0 MB ·   71974 turns
  meta.json     ok
  cursor        ok
coverage   live to 2026-06-10 · content 2026-06-10 to 2026-08-15
corpus     ~/.claude/projects · 1169 files · 0 vanished · 0 unreadable
records    315245 lines · 0 malformed · 0 untyped · 0 of an unknown type
dedup      10141 records collapsed on (session, uuid) at ingest
authorship 4431 human-shaped · 1158 typed · 172 command-args
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
assistant typed — is **2.8%**. The rest is tool output stored twice, opaque thinking-block
signatures, and attachments. Scanning that 2.8% takes about **35 milliseconds**, so there is
nothing for an index to speed up that a linear scan does not already do fast enough, and no
staleness or corruption class for it to introduce. See `docs/design.md` for the full measurement
and the rejected alternatives, including a working SQLite FTS5 prototype that lost on those
grounds, not on capability.

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
