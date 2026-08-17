# Requirements briefing: machine-wide recall over past Claude Code sessions

## Objective
A single way to ask what was said in any past session on this machine — locate the session,
recover a conclusion, place something in time, or find prior occurrences — and get an answer
without pulling transcript text into the asking session's context.

## Why (what the outcome enables)
Points made, decisions reached, and session handles disappear, and the only way to get them
back today is to send an agent grepping through whichever project directory happens to be the
current one. That misses — the store keys by checkout path, so one logical repo is spread
across many directories — and it costs the asking session a large amount of context to fail.
The corpus is 1,063 sessions / ~1.4 GB across 34 project directories and grows daily.

## Hard constraints
- **Delivered as a command-line tool, not a Claude Code plugin** (user-fixed lock-in) — the
  delivery form is decided; how agents come to know it exists is a separate question, settled
  below under Decided.
- **One query reaches the whole machine** — every project directory, every clone, and every
  worktree of the same repository. Scoping to the current directory reproduces the failure
  this exists to fix: one team's mobile app repository spans 13 separate directories, and the
  answer was in a different one than the search.
- **Query and result cost a small, bounded number of tokens** — an unbounded response would
  reproduce the context bloat that motivates the tool.
- **All four question shapes are served; none is cut for a first version** — locate a session,
  recover a conclusion and its reasoning, place a topic in time, find prior occurrences. This
  was challenged twice (once as a forced single-choice priority) and held both times.
- **No per-project setup** — no per-repository configuration or enablement step. A directory
  that was never initialized is exactly the one that will be searched.
- **No third-party service receives transcript content, and no bulk copy of the corpus leaves
  the machine** — content already reaches Anthropic during normal sessions and that baseline
  is accepted; a new vendor, or shipping the whole corpus regardless of what is ever asked,
  is not.
- **The caller decides how much comes back** — volume is controlled by whoever is asking, not
  fixed by the tool.

## Soft preferences
- The exact shape of a response — a handle, the verbatim passage, or a synthesized answer —
  is open. Giving up caller control over it would mean every lookup costs the same as the most
  expensive one.

## Dealbreakers
- Reporting that nothing was found when the thing is present. A confident wrong negative is
  worse than a slow answer: it sends the search elsewhere.
- Any single lookup capable of loading a multi-megabyte transcript into the asking context.

## Success conditions
- Today's failure inverts: sitting in one checkout of a multi-checkout repo, one query returns
  the session that lives under a different checkout of the same repo, with no agent reading
  either directory.
- A question of each of the four shapes, asked against a topic known to be present, returns
  the right session — regardless of which directory the question was asked from.

## Failure conditions
- It is correct but nobody reaches for it — lookups still happen by hand-typing "go search the
  transcripts", because nothing tells an agent mid-task that the capability exists.
- It answers cheaply for locate-style questions and lossily for conclusion-style ones, so the
  reasoning behind a past decision still requires opening the raw file.
- It is accurate only for older material, so the conversation most likely to be asked about —
  a recent one — is the one it cannot see.

## Explicitly out of scope
- **Capture and curation.** The existing vault (`secondbrain`), the per-project decisions
  ledger (`atlas`), and the research claims store are different tools for different jobs and
  are not the gap here. Raw session transcripts are the only complete record of what was said,
  and nothing indexes them today. This is a recall layer over that record, not a new capture
  discipline.

## Decided
Three questions were open when this brief was written and were settled during design (see
`docs/design.md` for the measurement behind each):

- **No-false-negatives is absolute per tier searched, not absolute everywhere, and the tool
  always declares which tier ran.** The alternative — an exhaustive read every query, or a
  derived structure provably current — was rejected once linear-scan cost was measured: the
  conversation tier scans in full by default (~30 ms end to end) and tool output scans in full
  when asked (~93 ms), so nothing needs truncating and the honest-coverage form costs nothing extra.
- **Cheap-in-context wins; conclusion recovery is served by a bounded turn window, not the whole
  session.** A hit returns the turns around it rather than the full conversation — mean session
  size is ~67K tokens and the largest is ~550K, so an unbounded fetch would violate the token
  budget on its own. `--full` stays available behind an explicit byte cap that refuses outright
  rather than silently truncating.
- **Discovery is the main loop calling the tool directly; `recall guide` is the on-ramp.**
  Bounded, cheap output removes the reason transcript searches used to get delegated to a
  subagent in the first place, so making the capability known to the primary loop — a line in
  agent instructions, or running `recall guide` — is enough. Exposing it as a plugin/MCP tool
  was rejected: better discoverability, but it costs every session's tool-roster budget just to
  be listed, and only agents that reach for a CLI benefit from it existing as one.

## Open questions
- Freshness: must a conversation from an hour ago be findable, or is "yesterday and older"
  sufficient? Asked, not answered by this brief; see the freshness decision in `docs/design.md`
  for how the shipped tool actually behaves.
- The first query is the expensive one — a caller hunting a half-remembered topic does not yet
  know what to ask for or what to request back. Caller-controlled volume assumes a caller who
  already knows; that assumption is weakest exactly when the tool is needed most. Unresolved as
  a design problem; `docs/design.md`'s lexical-expansion decision narrows it (a miss reports
  nearby terms so a bad first query still has a next move) without fully resolving it.

## Constraints challenged and upheld
- *All four question shapes matter equally* — challenged twice, once by forcing a single-choice
  priority with the cost of conceding each alternative spelled out; kept, because no shape is
  acceptable to lose.
- *No false negatives* — challenged with the unlock that honest self-reported coverage buys a
  much faster tool without misleading the user; resolved above under Decided, in the
  honest-coverage form.
- *Command-line tool, not a plugin* — challenged not on the form but on the discovery hole it
  leaves; kept as the delivery form. The discovery hole itself is resolved above under Decided.
- *No per-project setup* — challenged and found costless: the session store is already a single
  central location, so nothing per-repository is required under any approach. Upheld free.
- *Fully local* — challenged and **relaxed**. The original framing was incoherent given that
  transcript content already reaches Anthropic during ordinary sessions. Rewritten as: no
  vendor beyond that baseline, and no bulk copy of the corpus off the machine.
