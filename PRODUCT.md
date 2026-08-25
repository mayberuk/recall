# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Astro, deploying to GitHub Pages or Vercel (user decision, 2026-08-24). The promotional site is
greenfield: the repository contains a Go CLI and no web scaffold of any kind. Astro was chosen
over hand-written HTML and over Next.js for near-zero shipped JS with room to absorb docs pages
later.

Note the tension this creates and do not resolve it by drift: the product it sells has exactly
two direct dependencies and a build gate (`scripts/deps-gate.sh`) that fails on a third. The site
may carry a toolchain; it should not carry a heavy runtime, and shipping a JavaScript-dependent
hero would contradict the thing being sold.

## Users

Developers who run coding agents locally (Claude Code and Codex CLI today) and who have
accumulated months of session transcripts on one machine.

The situation is specific and is the reason the product exists: the user knows they solved
something before, cannot find which session it was in, and their agent has no memory of it. The
job is retrieval under uncertainty: locate the session, recover the conclusion and its
reasoning, place a topic in time, or find whether a problem has come up before.

A second user is the agent itself. Five read-only MCP tools expose the same four questions, so an
agent can answer "have we hit this before" without a human typing "go search the transcripts."
Both audiences read the same output.

## Product Purpose

One query reaches every past coding-agent session on the machine and returns an answer without
pulling transcript text into the asking session's context.

It exists because coding agents key transcripts by checkout path, so one logical repository
sprawls across clones, worktrees and relocations, and a path-scoped search silently misses. recall
resolves a repository by its git remote instead, making every checkout one corpus.

Success is a lookup that would otherwise have cost a long, failed grep, answered instead in milliseconds,
at a stated token cost, with an explicit statement of what was not searched.

## Positioning

Three claims a neighbouring tool could not truthfully copy:

- **It states what it did not search.** Every searching command ends in a `──` footer naming the
  tier, scope and exclusions in force, plus what the search cost. If the footer does not mention a
  narrowing, that narrowing did not happen. This is a contract, not a status line.
- **There is no index.** Nothing is built, nothing goes stale, nothing can corrupt, no daemon
  runs, no per-project setup exists. The corpus is read in full on every query because reading it
  measured fast enough that an index would be a liability.
- **One query reaches the whole machine.** Repository identity comes from the git remote, walked
  up from the working directory, so clones and worktrees resolve to a single corpus.

## Operating Context

Invoked from a terminal, in a checkout, mid-task, usually while the user is blocked on something
they suspect they already solved. Also invoked by an agent over MCP on stdio, inside a session the
user is watching.

Output lands in terminals the user has themselves themed (Solarized, Gruvbox, Nord, stock light,
stock dark), and is routinely piped to `less`, redirected to files, pasted into issues, and parsed
by agents via `--json` and `--format jsonl`. An optional zsh + fzf front end (`shell/recall.zsh`)
gives a live-filtering finder with a preview pane.

## Capabilities and Constraints

- Six commands: `find` (which session), `turns` (the passages), `show` (a conclusion in context),
  `when` (place a topic in time), `doctor` (archive integrity), `guide` (which command to use).
- `recall mcp serve` exposes five read-only MCP tools over stdio, protocol revision `2026-07-28`,
  running the same verbs through the same coverage funnel as the command line.
- Reads Claude Code (`~/.claude/projects`) and Codex CLI (`~/.codex/sessions`). Gemini CLI and
  Cursor are detected, fall back to Claude Code, and say so.
- Go 1.25, `CGO_ENABLED=0`, static binary, eight cross-compiled targets, two direct dependencies.
- Exit codes are part of the interface: `0` hits, `1` searched and matched nothing, `2` bad usage,
  `3` archive unreadable, `4` answer refused for exceeding `--max-bytes`. **Exit 1 is an answer,
  not a failure.**
- Output is bounded and self-describing: every answer states its own byte size and rough token
  cost, and `--budget N` shapes an answer down to fit.
- Install is `curl … | sh` with sha256 verification against the release manifest, or from source.
  Registration with an agent is a separate, deliberate step (`recall mcp install <client>`).
- **The CLI currently emits no colour at all**: no ANSI escape, no `isatty` check, no `NO_COLOR`
  handling. Structure is carried entirely by typography: `«»` around matches, `──` prefixing
  footer lines, `·` separating facts, `>` marking the focused turn in `show`. Any styling added
  later reinforces those; it never replaces them.

## Brand Commitments

- **The name is lowercase `recall`** in binary, prose, and docs, without exception. Confirmed
  binding by the user, 2026-08-24. A wordmark follows it.
- Canonical home is `github.com/mayberuk/recall`, MIT licensed. No domain is owned yet.
- The documentation voice is measured, specific and unhyped, and it is load-bearing rather than
  incidental: the docs say things like "a ratio nobody can reproduce is a number nobody should
  trust." Marketing copy that oversells would read as a different product than the one shipped.

## Evidence on Hand

Real, and usable in marketing without qualification:

- `bench/RESULTS.md`: 32 micro benchmarks, 48 end-to-end scenarios and 7 wall-clock gates,
  regenerated by `make bench` against a seed-generated corpus rather than any private session
  store, so a reader can reproduce them. Last measured 2026-08-24 on a Ryzen 7 5700X3D.
- `scripts/demo.sh`: builds a fixed corpus and runs every command the README quotes, so every
  output block in the docs is reproducible rather than illustrative.
- Headline measured figures: conversation-tier `find` 30 ms end to end on a real 1.52 GB store;
  0.34 ms for the scan itself on a seeded 51.4 MB corpus, at 261 allocations and 47 KB.
- Shipped v1.1.0 with seven signed release assets and a green CI history.

Absences that future work must not paper over: **no testimonials, no named users, no case
studies, no press, and as of 2026-08-24 zero stars, zero forks and six total release-asset
downloads.** There is no adoption story yet. A site that implies one would be lying, and the
product's entire positioning is that it does not overstate what it knows.

## Product Principles

1. **State the boundary of what you know.** The coverage footer is the product's character in one
   line. Anything built around recall (docs, site, interface) inherits the obligation to be
   explicit about limits rather than quietly confident.
2. **Reproducible over impressive.** Every number carries the harness that produced it. A claim
   the reader cannot re-run does not get made.
3. **Nothing to maintain.** No index, no daemon, no per-project config, no setup step. Additions
   that create something to keep fresh are working against the premise.
4. **Bounded by default.** The caller decides how much comes back; every answer declares its own
   cost. Unbounded output would reproduce the context bloat the tool exists to prevent.
5. **Degrade, never break.** Output survives being piped, redirected, parsed and read on a
   two-release-old terminal. Enhancements are additive to a plain-text floor that always works.

## Accessibility & Inclusion

- Colour may never be the sole carrier of meaning anywhere in the product. The output is piped,
  redirected and machine-parsed, and the coverage footer is a correctness contract, a distinction
  encoded only in colour breaks the moment output leaves a terminal.
- Any author distinction (`human` / `assistant` / `agent` / `system`) must survive deuteranopia and
  protanopia, since it is a distinction the user actively scans on.
- Terminal output must honour `NO_COLOR`, degrade through truecolor → 256 → 16 → none, and stay
  byte-clean under `--json` and `--format jsonl`.
- The site targets WCAG 2.2 AA. No user-specific accessibility requirement beyond that has been
  established.
