# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

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
