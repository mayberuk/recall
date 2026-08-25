# Worked examples

Every block here is real output, not a mock-up. `./scripts/demo.sh` builds `recall`, writes the
corpus these commands run against, and runs them, so you can reproduce the whole page. Only the
wall-clock figures in the footer will differ.

The corpus is five sessions across three repos, one of which (`github.com/acme/payments`) is
checked out twice: at `src/payments` on `main`, and at `src/payments-hotfix` on `hotfix/dedupe`.
Every command below runs from `src/payments`.

## `recall guide`: read this one first

One screen mapping each question shape to the command that answers it. No query cost, and the
same page the `recall_guide` MCP tool returns.

```console
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
```

## `recall find`: which session talked about this

The headline case. The strongest answer is recorded in `src/payments-hotfix`, a *different
checkout* on a branch that is not the one we are standing on, and it comes back without being
asked for by name, because recall keys a repo on its git remote rather than its path.

```console
$ recall find idempotency
2 sessions · 5 hits for "idempotency"
6e2b8d15-4c70-4a93-b8e1-05d7c2914f63  2026-06-03  github.com/acme/payments  hotfix/dedupe  4 hits in 3 turns of 6 turns
  human      Duplicate charges again. Two merchants hit the same «idempotenc»y key and one of them saw the other's receipt. How do we scope…
  assistant  Scope the «idempotenc»y key per merchant, never globally. The key is a string the… ×2
  assistant  …a row it cannot interpret. The existing primary key is the bare «idempotenc»y_key, so the migration is: add merchant_id to the table,…
3b8a2c94-6d17-4e05-8f2b-c091d67a4e38  2026-07-21  github.com/acme/payments  main  1 hit of 4 turns
  assistant  …charge may well have succeeded — so a retry there needs the «idempotenc»y key to be safe, and without one it must not be retried at all.
── 3 sessions · 3 searched · conversation only — tool output NOT searched (--results)
── live to 2026-05-12 · archived before that · refreshed just now
── scanned 3.0 KB · 14 turns · 6 ms
── 1.0 KB · ~264 tokens
```

Note the second hit: a passing mention in a different session about retry safety. Sessions are
ranked by **concentration** (hits per conversation turn), so the session that was mostly about
idempotency outranks the one that mentioned it once, regardless of which is newer.

## `recall turns`: the passages themselves

`find` tells you which session; `turns` gives you the text, ranked across all of them. Here it
also demonstrates the miss path: nothing in this repo carries the query, so rather than an empty
result it reports where the term *does* live and hands over the exact command to get there.

```console
$ recall turns connection pool --limit 1
no turns carry "connection pool" in github.com/acme/payments
found elsewhere: 5 hits in 1 other repo
  github.com/acme/infra                    5 hits · 1 session
run: recall find 'connection pool' --all
── 3 sessions · 3 searched · conversation only — tool output NOT searched (--results)
── live to 2026-05-12 · archived before that · refreshed just now
── scanned 7.5 KB · 36 turns · 2 passes · 0.6 ms
── 455 B · ~114 tokens
```

A search that finds nothing is still an answer with coverage attached. `2 passes` in the footer
is the miss path going back over the corpus to explain itself.

## `recall when`: place a topic in time

```console
$ recall when rate limit
"rate limit"  first 2026-05-12 · last 2026-07-21 · 8 hits in 2 sessions
  2026-05     6 hits · 1 session
  2026-07     2 hits · 1 session

oldest first
1a9c4f02-7b31-4e58-9d06-2f8a51c37e40  2026-05-12  github.com/acme/payments  main  6 hits in 2 turns of 4 turns
  human      …on the charge endpoint. Should we go with a sliding window «rate» limiter or a token bucket? ×2
  assistant  …a distributed caller. Merchant id is also the unit we bill and «rate»-limit contractually, so the limit we enforce is the limit in the… ×4
3b8a2c94-6d17-4e05-8f2b-c091d67a4e38  2026-07-21  github.com/acme/payments  main  2 hits in 1 turn of 4 turns
  assistant  …retries are synchronised by the same hourly batch that drives the «rate» limiting, so retry storms are the failure mode to design against. ×2
── 3 sessions · 3 searched · conversation only — tool output NOT searched (--results)
── live to 2026-05-12 · archived before that · refreshed just now
── scanned 3.0 KB · 14 turns · 0.5 ms
── 1.0 KB · ~263 tokens
```

Chronological rather than ranked, with a per-month histogram first, for answering "when did we
start worrying about this" rather than "what did we decide".

## `recall show`: the conclusion, in context

`turns` stamps every passage `session:uuid`, so `show --turn <uuid>` jumps straight back to it
with the surrounding turns. `--around 1` is one turn either side; `--around 0` is the turn alone.

