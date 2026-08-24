# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

Nothing yet.

## [1.1.0] - 2026-08-23

### Added

- **`install.sh` now installs a released binary rather than building from a checkout.**
  `curl -fsSL https://raw.githubusercontent.com/mayberuk/recall/main/install.sh | sh` detects the
  platform, downloads the matching release asset, **verifies it against the release's published
  sha256** and refuses to install on a mismatch, then puts it in `~/.local/bin`. `RECALL_VERSION`
  pins a version and `RECALL_INSTALL_DIR` moves the destination. Until now there was no way to
  install recall without a Go toolchain, despite the release workflow already publishing six
  cross-compiled binaries and their checksums. The build-from-a-checkout script it replaces is
  still here, as `scripts/install-from-source.sh`.
- **`AGENTS.md`, `llms.txt` and `ai.txt`.** `AGENTS.md` is the build, test and style contract for
  an agent working on recall's own source, in the format Claude Code, Codex, Cursor, Copilot,
  Gemini CLI and Windsurf all read natively. `llms.txt` is the curated map of the documentation.
  `ai.txt` was proposed as an AI-training opt-out; recall is a tool for AI agents to call, under a
  licence that already grants what a refusal would withhold, so its copy grants those uses
  explicitly and spends the rest of the file pointing an agent at what it needs.
- **`scripts/demo.sh` and the fixed corpus behind it.** Builds recall, writes five plausible
  sessions across three repos — one of them checked out twice — and runs every command the docs
  quote. Each example in the README and in `docs/examples.md` is now output a reader can
  reproduce, rather than output pasted from a store nobody else can see.
- `recall` now serves an agent over the Model Context Protocol, so the questions it answers no
  longer have to be typed at a shell to be asked. `recall mcp serve` speaks MCP revision
  `2026-07-28` over stdio and exposes five read-only tools — `recall_find`, `recall_guide`,
  `recall_show`, `recall_turns` and `recall_when`. Each one runs the CLI verb it is named after,
  so a tool call and a command line cannot disagree about what the corpus holds or about what
  the coverage footer declares. `recall doctor` is deliberately not among them. Registering it
  is two steps and both are yours to take: `recall mcp config <client>` prints the entry and the
  file it belongs in, and writes nothing; `recall mcp install <client>` is the opt-in, which
  runs the client's own `mcp add` where there is one, and otherwise inserts recall's single key
  into the config file after copying the original to `<file>.recall-backup`. Claude Code, Codex
  CLI, Gemini CLI, GitHub Copilot CLI, Cursor, OpenCode and Windsurf are known; Windsurf is
  print-only, because its documented config path and its third-party ones disagree. An install
  also puts recall's skill where that client reads skills from, which is what gets the tools
  reached for rather than merely listed, and `recall mcp export <dir>` writes the skill, the
  editor rules and the plugin manifests out for a client's own plugin command to consume.
  `install.sh` now points at both commands and still registers recall with nothing itself.
  **This drops Go 1.24**: the MCP SDK declares `go 1.25.0`, so the module's floor is now 1.25.
- `recall` now reads more than one coding agent's session store. Codex CLI is registered
  alongside Claude Code: rollouts under `~/.codex/sessions` (or `$CODEX_HOME/sessions`) archive
  to their own `agents/codex/` tree, built separately the first time anything asks for it — an
  existing Claude Code archive is untouched and **requires no rebuild** to pick this up.
  `--provider` (or `RECALL_AGENT`) chooses which agent's transcripts a run reads: `auto` detects
  from the environment, an agent name pins one, `all` reads every registered agent whose session
  store exists. `find`, `turns`, `when`, `show` and `doctor` all read whichever agent that
  selection resolves to; naming one with no registered provider is refused outright rather than
  answered from the corpus that does exist. See the README for detection order and the rest of the detail.

### Changed

- **The README is a front door rather than a manual.** The reference material it carried moved out
  intact, to `docs/mcp.md` (the five tools, every client's config path, what `install` writes),
  `docs/examples.md` (worked examples for all six commands) and `docs/agents.md` (exit codes,
  machine output forms, the coverage contract, choosing a provider).
- **`archive.Options.Strip` is gone.** It predated the `Provider` interface and its own comment set
  the condition for removing it: it goes once the searching verbs read through a provider, which
  they now all do. It had no caller in the shipped binary. The benchmark harness and the
  integration gate that still passed it now name `strip.ClaudeCode()` outright. `Options.Root`
  stays, as the session-store override those harnesses actually wanted; it no longer decides
  whose store it is.
- Searching is several times faster, with no change to what any command returns. Against 1.0.0 on
  the machine the work started on: a conversation-tier `find` 88 → 30 ms, an all-tier `find`
  460 → 93 ms, and a miss over every tier 549 → 113 ms. The corpus is still scanned in full and
  there is still no index; `bench/RESULTS.md` carries the reproducible figures behind that, taken
  against a seeded corpus rather than anyone's session store.
- The archive format is now `recall-turns-3`, which adds per-tier block offsets so a decode can
  run on every core. **The first run after upgrading rebuilds the archive**, as it does for any
  format change. Until it has, `--no-update` refuses with exit 3 and points at `recall doctor`
  rather than reading the old format wrongly. Downgrading rebuilds in the same way.

  **Back the archive up before that first run.** There is no reader for the old format, so the
  bump rebuilds rather than migrates, and a rebuild can only re-derive turns from raw transcripts
  that still exist. Claude Code deletes raw sessions after 90 days. Anything the archive held
  whose raw session is already gone does not survive the upgrade — silently, and with nothing to
  recover it from, which is the opposite of the guarantee an archive exists to give. Copy
  `$RECALL_HOME`, or `~/.local/share/recall` if that is unset, first. Keeping a v2 reader so the
  bump migrates, or refusing a rebuild that would shrink the archive, is unresolved.

## [1.0.0] - 2026-08-15

First release.

- `recall find` — locate the sessions that talked about something, ranked by concentration.
- `recall turns` — the matching passages themselves, ranked across every session.
- `recall show` — recover a conclusion with the turns around it, or a whole session's tail.
- `recall when` — place a topic in time, chronologically.
- `recall doctor` — archive integrity, coverage boundaries, format drift.
- `recall guide` — which command answers which question, read first.
- Machine-wide by default within a repo (every checkout, clone, and worktree that shares a git
  remote); `--all` reaches every repo on the machine.
- No index: the stripped conversation tier is small enough that a full linear scan is fast
  enough, so there is no staleness or corruption class to guard against.
- Every searching command reports what it did not search, in a `──` coverage footer.
