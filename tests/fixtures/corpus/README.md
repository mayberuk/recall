# Shared synthetic corpus

Owned by the scaffold portion. Every portion tests against this corpus and none invents a
private fixture for shared behaviour. Each file exists because the pathology it carries was
**measured in the real corpus**, and the finding is named below so nobody simplifies one away.
Removing or weakening a fixture removes the evidence for a decision.

Use it through `internal/fixtures.Materialize(t)`. Do not read this directory directly: the
`{{SCRATCH}}` token inside the JSONL is only substituted at materialization time, and three
fixtures need real git state on disk that only `Materialize` builds.

## Layout mirrors the real store

```
projects/<project-dir>/<sessionId>.jsonl
projects/<project-dir>/<sessionId>/subagents/agent-<id>.jsonl
```

Real project directory names are derived from `cwd` with `/` replaced by `-`. The scratch root
is a temp directory that does not exist until a test runs, so these directory names are fixed
and readable instead. The `cwd` **inside** each record is the substituted absolute path, and that
is what repo identity resolves from.

## What each row of the contract table maps to

`docs/orchestration.md` §Shared fixtures names files by pathology; the store layout names them
by session id. This is the mapping.

| Contract row | File | Measured pathology it pins |
|---|---|---|
| `multi-session.jsonl` | `-scratch-normal/c58d0e12-….jsonl` | one file carrying two `sessionId`s; 6 real files do |
| `dup-uuid-{a,b}.jsonl` | `-scratch-normal/7a41e5b8-….jsonl` and `-scratch-normal-android/7a41e5b8-….jsonl` | the same record uuid in two files; 9,473 uuids, 17,659 redundant copies |
| `no-promptsource.jsonl` | `-scratch-normal/6d19c7f4-….jsonl` | user records lacking `promptSource`; 33–68% of human-shaped records across all 24 versions, and none of them is human-typed prose |
| `unknown-type.jsonl` | `-scratch-normal/2a6e4b83-….jsonl` | record types this build has never seen; 24 versions, format documented as internal and changing |
| `empty-thinking.jsonl` | `-scratch-normal/5f30b8d6-….jsonl` | `thinking` with empty text and a long signature; 94.5% of blocks, 125.4 MB of signature against 2.09 MB of reasoning |
| `huge-result.jsonl` | `-scratch-normal/0c94e716-….jsonl` | a tool result of 22,053 bytes; 92.6% of tool-result bytes sit in results over 2 KB |
| `cwd-orphan-worktree.jsonl` | `-scratch-normal--claude-worktrees-orphan/9d6f2a13-….jsonl` | cwd under a worktree whose gitdir was pruned; `git remote get-url origin` exits 128 there |
| `cwd-remoteless.jsonl` | `-scratch-remoteless/b47c8e91-….jsonl` | a real repo with no origin; 2 of 144 measured cwd values |
| `cwd-subdir.jsonl` | `-scratch-normal-android/1e83f5c7-….jsonl` | cwd in a subdirectory of the repo (`normal/android`) |
| `relocated.jsonl` | `-scratch-gone/4c72d908-….jsonl` | a `relocated` record carrying `relocatedCwd` and no `cwd`; 14 measured paths are no longer on disk |
| `subagent/agent-x.jsonl` | `-scratch-normal/3f9c1d20-…/subagents/agent-b1c2d3e4f50617289.jsonl` | `isSidechain: true` under a session's `subagents/` dir; 947 of 1,077 real files |
| `mtime-skew/` | `-scratch-mtime-skew/8b05a3e7-….jsonl` | content dated 55 days before the file's mtime; the measured maximum coverage-boundary divergence |
| `needle.jsonl` | `-scratch-normal/3f9c1d20-….jsonl` | planted rare tokens for the no-false-negative property test |

## Planted tokens

Each appears **exactly once** in the corpus, so a hit count is an assertion rather than an
approximation. Constants live in `internal/fixtures`.

| Token | Tier | Session | Why it is there |
|---|---|---|---|
| `quixotrope` | conversation | needle | the default-tier needle |
| `flimberdash` | conversation | needle | thinking *text*, the 2.09 MB that is kept |
| `grimbleflax` | invocation | needle | a Bash command line, shown only with `--tools` |
| `wobbleknuth` | result | huge-result | present only in tool output: not returned by default, returned with `--results`, and the default run must say so |
| `snorplewick` | conversation | needle (subagent) | a conclusion reached inside a subagent, folded into the parent session |
| `thrumbaloo` | conversation | remoteless | the only hit outside the default repo scope; the zero-result probe must find it |
| `plimwarden` | conversation | dup | **one record uuid carried by two files**: a scan without uuid dedup reports two hits where the contract says one |
| `vorplextin` | conversation | needle | shares a record with `hobznatchet` below: a `text` block and a `tool_use` block in one `message.content` array |
| `hobznatchet` | invocation | needle | the same record as `vorplextin`, and a decoder that keeps only a record's first content block loses one of the two |

`hobznatchet`'s record is the only one in the corpus carrying both a conversation-tier and an
invocation-tier turn; every other record carries one or the other.

## Codex corpus

`internal/fixtures.MaterializeCodex(t)` builds a separate, synthetic Codex CLI store in process
rather than from files under this directory; see `internal/fixtures/codex.go`. Its plain-rollout
row carries `quenlaphor` in the assistant message, the only planted token in either corpus: a hit
on it proves a search reached the Codex store and not claude-code's.

## Counts the manifest asserts

Derived from the fixture content, never from running recall.

- 14 files, 13 distinct session ids, 13 top-level session files, 1 subagent transcript.
  Any count of "sessions" that counts files is wrong in both directions here: one file carries
  two sessions, and one session is carried by two files.
- 14 records carry `promptSource: "typed"` on 13 distinct uuids. Plus one slash-command record
  whose `<command-args>` carry typed words (args only, wrapper discarded) and one whose
  `<command-args>` are empty and must not count. **Human turns: 14.**
- 3 records of 2 unrecognised types. They must not crash the parser, must not be silently
  dropped, and must surface in `recall doctor`.

## Git scratch shapes

`Materialize` builds these with real `git`, because a faked `.git` does not reproduce the
failure the orphan case exists for. It skips the test when git is unavailable rather than
passing without having checked.

| Shape | Expected identity |
|---|---|
| `normal` | the origin remote |
| `normal/android` | the same origin remote, walked up from a subdirectory |
| `normal/.claude/worktrees/orphan` | the same origin remote, and the walk must **continue past** the exit-128 failure, not stop at it |
| `remoteless` | `repo, no remote`, keyed by toplevel path; never "unresolved" |
| `gone` | never created; outside any repo |
