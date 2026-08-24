# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `recall` now reads more than one coding agent's session store. Codex CLI is registered
  alongside Claude Code: rollouts under `~/.codex/sessions` (or `$CODEX_HOME/sessions`) archive
  to their own `agents/codex/` tree, built separately the first time anything asks for it — an
  existing Claude Code archive is untouched and **requires no rebuild** to pick this up.
  `--provider` (or `RECALL_AGENT`) chooses which agent's transcripts a run reads: `auto` detects
  from the environment, an agent name pins one, `all` reads every registered agent whose session
  store exists. `find`, `turns`, `when` and `show` still read Claude Code only for now and refuse
  an explicit `--provider`/`RECALL_AGENT` naming anything else; `recall doctor` reads whatever
  `--provider` resolves to. See the README for detection order and the rest of the detail.

### Changed

- Searching is several times faster, with no change to what any command returns. Against 1.0.0 on
  the machine the work started on: a conversation-tier `find` 88 → 30 ms, an all-tier `find`
  460 → 93 ms, and a miss over every tier 549 → 113 ms. The corpus is still scanned in full and
  there is still no index; the Decisions section of `docs/design.md` records what each change was
  measured at.
- The archive format is now `recall-turns-3`, which adds per-tier block offsets so a decode can
  run on every core. **The first run after upgrading rebuilds the archive**, as it does for any
  format change. Until it has, `--no-update` refuses with exit 3 and points at `recall doctor`
  rather than reading the old format wrongly; an ordinary run needs nothing from you. Downgrading
  rebuilds in the same way.

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
