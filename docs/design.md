---
brainstorm: transcript-recall
title: Machine-wide recall over past Claude Code sessions
status: ready
altitude: architecture
created: 2026-08-13T00:00:00Z
updated: 2026-08-13T00:00:00Z
seeded-from: grillme
reviewed: 2026-08-13 — adversarial hypothesis test, verdict DENIED, 12 findings accepted
---

# Machine-wide recall over past Claude Code sessions

## Objective
A command-line tool that answers "which session was that", "what did I conclude", "when did
I first say this", and "have I hit this before" across every session on this machine, without
pulling transcript text into the asking agent's context.

## Problem / Context
Requirements pinned in `docs/requirements.md`. The store at `~/.claude/projects/` keys
by checkout path, so one logical repo (a mobile app's, in the case that motivated this) can span
13 directories — a search scoped to the working directory misses. Today's failure: looked in one
checkout, the session was in a different checkout of the same repo.

## Measured facts about the corpus

All figures re-measured corpus-wide on 2026-08-13 and independently verified during adversarial
review. Earlier sampled figures in this document were wrong and have been replaced.

### What the corpus actually contains

```
1,077 .jsonl files  =  130 session files  +  947 subagent transcripts
                       132 distinct sessionIds
```

The layout is `<project>/<sessionId>.jsonl` for a session and
`<project>/<sessionId>/subagents/agent-<id>.jsonl` for each Task-tool subagent it spawned.
**88% of files are subagent transcripts.** There are ~131 real sessions spanning 2026-06-10 to
2026-08-13 — a rate of **~2 sessions/day**, not the 16.8 an earlier draft claimed by counting
files as sessions.

### Byte accounting (1.29 GB)

| Slice | Size | Share |
|---|---|---|
| `user` records | 820 MB | 62% |
| ├─ `message` (tool_result blocks) | 405 MB | |
| └─ `toolUseResult` — a *second* structured copy of the same results | 362 MB | |
| `assistant` records | 373 MB | 28% |
| ├─ `thinking` **signature** (opaque base64, zero recall value) | 125.4 MB | |
| ├─ `tool_use` (Edit/Write inputs carry whole file contents) | 65 MB | |
| ├─ `text` — actual assistant prose | 18 MB | |
| └─ `thinking` **text** — actual reasoning | 2.09 MB | |
| `attachment` | 106 MB | 8% |
| per-line metadata (uuid/sessionId/cwd/timestamp × ~266K records) | ~50 MB | 4% |

**58% of the store is tool results, roughly half of that redundant duplication.** The
conversation is **34.3 MB (2.6% of bytes)**, which compresses to roughly 6 MB. Archive growth
at ~2 sessions/day is a few MB per year.

**Thinking is 97% signature.** Only 2.09 MB is reasoning text, and **94.5% of thinking blocks
carry no text at all** — Claude Code does not persist most reasoning. Including all thinking
text costs ~6% of the archive, not 6×.

### Per-session conversation size — the figure that broke a decision

```
sessions=131   total=34.3 MB
mean=268 KB (~67K tokens)   median=118 KB (~29K tokens)
p90=681 KB   p99=1.5 MB   max=2.22 MB
sessions >512 KB: 24    >1 MB: 6
```

An earlier draft claimed ~19 KB / ~5K tokens per session. That was 26 MB divided by 1,074
*files* when only 131 are sessions — wrong by ~14×. A whole-session fetch is **not** cheap, and
an unbounded one on the largest session would load ~550K tokens, violating the requirements
brief's dealbreaker directly.

### Tool-result size distribution

```
72,089 tool results   mean 4,979 B   p50 585 B   p90 7,023 B   p99 83,857 B   max 679 KB
under 2 KB:  72.4% corpus-wide  |  79.1% in files >1 MB  |  59.9% in files ≤1 MB
```

An earlier draft said 81%, measured on 25 files all larger than 1 MB — the least representative
17% of the corpus by count. **By bytes rather than count**, 92.6% of tool-result text sits in
results over 2 KB, so a 1 KB head + 1 KB tail indexes only **~19% of tool-result text**.

### Record types (20, whole corpus)

```
assistant 137,991 · user 77,477 · attachment 27,281 · last-prompt · mode · permission-mode
ai-title · system 4,434 · queue-operation · pr-link 3,543 · file-history-snapshot
agent-name · file-history-delta · custom-title · bridge-session · worktree-state
relocated · frame-link · started · result
```

Free facets already in the data: `pr-link` (prNumber/prRepository/prUrl) → "the session that
produced MR 1284"; `attributionSkill` / `attributionPlugin` / `attributionMcpServer` →
"sessions where flightplan's execute ran"; `custom-title` and `ai-title` → session titles;
`relocated` (relocatedCwd) → sessions whose directory moved.

**Attribution fields are not universal.** `isSidechain` is present only on the four
conversational record types (user, assistant, attachment, system) — 93% of records, and the
16 metadata types have no author to attribute. `agentId` covers 43–46% of user and assistant
records and swings from 0% to 83% across versions with no monotone pattern. Subagent
attribution is therefore `isSidechain`-derived with `agentId` as an opportunistic refinement,
not free.

### Records are duplicated across files

6 top-level files carry more than one `sessionId`, and **9,473 record uuids appear in more than
one file (17,659 redundant copies)** — resumed and forked sessions carry prior records forward.
Files are append-only, but they are *not* one-session-per-file. Any count of hits or turns must
deduplicate by uuid or it double-counts.

### Version tolerance

The corpus spans **24 Claude Code versions** (2.1.170 → 2.1.231) and the official docs state
the entry format is internal and changes between releases. A parser must tolerate absent fields
rather than assume a schema.

`promptSource` is **not** an example of version drift — an earlier draft claimed it was added in
a later version, but it is present in every version including the oldest at a similar rate
(2–9% of user records). It records prompt *origin*: `typed` 1,069 / `sdk` 1,024 / `system` 514 /
`queued` 79. No cleanly version-gated field was found in the retained window; the
version-tolerance requirement stands on the documented format warning and on `agentId`'s
0–83% swing, not on `promptSource`.

### Human-turn discrimination — settled by spike 2026-08-14

**`promptSource == "typed"` is the discriminator. Content-shape detection is wrong and
overcounts by 5×.** Every record in the 5× gap is machine-generated.

Cross-tab of the 5,435 human-shaped user records (content is a string or a `text` block, no
`tool_result`):

```
promptSource   isSidechain    count
ABSENT         main           1,552     ← examined; ~zero are his prose
ABSENT         sidechain      1,163     ← promptSource is never written in subagent transcripts
typed          main           1,102     ← verified clean, unmistakably his voice
sdk            main           1,021     ← prompts sent TO subagents
system         main             517
queued         main              80
```

Two structural facts: **no labelled record is ever a sidechain**, so absence inside a subagent
transcript is expected rather than missing data; and absence is **not** version drift — it runs
33–68% across all 24 versions with no trend.

All 1,552 unlabelled main-session records classified by pattern:

```
343  slash-command wrappers (<command-name>/<command-message>)
279  other XML wrappers (<local-command-caveat>, <bash-input>, …)
259  skill preambles ("Base directory for this skill: …") and image-paste metadata
251  <local-command-stdout> — command output
178  compact continuation summaries ("This session is being continued from …")
140  "[Request interrupted by user]"
102  bare slash commands ("/compact")
  0  prose the user actually typed
```

**One carve-out.** 166 slash-command records carry real typed words inside `<command-args>` —
`/livecheck:run on an android device to test`, `/atlas can you look at the state of my
cc-plugins repo…` — and **all 166 are unlabelled**. So the rule is `promptSource == "typed"`
**plus** the `<command-args>` payload of slash-command records, args only, wrapper discarded.
Total genuinely-typed turns: **~1,268**.

Rejected: content-shape with an exclusion list. It was the working hypothesis and the spike
refuted it — the exclusion list would have had to name seven machine-text families to recover a
signal a single field already carries exactly.

**Safety property this creates.** The rule leans on a field absent from half of all human-shaped
records. That absence is *meaningful*, but if a future Claude Code version stops writing
`promptSource` altogether the rule degrades to returning nothing rather than to returning noise.
`recall doctor` must warn when a corpus contains human-shaped main-session records and **zero**
`typed` labels — that combination means the field vanished, not that the user stopped talking.

### Retention — the corpus deletes itself

`cleanupPeriodDays` was unset in both `~/.claude/settings.json` and managed settings, so the
documented 30-day default governed. Evidence of loss already on disk: nothing survives past 90
days, while `history.jsonl` prompts reach back to 2026-01-21 and 288 session IDs in it have no
transcript. **Set to 90 on 2026-08-13**, verified.

`~/.claude/history.jsonl` (1.8 MB, ~4,678 prompts, 358 sessions) is a partial, differently-
retained record of typed prompts only. Useful as a complementary archive of already-deleted
sessions, never as an index.

`sessions-index.json` exists in 1 of 34 project dirs and carries `summary`, `gitBranch`,
`messageCount`, `isSidechain`, `firstPrompt`. Built lazily — a hint, not a source of truth.

### Measured performance

| Operation | Cost |
|---|---|
| `stat` every file (freshness check) | 0.4 s |
| Cold strip of all 1,077 files / 1.29 GB via one `jq` process | 8–10 s |

A per-file loop spawns 1,077 processes and takes minutes; piping all files into a single `jq`
takes seconds. This is a real constraint on any implementation.

### Prior art

- `ccs` — ripgrep + Python over the same files; claims ~70ms across 1000+ sessions / 2 GB.
  Does not do repo identity, tier-labelled search, terms-present-nearby, or ranking.
- `claude-history` (Rust) — fuzzy-search TUI with field-aware ranking.
- `claude-code-log`, `simonw/claude-code-transcripts` — transcript→HTML renderers, not search.

Worth a short evaluation before building.

## Current direction
An index that is a cache of the ~9-second strip pass, not a separate structure that can
disagree with the source. Locate exactly over a complete index; return a bounded window of
turns around each hit rather than whole sessions.

## Decisions

- **Freshness by byte-offset comparison at query time** — store each file's length; unchanged →
  skip, grew → seek and read new bytes, new → read whole, shrank or mtime moved without growth →
  re-read whole. Worst case falls back to the full pass and is still correct. Rejected:
  background daemon / session hook (needs setup, can miss, and a miss is invisible).
- **Fetch returns a turn window around the hit, not the whole session** — mean session
  conversation is ~67K tokens and the largest is ~550K, so a whole-session fetch violates the
  dealbreaker. `--full` remains available behind an explicit byte cap that refuses rather than
  truncates. Rejected: byte cap with continuation cursor (multiple round trips on the 24
  sessions over 512 KB, and it keeps a mental model the data does not support), summarizing
  oversized sessions (reintroduces the lossy component rejected when the trustworthy floor was
  chosen).
- **The no-false-negatives guarantee is absolute over whatever tier was searched, and the tool
  always declares which** — superseded the earlier head/tail compromise once linear-scan cost
  was measured. Nothing is truncated: the conversation tier is scanned in full by default
  (~30 ms end to end) and tool output is scanned in full when asked (~93 ms). What makes the default safe
  is the declaration, present in every response: `conversation only — tool output NOT searched
  (--results)`. Rejected: head/tail truncation (indexes only ~19% of tool-result *text* by
  bytes, a real false-negative surface with nothing compensating for it), full-tier default
  (fine one-shot, sticky at ~93 ms per keystroke in interactive mode).
- **Subagent transcripts are indexed, labelled, and folded into the parent session** — 947 of
  1,077 files are subagent work, and a decision reached inside an Explore or review agent is
  part of what happened. Hits are tagged agent-authored via `isSidechain`; the session is the
  unit of result. Rejected: separate result type (adds a second entity to the mental model),
  exclusion (makes delegated work unfindable, and heavy delegation to subagents is routine).
- **Counts deduplicate by record uuid** — 17,659 records exist in more than one file, so hit
  counts, turn counts, and verbatim output are computed once per logical turn.
- **Lexical expansion over semantic search** — stemming and fuzzy matching, and on a miss,
  report the terms that actually exist in the corpus near the query. Converts a dead end into
  the next query, which suits an agent caller that re-queries cheaply, and adds no dependency.
  Rejected: local embeddings (heavy dependency; ranking can bury a hit — revisit only if
  empty-query logs prove it necessary), bounded LLM distillation (recurring cost forever),
  exact-only (a miss gives no next move).
- **Discovery via the main loop calling the tool directly** — bounded output removes the reason
  transcript searches were delegated, so main-loop-only awareness channels suffice; a line in
  the user's own agent definitions is the backup. Rejected: exposing it in the tool roster as
  a plugin/MCP (best discovery but violates the CLI lock-in and costs roster context),
  main-loop-only with no backup (measured evidence of failing on this machine).
- **recall is in the tool roster after all — this supersedes the discovery decision above** —
  that rejection priced roster context against discovery and concluded the CLI's own on-ramp
  was enough. The price was right; the question was wrong. The failure condition it was hedging
  against is the one `docs/requirements.md` names — *"It is correct but nobody reaches for it"* —
  and it is the one that materialised: lookups kept happening because somebody typed "go search
  the transcripts", which is exactly the awareness channel the decision assumed would not be
  needed. What ships is the option that was priced and not taken: five read-only tools
  (`recall_find`, `recall_guide`, `recall_show`, `recall_turns`, `recall_when`) over stdio, plus
  a skill whose description is written to fire on the question shapes rather than on the tool's
  name. The CLI lock-in is untouched, which is the half of the old rejection that turns out not
  to apply: `recall mcp serve` is a sub-command of the same binary, and every tool call runs the
  same verb the command line runs, through the same `openCorpus` and coverage funnel, so the two
  surfaces cannot disagree about what the corpus holds. The roster cost is paid down rather than
  denied — five tools and not six, since `recall doctor` answers a question about archive
  integrity and not about past sessions, and one of the five is the guide, which keeps the query
  semantics out of the other four descriptions and costs nothing until it is called. Rejected:
  the server without the skill (gets the tools listed, tells the model nothing about when
  reaching for them beats re-deriving, which is the failure being fixed), the skill without the
  server (leaves every lookup a shell round trip the roster never advertises).
- **The archive refreshes on every tool call, not once when the server starts** — refreshing at
  start would make every later call about 8 ms cheaper, which is the measured cost of a refresh
  that finds nothing to do (see the GOMAXPROCS decision above: 7.9 ms). It is rejected anyway,
  on two grounds. The first is that it reproduces a named failure condition from
  `docs/requirements.md` verbatim — *"It is accurate only for older material, so the conversation
  most likely to be asked about — a recent one — is the one it cannot see"* — and under a server
  the session being asked about is very often the one still being written. The second is that it
  needs a mental model this protocol revision forbids: `2026-07-28` says an open stdio connection
  is not a session (`C-20260817-sessions-and-session-id-removed`), so corpus state cached across
  calls is per-connection state under another name. The cost is 8 ms against a ~30 ms
  conversation-tier query, under a quarter of it.
- **Tool calls are serialized, one at a time** — the SDK dispatches concurrently, and the refresh
  underneath a search writes to disk: two archive updates in one process is not a supported
  state. Serialising also bounds peak memory to a single corpus load, and it is what makes the
  per-call agent selection safe: a call naming a `provider` is answered by rewriting
  `RECALL_AGENT` in the process environment and restoring it afterwards, which is only sound
  while nothing else can be running. The cost is that a client cannot overlap two recall calls;
  at ~30 ms each that is not worth engineering around.
- **Repo identity is the git remote, walked up from `cwd`** — separate checkouts and worktrees of
  the same repo all resolve to the same remote. The walk must continue past *any* failure, not only a missing
  directory: an orphaned worktree with a pruned gitdir has a `.git` file that stops a naive walk
  and errors on remote lookup. A worktree resolves via its gitdir pointer to the parent repo. A
  repo with no remote (`dev/homebase`) is a distinct "repo, no remote" identity keyed by toplevel
  path, not an unresolved one. Measured on 144 distinct cwd values: 11 outside any repo, 2
  remoteless, 14 paths no longer on disk; `relocated` records carry `relocatedCwd` and no `cwd`.
- **Default scope is the current repo; machine-wide is one flag** — with a zero-result inside
  repo scope automatically probing wider and reporting what exists elsewhere, so the dangerous
  silence cannot happen.
- **Three tiers, searched by default and tier-labelled; displayed selectively** — conversation
  (34.3 MB) indexed and shown; invocation *signatures* with payloads dropped, shown one line
  each on `--tools`; tool results indexed head+tail and shown only on `--results`. Tail matters
  because Bash failures print at the end.
- **Results group by session, and sort follows the verb** — `find` ranks by concentration,
  `when` is chronological, `--sort recent` overrides, facet summary above results. Concentration
  needs a minimum-hits floor or a shrinkage term (see Ranking evidence) and its denominator must
  be conversation turns only, not all records.
- **The tool archives, not just searches** — the stripped conversation is written to a
  compressed archive that never expires (~6 MB today). Raw retention set to 90 days, which buys
  *archiving latency* rather than retention. Rejected: 6-month archive cap (no storage argument
  against a few MB, and it caps how far "when did I first" can reach), leaving raw at 30 days.
- **Archived and live are different epistemic states, and the tool reports both boundaries** —
  once the raw file is gone the archive cannot be verified against source. The coverage footer
  reports the mtime-based live boundary (what cleanup will delete) *and* the content-date
  boundary (what the archive covers); measured divergence between them reaches **55 days**,
  because a resumed session's mtime is far newer than its content. A file that disappears
  between the archiver's `stat` and its `open` is reported, never silently skipped.
- **A decoded turn is a view into the tier file, not a copy of it** — `frames.str` returns
  `unsafe.String` into the buffer the whole tier file was read into. Copying every field cost
  53,116 allocations on a conversation-tier load of the medium generated corpus and 266,171 on
  all three; views cost 9 and 23. Load went 2.085 ms → 1.023 ms and 15.889 ms → 6.367 ms
  (benchstat, n=10, p=0.000), and on the real 280 MB archive 13 ms → 6 ms and 80 ms → 38 ms.
  The price is an invariant: a tier file is immutable while any turn decoded from it is
  reachable. Two tests hold it up — one asserts the fields really do point inside the buffer,
  because otherwise the rule would cost nothing and protect nothing, and one asserts that ten
  times the turns do not cost ten times the allocations.
- **The substring scan anchors on the needle's rarest byte, unless that is the first one** —
  `bytes.Index` scans for the first byte, and an English word starting with a common letter
  false-positives on nearly every word in the text, paying a verification compare each time.
  A static byte-frequency table picks the anchor. Worth −7% on one ordinary word, −35% on two
  with `--all-terms`, and −7% geomean over every measured query shape. The fallback is not
  optional: forcing the hand-rolled loop on needles whose first byte is already rare cost up to
  11%, because `bytes.Index` is assembly and Go is a slower route to the same answer. Rejected:
  cgo or SIMD via a dependency (both ruled out by the stack), and a rarity *threshold* rather
  than the simple first-byte test (no measurement supported a specific value, and an unmeasured
  knob is worse than none).
- **A whole-corpus pass gets GOMAXPROCS goroutines, and the corpus walk is one of them** — the
  worker cap was `min(GOMAXPROCS, 8)` with no recorded reason and it cost 17%: a cold build of
  the 1.39 GB store measured 1.169 s on eight workers against 0.972 s on sixteen, and 24 and 32
  measured 1.011 s and 0.956 s, so GOMAXPROCS is where it flattens. Separately, the pass that
  finds *nothing* to do was 13.4 ms and 9.2 ms of that was `filepath.WalkDir` waiting on 141
  per-checkout directories in turn; walking the root's children concurrently took the walk to
  3.8 ms and the refresh to 7.9 ms. Statting the 1,199 files it finds was never the cost — 1.67 ms
  sequential, 1.2 ms parallel, because a warm `stat` is nearly free.
- **The JSONL read buffer is 1 MB** — 1.39 GB through 256 KB buffers is roughly 5,700 read
  syscalls, and the same cold build measured 1.024 s at 256 KB against 0.888 s at 1 MB. Four
  megabytes measured 0.914 s, so the win is in the syscall count and not the buffer size.
  Nothing is wasted on small files: the smallest transcript in that store is over 256 KB, the
  median is 391 KB, and only as many buffers exist as there are workers.
- **The scan is sharded across cores, and the merge rule is the whole argument** — the corpus is
  cut into contiguous ranges scanned concurrently. What makes that safe is that the walk is
  order-independent in what it finishes with: `need` only rises and every rise clears the hits
  below it, so a completed walk holds exactly the turns carrying the most terms anything in its
  range carried, plus the turns one level below when the query was unsatisfiable. So the global
  answer is the best of the local bests — a range that reached the best contributes its hits and
  its below, a range that reached exactly one less contributes its hits to the below because that
  is the same level, and anything further down contributed nothing a single pass would have kept.
  Ranges are contiguous rather than strided so they concatenate back into input order and so a
  session's adjacent turns mostly stay whole. Real 280 MB store, median of 20: conversation-tier
  `find` 73.9 → 30.3 ms, all tiers 374.8 → 108.0 ms, one ordinary word 70.8 → 37.7 ms, all
  p<0.0001. The cost is allocations, which roughly double: every range sets up its own matcher
  scratch, session map and hit slice. That is per-range and not per-turn, and a test holds it to
  that by comparing two corpus sizes at a fixed range count. Rejected: striding turns across
  workers (destroys input order and splits every session).
- **The zero-result survey is sharded too, once its byte budget stops being spent during the
  walk** — the survey explains a miss, and after the scan was sharded it was the most expensive
  thing the tool did: 183 ms against 30 ms for a hit, and the only operation over a gate in the
  acceptance contract. It was left sequential on the grounds that its byte budget and its
  candidate cap are both order-dependent; both turned out not to hold. The budget is
  order-dependent only while a walk is spending it, so it is settled first instead — `fold`
  preserves every byte position, so a turn's folded length is its text length and a prefix sum
  over lengths picks the same turn the sequential walk stopped at, without folding or tokenizing
  anything. The ranges are cut from that prefix. The cap was profiled rather than reasoned about
  and reached 70 candidates against a limit of 4,096 on the worst real shape, a five-letter term
  over all three tiers where the shared-prefix family rule is loosest; it now bounds each range
  rather than their union, which is a memory bound and not an answer. What is left is two
  accumulating walks whose merge is addition. End to end against the pre-optimization binary,
  interleaved, 20 runs a shape: a conversation-tier miss 182.7 → 45.2 ms and an all-tier miss
  538.8 → 144.7 ms, both p=0.00005. Rejected: keeping the per-range cap and truncating the merged
  candidate set back to 4,096 (map iteration order decides what survives, which reintroduces
  exactly the machine-dependent answer this design exists to avoid).
- **`fold` lowercases eight bytes to the add, in Go rather than in assembly** — profiling put
  87% of a scan inside it, because the substring search it feeds is standard-library assembly
  and this was a byte at a time. Lifting `'A'` to a lane's top bit with one add and doing the
  same one past `'Z'` with another leaves that bit set exactly where the lane held an upper-case
  letter, and shifting it down two is the `0x20` that lowercases it; both addends apply only to
  lanes already known to be ASCII, so the largest lane value is `0xbe` and nothing carries into
  its neighbour. Real corpus, back to back: the hit scan 6.10 → 3.40 ms on the conversation tier
  and 31.30 → 17.43 ms across all of them, both −44%. Per byte, 3.6–6.2× on ASCII and 1.9–2.3×
  when a rune turns up every fiftieth byte. Correctness is agreement with the byte-at-a-time
  version, which stays in the test file as the reference: named boundaries, a non-ASCII byte at
  every offset across a word, the runes whose lowercase form is a different width, invalid
  UTF-8, and 47.7 million fuzz executions with no disagreement. Accepted cost: 7–9% slower on
  text that is mostly accented Latin, which is about 1.4% end to end for such a corpus. Three
  arrangements were measured trying to remove that and all three landed in the same place, so it
  is the two-loop shape and not a fixable detail; text with no ASCII at all never enters the
  wide loop.
- **Sixteen bytes at a time in NEON and SSE2, over that Go path as the fallback** — kept on a
  weaker case than anything else here, which is worth stating rather than dressing up. It
  delivers what was predicted per byte: 2.02× over the eight-byte Go loop on ASCII and 1.94× on
  real turn text. End to end it is −6.8% on an all-tier `find` (p=0.00015) and nothing
  measurable on the other two shapes tested (p=0.50 and p=1.00), because the scan is only 3.4 ms
  of a 25 ms search. It pays at all because the corpus is 99.17% ASCII and averages 328 bytes
  between runes, so a sixteen-byte block is nearly always whole. A four-refusal budget stops it
  hurting accented prose, where being refused a block means a rune within sixteen bytes and the
  call was paid for nothing: that shape returns from 0.89–0.91× to 0.99–1.05× against the Go
  path, and ASCII loses nothing. Correctness is agreement with the byte-at-a-time reference —
  42.0 M fuzz executions on arm64, 27.7 M on amd64, no disagreement — plus a test holding the
  routine to whole blocks, no write past what it reports, and stopping at a block holding a rune,
  since the lane adds are only valid where the top bit starts clear. The amd64 path's speed was
  measured on real silicon later; see "The amd64 SSE2 fold is kept" below. Rejected: runtime CPU
  feature detection for AVX2, which would
  need `golang.org/x/sys/cpu` and break the one-dependency rule for a wider register on a step
  already down to 3.4 ms.
- **Tier files are read by concurrent `ReadAt` into one buffer, not by `mmap`** — the tool
  results alone are 197 MB, and copying them out of the page cache through one byte stream is
  one core's memory bandwidth against sixteen. Over the real 282 MB archive, warm, median of
  nine: `os.ReadFile` 16.9 ms, `syscall.Mmap` plus the page faults a sequential decode has to
  take 9.0 ms, parallel `ReadAt` 5.6 ms. Archive load goes 5 → 3 ms for the conversation tier
  and 34 → 21 ms for all of them. Concurrent `ReadAt` is safe by contract: the offset is an
  argument rather than the file's position, so the goroutines share nothing to race over.
  Rejected: `mmap`, which the performance plan had called for — it wins half as much and costs a
  build-tag split, a mapping that can never be unmapped while any zero-copy string points into
  it, a truncated-file SIGBUS as a new failure mode, and a hand-written Windows path that CI
  builds but never runs.
- **Per-tier block offsets live in the tier file's header, not in `meta.json`** — the offsets let
  a decode run on every core, and they are framing metadata rather than the search index this
  project has ruled out: an offset says where a decoder may start, not where a term occurs, so it
  cannot disagree with the corpus about what the corpus contains. Where it *can* disagree is with
  the bytes, and that is why the obvious placement beside them in `meta.json` was not used:
  `writeTier` re-frames a tier only when that tier gained turns,
  while `meta.json` is rewritten on every update, so the two are designed to go out of step. In
  the file the table travels with the bytes it describes. `tierMagic` goes to `recall-turns-3`,
  which is the upgrade path — an unrecognised framing already rebuilds. Offsets are validated, not
  trusted: in bounds, increasing, starting at the body, and then each block must decode exactly the
  turns it declared and stop exactly on the next block's offset, so a table that disagrees with the
  file is refused whole and the sequential walk runs instead. Worth −8.7% and −11.1% on an all-tier
  `find` in two independent runs (p ≤ 0.0001) on linux/amd64, where all-tier load is 20 ms of 65.
  Rejected: `meta.json` per the plan (can go stale against the file it describes); a checksum over
  the table (hashing 111 MB costs more than the 15 ms it would protect).
- **The amd64 SSE2 fold is kept, and no longer as an exception** — it shipped on the assumption
  that SSE2 behaves like NEON, which was untestable on the machine that wrote it. Measured on a
  Ryzen 7 5700X3D: 2.58× the word-at-a-time path on 20 KB of ASCII, and end to end it wins four
  query shapes of five — all tiers −8.3% (p=0.0001), conversation miss −5.1% (p=0.00005), all-tier
  miss −4.3% (p=0.00015), one ordinary word −4.1% (p=0.026), conversation hit −3.0% (not
  significant). NEON on arm64 wins one of three, so the half of the assembly surface that was
  doubted now carries more evidence than the half that was not. An A/A control — the same binary
  against a copy of itself — puts the method's noise floor at ±1.5% with every p above 0.49.
  Correctness on amd64 is now native rather than emulated: 13.4 M `FuzzFold` executions against
  the byte-at-a-time reference.
- **A caseless SIMD substring matcher was dropped on the profile, not attempted** — it was the
  endgame of the performance plan, on the premise that case-folding and substring search were each
  about half the scan and one fused pass would collapse both. Re-profiled at the end of wave 2,
  the substring search is **1.4%** of the scan profile because `bytes.Index` is standard-library
  assembly, and `fold` is 81% of the hit path but already vectorized. The remaining cost is
  elsewhere: the miss path's `tokenize` is 36.4% of the profile, two and a half times the whole hit
  path. Rejected on this project's own rule that a change must beat the baseline significantly, and
  recorded because the estimate that justified it — "66 → ~8 ms" — was written when the scan cost
  66 ms and the whole conversation-tier `find` now costs 21 ms.
- **No profile-guided optimization** — measured and dropped, not skipped. Over 40 interleaved
  runs of each binary against the real archive, a PGO build was −0.36% on a conversation-tier
  find (p=0.95) and +0.52% on an all-tier find (p=0.65), with identical minima. The hot paths are
  syscalls, `memmove`, gjson parsing and tight byte loops, none of which are what PGO's
  devirtualization and cross-edge inlining improve. Revisit only if the hot profile changes shape.
- **The provider seam is an anonymous interface, and registration lives in `cmd/recall`** —
  `archive.Provider` names where an agent's transcripts live, which paths under it are
  transcripts, and how to decode them, and it is implemented in `internal/strip`, never imported
  there: `archive` stays the package that owns the corpus walk, `strip` stays the package that
  knows the record formats. What makes that hold is that `archive.Decoder` is a type alias to an
  anonymous interface literal rather than a named type — Go only treats a returned interface as
  identical for structural interface satisfaction when the literal shape matches exactly, so a
  named `archive.Decoder` would force `strip` to import `archive` purely to spell its own method's
  return type, inverting the direction this design pins. `strip` declares its own alias with the
  same method set for the same reason. Registration happens in `cmd/recall`'s own `init()` rather
  than `strip`'s: `internal/archive/perf_test.go` is an in-package test that already imports
  `strip`, so a `strip` that registered itself on import would close the cycle from either
  direction it could be broken. `main` registering a driver it depends on, the way a database
  driver does, was the only place left that owns both packages already.
- **Adding a second agent changes no on-disk format** — a Codex archive is a sibling
  `agents/codex/` directory in the same tier-file shape claude-code already writes, not a new
  field or a new magic string; `recall-turns-3` still heads every tier file. That mattered because
  a format bump forces every archive to rebuild from the live corpus on its next run, and raw
  session files past the 90-day retention window (see the retention decision above) are already
  gone from disk by the time an unrelated rebuild would happen — a rebuild reads the corpus, not
  the old archive, so it cannot recover a turn only the archive still remembers. Claude-code's own
  files stay at the archive root rather than moving under a matching `agents/claude-code/`, for
  the same reason: they were written there before there was anywhere else to put them, and moving
  them would force the one archive every existing install already has to rebuild for no functional
  gain.
- **Detection is a first-match-wins probe, cheapest and most certain first, and `CLAUDECODE` is
  never among the matches** — `CODEX_THREAD_ID` or `CODEX_SESSION_ID`, then `GEMINI_CLI`, then
  `CURSOR_AGENT`, then claude-code by default. Codex is checked on `CODEX_THREAD_ID` first because
  the locally installed binary was measured not to set `CODEX_SESSION_ID` at all — a version
  behind the one Codex's own docs describe (`C-20260817-codex-local-binary-lacks-session-id`) — so
  a probe that trusted only the documented variable would misdetect Codex on this and any
  similarly-versioned install. `CLAUDECODE=1` is real and reliable
  (`C-20260817-claude-code-sets-claudecode-1`) but is read only to populate what `doctor` reports
  as detected, never to select: it is set for every subprocess Claude Code spawns, including a
  nested agent it runs as a Bash-tool child, so treating it as a selector would misdetect that
  nested agent as Claude Code itself. `GEMINI_CLI=1` and `CURSOR_AGENT=1` are equally reliable
  (`C-20260817-gemini-cli-sets-gemini-cli-1`, `C-20260817-cursor-agent-sets-cursor-agent-1`) and
  are detected today even though neither has a registered provider yet — see the scope note in
  `docs/requirements.md`.
- **An explicit agent with no provider is an error; a merely detected one falls back and says
  so** — naming `--provider codex` (or `RECALL_AGENT=codex`) when no `codex` provider is
  registered refuses outright, because silently answering from claude-code instead would be a
  wrong answer reported as a right one. A detected agent is judged differently: one inferred from
  the environment whose session root does not exist on disk (`CODEX_HOME` unset and no
  `~/.codex/sessions`, say) falls back to claude-code and reports why, because nothing was
  promised about an agent nobody explicitly asked for — the caller asked a general question, not
  for one corpus by name, and refusing to answer at all would be a worse failure than answering
  from the corpus that does exist.
- **Codex's `response_item` records are archived; `event_msg` is counted and dropped** — a Codex
  rollout carries the same conversation twice: `response_item`/`message` with `role` `user` or
  `assistant` is the model-facing turn, and `event_msg`/`user_message` restates the same text for
  the UI (`C-20260817-codex-event-msg-duplicates-turns`, a 269:99 split in the report's own
  sample). Archiving both would show every user turn twice, so only `response_item` is read;
  `event_msg` is tallied as `Telemetry` so `doctor` can report the size of the double-count a naive
  reader would have made. This rests on an assumption the fixtures do not exercise either way: if
  Codex ever writes a user message only to the event stream, with no `response_item` twin, those
  words never reach the archive. Nothing sampled for the research report proves or disproves that
  this happens — it is a known boundary of the decision, not a closed question.
- **The archive's dedup key for a Codex record is its byte offset, not its ordinal position** — a
  rollout record carries no id of its own, and the archive deduplicates every tier on
  `(session, uuid)`, so a decoder has to synthesise both halves. An ordinal — the count of records
  this decoder has been handed — was tried and measured wrong: a resumed read is primed with the
  file's head and then continues from a byte cursor, so a counter numbers the same record
  differently on an appending pass than it would on a whole re-read, and because the archive dedups
  on that key, a rebuild after new records land would keep both numberings as two copies of every
  turn past the cursor. A record's own byte offset in the file is stable under both kinds of read,
  so the synthesised id is the offset plus an FNV-1a hash of the raw line: the offset alone would
  collapse distinct content that a fork restarts numbering from zero for, and the hash alone would
  collapse a genuinely repeated message.
- **A `.jsonl.zst` rollout is counted and left unread, never decompressed** — Codex's background
  worker rewrites cold rollouts to zstd-compressed files as part of its migration to a paginated
  JSONL-plus-SQLite thread store (`C-20260817-codex-zstd-and-sqlite-migration`); nothing in
  `recall` otherwise needs a zstd library, and adding one solely to read files Codex itself
  considers cold was not worth a second dependency (see the gjson decision below on what earns
  that place). The transcript check recognises the suffix, adds it to a `Compressed` count, and
  excludes it from the walk — handing a compressed file to the JSONL reader would report a file of
  malformed lines rather than the unread file it is. `recall doctor` declares the count rather than
  staying silent about it.
- **A `compacted` record's `replacement_history` is not archived a second time** — Codex keeps the
  pre-compaction turns inside the compaction summary itself when it writes one
  (`C-20260817-codex-compacted-keeps-replacement-history`). Those turns already reached the
  archive from their own earlier `response_item` records when they were live, so re-reading them
  out of `replacement_history` would put the same words in the store twice. The count is kept —
  `Replaced` — so `doctor` can report how much a naive reader would have double-archived, without
  archiving it.
- **Codex's day-nested rollout directories needed no new walk, and no index of the dates** —
  `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` (`C-20260817-codex-sessions-dated-layout`) nests
  three directories deeper than claude-code's one-directory-per-checkout layout, but the corpus
  walk was already written to assume nothing about depth: it recurses under every top-level entry
  until it finds files, and the top-level fan-out that makes the walk concurrent (see the
  GOMAXPROCS decision above) just sees fewer, larger top-level entries for Codex than it does for
  claude-code. Nothing was added to interpret or index the date directories — they carry no
  identity of their own, since a rollout's cwd and thread id live in its `session_meta` record and
  nowhere in its path — so a reader that already walked whole was already correct, only with less
  concurrency to exploit at the top.

## Stack

Chosen on measurement taken on this machine 2026-08-13, not on precedent alone.

- **Go, with `github.com/tidwall/gjson` as the only dependency at the time.** Matches `fp` and `atlas`
  (both Go, stdlib `flag`, Go 1.25), gives a ~5 ms process start that a per-query agent tool
  needs, and ships a single static binary with `CGO_ENABLED=0`. Measured full-corpus strip:
  **gjson 1.31 s (1011 MB/s) · Go stdlib `encoding/json` 7.21 s · `jq` 9.6 s.** gjson wins by
  parsing lazily — extracting only the requested path and skipping the rest — not by being a
  faster language; eager Go and eager C are only 1.3× apart.
  This is the first plugin here with an external dependency; accepted for a 5.5× strip pass.
- **No index and no database.** After stripping, the conversation tier is 47.8 MB of a 1.52 GB
  store; a search over it costs ~30 ms end to end, and every tier in full ~93 ms. An index would add a staleness class,
  a corruption class, and a way to silently under-report — all for a problem the corpus is too
  small to have. Rejected: SQLite FTS5 via `modernc.org/sqlite` v1.56.0, which was **verified
  working** (pure Go, SQLite 3.53.3, FTS5 with porter stemming, `bm25()`, `snippet()`,
  `PRAGMA integrity_check`, 9.2 MB static binary) — it is a real option if the corpus ever
  outgrows linear scan, but bm25 was deliberately rejected in favour of concentration ranking,
  stemming is measured worthless here, and `snippet()` is byte offsets around a match.
- **Search covers the conversation tier by default; tool output is opt-in** — and every
  response states it: `conversation only — tool output NOT searched (--results)`. The
  dealbreaker was a *silent* false negative; an explicit declaration of what was not searched
  is honest coverage, not a silent miss. Rejected: full-tier default (~93 ms is fine one-shot
  but sticky per keystroke interactively), head/tail truncation (a real false-negative surface
  with no compensating honesty).
- **Interactive mode is an fzf front-end, not a custom TUI** — a shell function over the same
  CLI commands. Zero Go dependencies added (fzf is an external binary, itself written in Go,
  already installed at 0.74.1). `--disabled` turns off fzf's own matching so the tool owns
  search and ranking; `--track` keeps the highlighted row stable as results reload; `--read0`
  plus `--gap` allow multi-line records; `--with-nth` hides the session ID while keeping it
  addressable by `--preview` and key-bindings; `--ansi` colours. All verified present on
  0.74.1. Rejected: a native Bubble Tea TUI (Bubbles v2.0.0 has the list/input/viewport
  components and would keep the corpus resident in RAM, avoiding per-keystroke process start —
  worth revisiting only if full-tier interactive search proves sticky), because it duplicates
  fzf, adds a dependency tree, and risks making something reachable only through a TUI when the
  primary caller is an agent that cannot drive one.

**Ranking speed is not a design concern.** Sorting 131 sessions, or a few thousand hits, costs
microseconds. Scanning is the only real cost.

## The filtering funnel — how 1.29 GB becomes 36.5 MB

Measured corpus-wide:

```
0. everything on disk                                 1322 MB   100%
1. keep only user + assistant records                 1191 MB    90%
2. drop toolUseResult (a duplicate of the results)     829 MB    63%
3. keep only the message body (drop per-line metadata)  706 MB    53%
4. drop tool_result blocks (command/file output)        363 MB    28%
5. drop tool_use blocks (Edit/Write payloads)           298 MB    23%
6. drop thinking signatures (opaque base64)             172 MB    13%
7. keep only actual words                              36.5 MB   2.8%
```

The largest single cut is step 2: tool output is stored **twice** per record, once in
`message.content` and again in the top-level `toolUseResult` field. Step 6 is cryptographic
signatures on thinking blocks — no words, unsearchable, 126 MB.

Steps 0–6 are progressive subtractions with some overlap slop; step 7 is a direct measurement
of what is kept and is the number to trust. Of the final 36.5 MB, 34.4 MB is conversation text
and 2.1 MB is thinking text.

## Ranking evidence

Grouping by session is most of the fix: `"wallet"` matches ~2,225 turns across 70 sessions.
Hits-per-turn separates real sessions from incidental mentions — but it fails in **both**
directions, and an earlier draft tested only one:

| session | hits / turns | ratio | |
|---|---|---|---|
| 4fa40cc0 | 31 / 192 | 0.1615 | short, genuinely about wallet |
| e5f9a621 | 1 / 13 | 0.0769 | **one passing mention — outranks the real one** |
| 6941d8f9 | 554 / 7944 | 0.0697 | the corpus's most wallet-intensive session |
| 0b040c29 | 1 / 7181 | 0.000 | huge session, one mention — correctly sunk |

Raw count and normalized density each fail alone. Density correctly sinks `0b040c29` but lets a
13-turn session with a single mention beat the 554-hit session the query is actually about.
The rule needs a minimum-hits floor or shrinkage (`hits/(turns+k)`), and the denominator must
count conversation turns only — counting tool-result records penalises a session for using tools.

Also measured: the flood case dominates the miss case. Stemming buys nearly nothing here
(`wallets` appears 0 times); the value of lexical expansion is concentrated in the
terms-present-nearby response.

## Sketches
- 2026-08-13 ascii (see Trail) — CLI surface: `find` / `show` / `when`, coverage footer,
  tier tags, terms-present-nearby miss response.

## Scope
**In:** recall over session transcripts and their subagent transcripts — locate, conclusion,
timeline, recurrence — plus a compressed archive of the stripped conversation so recall outlives
Claude Code's retention window.
**Out:** capture and curation (secondbrain / atlas / research claims are separate tools for
separate jobs). Backing the archive up off-machine is a separate decision, deliberately not
taken here.

## Open questions

Still open:

- **A format bump rebuilds rather than migrates, and a rebuild can shrink the archive.**
  `openFrames` refuses any file whose magic is not the current one, and a refused store is
  rebuilt from the raw corpus — which by then holds only what Claude Code's 90-day retention has
  not deleted. So the one guarantee the archive exists to give, that it outlives the transcripts,
  is the one an upgrade can silently take away. Two candidate fixes, neither taken: keep a reader
  per old format so the bump migrates, or refuse a rebuild that would leave the archive with
  fewer turns than it had. The refusal is cheaper and catches the case regardless of which format
  pair caused it.
- **The first query is the expensive one.** Caller-controlled volume assumes a caller who already
  knows what to ask for, and that assumption is weakest exactly when the tool is most needed. The
  lexical-expansion decision narrows it — a miss reports which terms are carried by nothing and
  the corpus words nearest the ones that are — without resolving it. `docs/requirements.md`
  carries the same question from the requirements side.
- **Nothing measures `--words` on against off.** Counting lines and words is a second pass over
  the scanned bytes, which is why it is opt-in; the 3% budget that forced it there was a
  pre-registered gate, and no benchmark now compares the two, so a change that makes counting
  always-on again would not be caught.
- **`archive.Options.Strip` has no production caller.** Its own doc says it retires once the
  searching verbs read through a `Provider`, which they now do. Only the benchmark harness and
  the legacy-output comparison still pass it, and retiring it means deciding what happens to that
  comparison.

Settled by what shipped, previously open:

- **fzf field indexing with multi-line records** — `find --fzf` emits NUL-terminated
  `<session id>\x1f<block>` records and `shell/recall.zsh` is the front end, so `--read0` carries
  the multi-line block and no field-splitting is needed.
- **What the parser does with an unrecognised record type** — neither ignored nor refused:
  counted by type and reported, so an unknown type is visible as a number rather than as a
  missing result.
- **Archive integrity** — `recall doctor` checks each tier's frames, the metadata and the cursor,
  and reports the store as found *and* as it stands after a forced refresh. The two disagreeing
  is the alarm; a first run, where there was nothing to have been corrupt, says so rather than
  passing.
- **Evaluate `ccs` first** — moot; recall shipped and covers all four question shapes.
- **Implementation language** — Go, on the measurement recorded above rather than on precedent.
- ~~Which human-turn discriminator is correct~~ — settled 2026-08-14 by spike, see §Human-turn
  discrimination. `promptSource == "typed"` plus `<command-args>` payloads; ~1,268 turns.
  Content-shape refuted.

Resolved by review, previously open:
- Thinking blocks: **include the 2.09 MB of thinking text** (~6% archive cost, not 6×), and
  record that 94.5% of reasoning is not persisted so it cannot be relied on for conclusions.
- Subagent turns: indexed, labelled, folded into the parent session.

## Trail
- 2026-08-13 [research] measured the corpus; concluded conversation is ~1% of bytes.
- 2026-08-13 [options] shape fork → chose index-as-cache-of-the-strip-pass; rejected
  distillation (lossy in the dealbreaker direction) and pure scan (too slow per query).
- 2026-08-13 [options] recall fork → lexical expansion; rejected semantic, distillation, exact-only.
- 2026-08-13 [options] discovery fork → main loop calls it directly, agent definitions as backup.
- 2026-08-13 [options] ranking fork → sort follows the verb.
- 2026-08-13 [research] audit + web search → found retention deleting data (288 sessions already
  lost), `pr-link`/attribution facets, prior art, and the `history.jsonl` partial record.
- 2026-08-13 [decide] archive unbounded, raw retention 90 days; `cleanupPeriodDays: 90` written.
- 2026-08-13 [challenge] adversarial hypothesis test, verdict **DENIED**, 12 findings accepted.
  Supersedes: the ~19 KB/session figure (actually 268 KB mean), the 81%-under-2KB figure
  (72.4%), the absolute no-false-negatives claim (now per-tier), the `promptSource` version-drift
  example (wrong), the "isSidechain on every record" claim (4 of 20 types), the thinking-block
  size premise (97% signature), and the one-session-per-file assumption (17,659 duplicates).
- 2026-08-13 [decide] turn-window fetch; per-tier guarantee; subagents indexed and folded in.
- 2026-08-13 [research] stack measured on this machine: gjson 1.31 s vs stdlib 7.21 s vs jq
  9.6 s over the full corpus; linear scan of the 36.5 MB stripped corpus ~35 ms; SQLite FTS5
  verified working in pure Go. Measured the filtering funnel (1322 MB → 36.5 MB, 36×).
- 2026-08-13 [decide] Go + gjson, no index, no database. **Supersedes** the per-tier
  head/tail guarantee: linear scan makes truncation unnecessary, so nothing is truncated and
  the default tier is declared in every response instead.
- 2026-08-13 [decide] interactive mode is an fzf front-end (`--disabled` + `change:reload` +
  `--preview`), not a custom TUI; Bubble Tea rejected but recorded as the fallback if
  per-keystroke process start proves sticky.
