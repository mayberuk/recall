# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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

- Searching is several times faster, with no change to what any command returns. Against 1.0.0 on
  the machine the work started on: a conversation-tier `find` 88 → 30 ms, an all-tier `find`
  460 → 93 ms, and a miss over every tier 549 → 113 ms. The corpus is still scanned in full and
  there is still no index; the Decisions section of `docs/design.md` records what each change was
  measured at.
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
  bump migrates, or refusing a rebuild that would shrink the archive, is unresolved and tracked
  under Open questions in `docs/design.md`.

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