```console
$ recall show 6e2b8d15 --turn 6e2b8d15-0001-4000-8000-000000000001 --around 1
6e2b8d15-4c70-4a93-b8e1-05d7c2914f63  github.com/acme/payments  hotfix/dedupe  6 turns (conversation)

turns 1-3 of 6
  2026-06-03  human
    Duplicate charges again. Two merchants hit the same idempotency key and one of them saw the other's receipt. How do we scope these keys?
> 2026-06-03  assistant
    Scope the idempotency key per merchant, never globally. The key is a string the client chooses, so a global namespace means any two clients picking `order-1` collide — and the collision does not fail loudly, it returns the first caller's stored response to the second, which is exactly the cross-merchant receipt leak you just saw. Make the storage key the pair (merchant_id, idempotency_key) and the collision becomes impossible rather than unlikely.
  2026-06-03  human
    Do we need to migrate the existing rows?
── 1 session · 1 searched · conversation only — tool output NOT searched (--results)
── live to 2026-05-12 · archived before that · refreshed just now
── showing 3 of 6 turns (--around)
── scanned 1.3 KB · 6 turns · 0.5 ms
── 1.1 KB · ~276 tokens
```

Any unique session-id prefix works, which is why `6e2b8d15` is enough.

## `recall doctor`: is the archive sound

```console
$ recall doctor
archive    /tmp/recall-demo/archive
integrity  ok · 22 turns · 5 sessions · 8.8 KB
  conversation  ok     ·    8.8 KB ·      22 turns
  invocation    ok     ·      16 B ·       0 turns
  result        ok     ·      16 B ·       0 turns
  meta.json     ok
  cursor        ok
coverage   live to 2026-05-12 · content 2026-05-12 to 2026-07-21
skew       0 days on -tmp-recall-demo.gP1HBW-home-src-payments-hotfix/6e2b8d15-4c70-4a93-b8e1-05d7c2914f63.jsonl
corpus     /tmp/recall-demo/home/projects · 5 files · 0 vanished · 0 unreadable
records    22 lines · 0 malformed · 0 untyped · 0 of an unknown type
dedup      0 records collapsed on (session, uuid) at ingest
authorship 11 human-shaped · 11 typed · 0 command-args
```

`integrity` verifies each tier's frames, the metadata and the cursor. `coverage` states two
different boundaries: `live` is how far back the raw transcripts still reach, `content` is what
the archive holds, and the archive outliving the raw store is the entire point, because Claude
Code deletes sessions after 90 days. `skew` names the largest gap between a file's mtime and the
newest turn inside it. `doctor` is deliberately *not* an MCP tool: it answers a question about
the archive, not about your past sessions.

## `recall-fzf`: the interactive front end

`shell/recall.zsh` is an fzf front end over the CLI. It runs recall's own commands and renders
their output; it never searches, ranks or parses transcripts itself. There is nothing to build
and nothing to set up per project. Source it:

```sh
source shell/recall.zsh
```

```sh
recall-fzf idempotency                       # live finder, prints the chosen session id
recall-fzf idempotency --all                 # extra flags pass straight to `recall find`
recall show "$(recall-fzf --ids idempotency | head -1)"
```

In the finder: typing re-searches, `enter` prints the session id and exits, `ctrl-o` opens the
whole session in `$PAGER`, and `ctrl-/` toggles a preview of `recall show` for the highlighted
row. fzf's own matching and sorting are disabled, because recall did both already, and letting fzf
re-rank would discard the concentration ordering that makes the first result the right one.

**Without a terminal** the same function prints to stdout, so it works in a pipeline and from a
script:

```sh
recall-fzf --ids <query>     # one session id per line
recall-fzf <query>           # the ranked records, blank line between them
```

Exit codes match the CLI's: `0` hits, `1` searched and matched nothing, `2` a query was required
and none was given, `127` the binary was not found (set `RECALL_BIN`). On a miss the coverage
footer goes to stderr, because there is no header to put it in.

Requires `fzf` for the interactive path only; the pipeline path shells out to nothing but recall.

**fzf versions.** Two refinements need a recent fzf, and the function probes for each rather than
demanding a version, because a flag fzf does not recognise is a hard error that would cost the
whole finder. `--id-nth` lets `--track` follow a record's *identity* across reloads instead of a
screen position, so the highlight stays on the same session as results change; without it the
cursor holds a position instead. The `result-final` event updates the header once results settle
rather than on every keystroke, and where it is missing the plain `result` event does the same job
slightly more often. Both were added after fzf 0.67. Everything else the finder uses, including
`change:reload-sync`, `transform-header` and `--gap`, works there.
