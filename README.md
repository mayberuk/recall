<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/banner-dark.svg">
    <img src="docs/assets/banner.svg" width="100%"
         alt="recall, a line-printer ledger sheet: the command recall find idempotency returning two sessions, above the four-line footer that states what was not searched.">
  </picture>
</p>

# recall

**Search every past coding-agent session on your machine, from any checkout, in milliseconds.**

[![CI](https://github.com/mayberuk/recall/actions/workflows/ci.yml/badge.svg)](https://github.com/mayberuk/recall/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mayberuk/recall?sort=semver)](https://github.com/mayberuk/recall/releases/latest)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](go.mod)
[![MCP](https://img.shields.io/badge/MCP-2026--07--28-6E56CF)](docs/mcp.md)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

You solved this before. Two months ago, in a branch you've since deleted, in a session you can't
find. Your agent doesn't remember it, and neither do you. Only that it happened.

`recall` finds it. One query reaches every session your coding agent has ever recorded on this
machine, across every checkout, clone and worktree of the repo you're standing in. No index, no
per-project setup, no daemon. Ask from a shell, or let your agent ask over MCP.

```console
$ cd ~/src/payments        # the main checkout
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

That answer came out of a **different checkout** (`payments-hotfix`, a branch that no longer
exists locally), found from `payments` without naming it. Every block in this README is real
output you can reproduce: `./scripts/demo.sh` builds a fixed corpus and runs them.

## Why it exists

Claude Code keys sessions by **checkout path** (`~/.claude/projects/-home-you-src-payments/…`).
One logical repository sprawls across clones, worktrees and relocations, so a search scoped to
the directory you happen to be in silently misses the answer. That is the failure this was built
for: a conclusion hunted for in one checkout that turned out to live in another checkout of the
*same* repo.

`recall` resolves a repo by its **git remote**, not its path, so every checkout is one corpus.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/mayberuk/recall/main/install.sh | sh
```

Downloads the binary for your platform, verifies it against the release's published sha256, and
installs to `~/.local/bin`. It registers recall with nothing: that is a separate, deliberate step.

<details>
<summary>Other ways in</summary>

```sh
go install github.com/mayberuk/recall/cmd/recall@latest   # needs Go 1.25+

# from a checkout
git clone https://github.com/mayberuk/recall.git && cd recall
./scripts/install-from-source.sh    # builds, verifies, installs to ~/.local/bin
```

Pin a version with `RECALL_VERSION=v1.1.0`, or change the destination with `RECALL_INSTALL_DIR`.
Pre-built binaries for Linux, macOS and Windows on amd64 and arm64 are attached to
[every release](https://github.com/mayberuk/recall/releases/latest).
</details>

`recall` is a single static binary (`CGO_ENABLED=0`) with two dependencies and no configuration.
Nothing to initialize: a directory you never set up is exactly the one you'll want to search.

## Let your agent ask

`recall` speaks the [Model Context Protocol](https://modelcontextprotocol.io) over stdio. One
command registers it and installs the skill that makes an agent actually reach for it:

```sh
recall mcp install claude-code    # or codex · gemini · copilot · cursor · opencode · windsurf
```

`recall mcp config <client>` prints the same entry and **writes nothing**, if you'd rather paste it
yourself. Five read-only tools (`recall_find`, `recall_turns`, `recall_show`, `recall_when`,
`recall_guide`), each running the CLI verb it's named after, so a tool call and a command line can
never disagree about what the corpus holds. Full client-by-client detail: **[docs/mcp.md](docs/mcp.md)**.

<details>
<summary>Or have your coding agent install it for you</summary>

Paste this to any agent with shell access:

> Install `recall`, a CLI that searches my past coding-agent sessions, by running
> `curl -fsSL https://raw.githubusercontent.com/mayberuk/recall/main/install.sh | sh`.
> Then register it with yourself by running `recall mcp install <your client>`. Run
> `recall mcp config --help` to see the supported client names and pick the one that matches you.
> Finally run `recall guide` and tell me in one line what it can answer.

</details>

## What it answers

| Question | Command |
|---|---|
| Which session was that? | `recall find <query>` |
| What did we conclude, and why? | `recall turns <query>` · `recall show <session>` |
| When did this first come up? | `recall when <query>` |
| Have I hit this before, anywhere? | `recall find <query> --all` |
| Is the archive healthy? | `recall doctor` |
| Which command do I want? | `recall guide` |

Searching in the repo you're in but the answer is elsewhere? It says so, and hands you the query:

```console
$ recall turns "connection pool" --limit 1
no turns carry "connection pool" in github.com/acme/payments
found elsewhere: 5 hits in 1 other repo
  github.com/acme/infra                    5 hits · 1 session
run: recall find 'connection pool' --all
```

Place a topic in time with `recall when`:

```console
$ recall when "rate limit"
"rate limit"  first 2026-05-12 · last 2026-07-21 · 8 hits in 2 sessions
  2026-05     6 hits · 1 session
  2026-07     2 hits · 1 session
```

More worked examples, with output: **[docs/examples.md](docs/examples.md)**.

### Interactive, if you have fzf

```sh
source shell/recall.zsh
recall-fzf idempotency          # live finder; prints the chosen session id
recall show "$(recall-fzf --ids idempotency | head -1)"
```

Type and the list re-searches; `ctrl-o` opens the whole session in your pager, `enter` prints
its id and exits, so it composes with everything above. recall owns matching and ranking
(fzf's own are turned off), which is why the ordering you see is still concentration ranking.

With no terminal the same function prints ranked records to stdout instead, so it works in a
pipeline: `recall-fzf --ids <query>` gives one session id per line.

## Fast enough that an index would be a liability

Of everything Claude Code writes to disk, the actual conversation is about **3%**: 47.8 MB of a
1.52 GB session store. The rest is tool output stored twice, thinking-block signatures and
attachments. Searching that 3% is quick enough that there is nothing left for an index to win.

### On a real session store

| Query, end to end (process start → rendered output) | 1.0.0 | now |
|---|---:|---:|
| `find` over the conversation tier | 88 ms | **30 ms** |
| `find` over every tier, tool output included | 460 ms | **93 ms** |
| a miss over every tier (the expensive path) | 549 ms | **113 ms** |

*143 sessions · 192,000 turns · 1.52 GB · AMD Ryzen 7 5700X3D.*

### On a corpus you can reproduce

Every figure below comes from a corpus generated from a fixed seed rather than from anyone's
session store, so `make bench` on your machine measures the same bytes. **Small** is 5.1 MB of
transcript across 17 files (2,972 turns); **medium** is 51.4 MB across 56 files (29,564 turns).

The whole binary, invoked end to end (process start, freshness check, search, render, exit):

| command | small | medium |
|---|---:|---:|
| `recall find` | 4.0 ms | **8.5 ms** |
| `recall find --all` (every repo on the machine) | 3.9 ms | 8.2 ms |
| `recall find --results` (tool output too) | 6.3 ms | 16.3 ms |
| `recall turns` | 3.8 ms | 7.8 ms |
| `recall show` | 3.7 ms | 7.7 ms |
| `recall when` | 3.7 ms | 7.9 ms |
| `recall guide` | 2.7 ms | 2.7 ms |

Most of those milliseconds are process start and getting the corpus into memory. The search itself
is the small part:

| Over the loaded 51.4 MB corpus | time | memory | allocations |
|---|---:|---:|---:|
| scan, single term | **0.34 ms** | 47 KB | 261 |
| scan, phrase | 0.48 ms | 211 KB | 345 |
| scan, two terms in conjunction | 3.24 ms | 28 MB | 526 |
| scan, a miss: every byte read, nothing found | 2.41 ms | 115 KB | 378 |
| freshness check, nothing changed on disk | 0.47 ms | 169 KB | 889 |
| load turns, conversation tier | 1.00 ms | 8.7 MB | 30 |
| load turns, every tier | 4.99 ms | 51 MB | 95 |

Finding a term across 51 MB of transcript costs **261 allocations**. That is what having no index
buys: no B-tree to walk, no posting lists to merge, nothing to hold in sync with the corpus.

### The thresholds it has to clear

`make bench-gate` fails the build on a breach. The limits sit where the *architecture* would come
into question (a search you can feel, an update you would notice), not next to the current
measurement, so a breach means linear scanning stopped being viable rather than that a benchmark
wobbled.

| gate (medium corpus) | limit | measured |
|---|---:|---:|
| `find`, conversation tier | 250 ms | 0.3 ms |
| `find`, all tiers | 1200 ms | 0.8 ms |
| archive load, conversation tier | 195 ms | 1.3 ms |
| archive load, all tiers | 890 ms | 5.7 ms |
| incremental archive update | 1500 ms | 0.6 ms |
| cold strip of the whole corpus | 4000 ms | 185.3 ms |
| cold archive build | 4000 ms | 467.5 ms |

Allocation counts are gated separately, against a committed baseline, because they are the same
number on any machine where a nanosecond is not. `make bench-compare` fails on a 2% regression.

Full tables (32 micro benchmarks, 48 end-to-end scenarios, and the corpus they ran against) are
in [bench/RESULTS.md](bench/RESULTS.md), regenerated by `make bench`.

So there is no index: nothing to rebuild, nothing to go stale, no corruption class to guard
against. A SQLite FTS5 prototype was built and worked; it lost on exactly those grounds.

## It tells you what it didn't search

Every searching command ends in a `──` footer naming what it left out: tier, scope, exclusions,
and what the search cost. **If the footer doesn't mention a narrowing, that narrowing didn't
happen.** Treat it as the coverage contract, not decoration.

```
── 3 sessions · 3 searched · conversation only — tool output NOT searched (--results)
── live to 2026-05-12 · archived before that · refreshed just now
── scanned 3.0 KB · 14 turns · 6 ms
── 1.0 KB · ~264 tokens
```

That last line is the one agents care about: every answer states its own size and rough token
cost, and `--budget 2000` shapes an answer down to fit. Recovering a conclusion should not cost
more context than re-deriving it.

## Agents it reads

| Agent | Session store | Status |
|---|---|---|
| Claude Code | `~/.claude/projects` | read |
| Codex CLI | `~/.codex/sessions` (`$CODEX_HOME`) | read |
| Gemini CLI, Cursor | not read | detected, falls back to Claude Code and says so |

`--provider auto` (the default) detects who is asking; a name pins one; `all` searches every
registered agent at once. Naming an agent this build cannot read is refused outright rather than
answered from a corpus that isn't theirs.

Codex archives to its own tree, so adding it never rebuilds an existing Claude Code archive.

## Documentation

| | |
|---|---|
| [recall.mayberuk.com](https://recall.mayberuk.com) | the site: what it does, and the numbers behind it |
| [Documentation](https://recall.mayberuk.com/docs) | every command, flag, exit code and MCP tool, on one page |
| [docs/mcp.md](docs/mcp.md) | MCP tools, every client's config path, what `install` writes |
| [docs/examples.md](docs/examples.md) | worked examples for all six commands |
| [docs/agents.md](docs/agents.md) | exit codes, `--json`, the coverage contract, written for agents |
| [bench/RESULTS.md](bench/RESULTS.md) | every benchmark, its corpus, and the gates it ran against |
| [CHANGELOG.md](CHANGELOG.md) | **read before upgrading**: v1.1 rebuilds the archive |
| [AGENTS.md](AGENTS.md) | build, test and style contract for agents working *on* recall |

## License

MIT. See [LICENSE](LICENSE).
