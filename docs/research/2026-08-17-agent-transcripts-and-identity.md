---
type: research-report
id: R-20260817-agent-transcripts-and-identity
date: 2026-08-17
scope: project
mode: harness@26a16c2f
question: For each of Claude Code, Codex CLI, Gemini CLI, Cursor, OpenCode, GitHub Copilot CLI and Aider — which env var identifies the agent to a child process, where do session transcripts live, what is their format, and what quirks must a reader handle?
tags: [transcripts, env-var, claude-code, codex, gemini-cli, cursor, opencode, copilot, aider, jsonl]
sha: 55fbee1
---

# Agent transcripts and runtime identity — seven coding agents

> **Provenance**
> - **Report ID:** R-20260817-agent-transcripts-and-identity
> - **Commissioning question:** For each of Claude Code, Codex CLI, Gemini CLI, Cursor, OpenCode, GitHub Copilot CLI and Aider — which env var identifies the agent to a child process, where do session transcripts live, what is their format, and what quirks must a reader handle?
> - **Mode:** harness@26a16c2f
> - **Builds on (does not re-research):** `docs/research/2026-08-17-client-install-surfaces.md`, `docs/research/2026-08-17-mcp-spec-and-go-sdk.md` (MCP spec and installation surfaces are explicitly out of scope here)
> - **Method:** Two evidence streams, deliberately kept separate so they can disagree. (1) **Direct inspection** of five agents installed on this machine — `~/.claude`, `~/.codex`, `~/.gemini`, `~/.cursor`, `~/.local/share/opencode` — read-only: `ls`/`find`, `jq` key-extraction over real transcripts, `sqlite3` in `mode=ro` against copies, and `strings`/`grep` over the shipped binaries and JS bundles for env-var literals. No message bodies were printed; every structural sample below has its string leaves replaced with `…`. No credential file was opened. (2) **Primary-source docs and source code** for all seven, fetched by four parallel sub-agents from `raw.githubusercontent.com`, `api.github.com/…/contents`, `code.claude.com/docs`, `cursor.com/docs`, `docs.github.com`, `aider.chat/docs` and `opencode.ai/docs`. Copilot CLI and Aider are not installed here, so they rest on docs/source only.
> - Append-only. Do not edit claim blocks after they are written; supersede with a new
>   claim, and record status changes as events.

## Bottom line

Four of the seven agents give a child process a reliable, cheap identity signal, and they all use the same shape — a fixed name set to `1` in the environment of every shell command the agent runs: **`CLAUDECODE=1`** (Claude Code), **`GEMINI_CLI=1`** (Gemini CLI), **`CURSOR_AGENT=1`** (Cursor), and for Codex the pair **`CODEX_SESSION_ID` / `CODEX_THREAD_ID`** (a value, not a flag, but always present). **OpenCode, GitHub Copilot CLI and Aider set nothing** — all three were checked at the source level and the absence is positive, not merely unfound. Detection should therefore be a first-match-wins probe over a small ordered list of names, with a documented "unknown agent" fallback rather than a guess.

Transcript storage splits three ways and there is no shared convention worth abstracting over. Claude Code and Codex are the friendly cases: append-only JSONL, one file per session, self-describing. Claude Code keys by **encoded cwd** (`~/.claude/projects/-home-<user>-dev-recall/<uuid>.jsonl` — every non-alphanumeric character becomes `-`), so the project is in the *path*; Codex keys by **date** (`~/.codex/sessions/2026/08/05/rollout-<ISO>-<thread-uuid>.jsonl`), so the project is only in the *first record* and a reader must open every file to know which repo it belongs to. Gemini writes one JSON document per session under `~/.gemini/tmp/<sha256(cwd)>/chats/`. Cursor's CLI writes a content-addressed SQLite blob store at `~/.cursor/chats/<md5(cwd)>/<session-uuid>/store.db`, and OpenCode has migrated to a single global SQLite database at `~/.local/share/opencode/opencode.db` keyed by the **git repository's root-commit SHA** — the only agent here that identifies a project by repo identity rather than by filesystem path.

The single most expensive thing to get wrong is version skew, not format. Claude Code's own docs say the JSONL entry format "is internal … and changes between versions", and this machine's corpus proves it: 14 different `2.1.x` versions in one store, and the widely-cited `{"type":"summary"}` record does not appear even once. Codex is mid-migration from flat JSONL rollouts to a paginated JSONL-plus-SQLite store, and separately zstd-compresses cold rollouts to `.jsonl.zst`. Gemini has replaced its `sha256(cwd)` directory naming with slug IDs and migrates old directories on startup. Any reader must treat the on-disk shape as a moving target: parse defensively, key off a small set of load-bearing fields, and skip unknown record types rather than failing.

## Claims

<!-- dp research add-claim writes ```claim blocks here. Prose explaining a claim goes
     immediately above it. Do not hand-author a fence. -->

## Bottom line — the table

**Identity: what a child process can read**

| Agent | Variable a child can rely on | Value | Set for shell tool | Set for MCP servers | Authority |
|---|---|---|---|---|---|
| Claude Code | `CLAUDECODE` | `1` | yes | yes (stdio) | official docs + direct inspection |
| Gemini CLI | `GEMINI_CLI` | `1` | yes | not established | official docs + source |
| Cursor (`cursor-agent`) | `CURSOR_AGENT` | `1` | yes | not established | source (bundle) + official docs |
| Codex CLI | `CODEX_SESSION_ID`, `CODEX_THREAD_ID` | UUID | yes | not established | source (`core/src/exec_env.rs`) |
| OpenCode | — none — | — | no | no | source (`tool/shell.ts`) |
| Copilot CLI | — none — | — | no | no (only `PATH` inherited) | official docs |
| Aider | — none — | — | no (no `env=` passed) | n/a | source (`aider/run_cmd.py`) |

**Storage: where the transcripts are and how they map to a project**

| Agent | Path | Format | Project key |
|---|---|---|---|
| Claude Code | `~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl` | JSONL | cwd with every non-alphanumeric char → `-`, in the directory name |
| Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-<ISO>-<thread-id>.jsonl` | JSONL (+ `.jsonl.zst` for cold files) | `session_meta.payload.cwd` inside the file — **not** in the path |
| Gemini CLI | `~/.gemini/tmp/<sha256(cwd)>/chats/session-<ISO>-<8hex>.json` | single JSON doc (older); JSONL on current `main` | `sha256(cwd)` hex directory (legacy) → slug ID (current) |
| Cursor CLI | `~/.cursor/chats/<md5(cwd)>/<session-uuid>/store.db` | SQLite, content-addressed blobs | `md5(process.cwd())` hex directory |
| Cursor IDE-side | `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<id>/<id>.jsonl` | JSONL | encoded cwd in path |
| OpenCode | `$XDG_DATA_HOME/opencode/opencode.db` (one global DB) | SQLite (`session`/`message`/`part`) | `project.id` = **git root-commit SHA**; `"global"` outside a repo |
| Copilot CLI | `~/.copilot/session-state/<session-id>/events.jsonl` (+ `session-store.db`) | JSONL + SQLite | not established |
| Aider | `<git-root>/.aider.chat.history.md`, `.aider.input.history`, `.aider.llm.history` | Markdown / plain text | the repo itself — files live in the repo root |

### Claude Code

`CLAUDECODE=1` is documented, not merely observed: *"Set to 1 in subprocesses Claude Code spawns (Bash and PowerShell tools, tmux sessions, hook commands, status line commands, stdio MCP server subprocesses)."* Running `env | grep -i claude` inside this very session confirms it, alongside a family of undocumented siblings — `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_EXECPATH`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`. Only `CLAUDECODE` and `CLAUDE_PROJECT_DIR` are documented; treat the rest as best-effort enrichment that may vanish.

The transcript is JSONL, one object per line, with a strong envelope repeated on nearly every record: `uuid`, `parentUuid` (a linked list through the conversation), `sessionId`, `timestamp` (ISO-8601 with `Z`), `cwd`, `gitBranch`, `version`, `userType`, `entrypoint`. A conversational record carries `type: "user" | "assistant"` plus a nested `message` object in Anthropic Messages API shape.

Redacted assistant tool-call record (`~/.claude/projects/<encoded-cwd>/<uuid>.jsonl`, strings replaced with `…`):

```json
{"parentUuid":"151ed65f-…","isSidechain":false,"message":{"model":"…","id":"…","type":"…","role":"…",
 "content":[{"type":"…","id":"…","name":"…","input":{"command":"…","description":"…"}}],
 "stop_reason":"…","usage":{"input_tokens":2,"cache_read_input_tokens":25488,"output_tokens":497}},
 "requestId":"…","type":"…","uuid":"decab171-…","timestamp":"2026-08-17T17:07:20.382Z",
 "sessionId":"048519dd-…","cwd":"…","version":"…","gitBranch":"…"}
```

The matching tool *result* is a `type: "user"` record whose `message.content[0].type` is `tool_result`, plus a top-level `toolUseResult` object holding the structured result (`{stdout, stderr, interrupted, isImage}` for Bash) and `sourceToolAssistantUUID` pointing back at the call. So a user turn, an assistant turn and a tool call are distinguished by two fields together — `type` and `message.content[*].type` — never by `type` alone.

Beyond `user`/`assistant`, this machine's store contains fifteen further top-level types: `attachment`, `system`, `mode`, `permission-mode`, `last-prompt`, `ai-title`, `file-history-delta`, `file-history-snapshot`, `queue-operation`, `pr-link`, `agent-name`, `relocated`, `worktree-state`. A reader should whitelist what it understands rather than switch exhaustively.

**Quirks.** Sub-agent transcripts moved out of the main file: they now live at `<session-uuid>/subagents/agent-<agentId>.jsonl` next to an `agent-<agentId>.meta.json` (`{agentType, description, toolUseId, parentAgentId, spawnDepth, model}`) — `spawnDepth` means sub-agents can nest. A sibling `<session-uuid>/tool-results/` directory holds large tool outputs externalised to `.txt` files. Compaction shows up as a `user` record with `isCompactSummary: true` (the docs' `system`/`compact_boundary` variant appears in sub-agent transcripts). Retention defaults to 30 days (`cleanupPeriodDays`); `CLAUDE_CONFIG_DIR` relocates the whole store; `/branch` and `--fork-session` copy a transcript into a *new* session ID, leaving the original intact — so the same conversation prefix can exist in several files. The hook docs warn the file "is written asynchronously and may lag the in-memory conversation": a live session's file is always behind, and always open for append.

### Codex CLI

Codex injects `CODEX_SESSION_ID` and `CODEX_THREAD_ID` into shell-tool children — the source comment is explicit that this "[e]xposes the shared root-session identity to model-reachable shell commands". `CODEX_PERMISSION_PROFILE` is also injected but its own doc comment warns children can overwrite it. `CODEX_SANDBOX=seatbelt` is macOS-only (compiled under `cfg(target_os = "macos")`), and `CODEX_SANDBOX_NETWORK_DISABLED=1` appears only when the network sandbox is off — neither is a dependable identity signal. `CODEX_HOME` and `CODEX_API_KEY` are read, never set for children. There is no `CODEX_AGENT=1`-style generic flag.

Version skew matters here: the binary installed on this machine is **0.135.0**, and `strings` finds `CODEX_THREAD_ID` and `CODEX_SANDBOX_NETWORK_DISABLED` but **not** `CODEX_SESSION_ID` or `CODEX_PERMISSION_PROFILE`. Those are newer than the local build. Detect on `CODEX_THREAD_ID` first.

Rollouts are JSONL with a uniform three-key envelope — `{timestamp, type, payload}` — and a `type` discriminator whose current values are `session_meta`, `response_item`, `event_msg`, `turn_context`, `compacted`, `inter_agent_communication`, `inter_agent_communication_metadata`, `world_state`, `security_risk_score`. The first line is always `session_meta` and it is the only place the project lives:

```json
{"timestamp":"2026-08-05T20:37:57.824Z","type":"…","payload":{"id":"019fd3a5-…",
 "timestamp":"2026-08-05T20:37:57.706Z","cwd":"…","originator":"…","cli_version":"…",
 "source":"…","model_provider":"…","git":{"commit_hash":"f44894bb…","branch":"…","repository_url":"…"}}}
```

Turn discrimination is two-level, like Claude Code's: `type: "response_item"` with `payload.type: "message"` and `payload.role` in `user`/`assistant`/`developer`; a tool call is `payload.type: "function_call"` (`{name, arguments, call_id}`) and its result is a separate `function_call_output` (`{call_id, output}`) record. `event_msg` records are the UI-event stream and **duplicate** the conversation — `event_msg`/`user_message` restates the same text as `response_item`/`message`/`user`. A naive reader that ingests both will double-count every user turn. In a 373-line sample the split was 269 `response_item` to 99 `event_msg`.

**Quirks.** `compacted` records carry a `message` summary plus an optional full `replacement_history`, so a compacted rollout still contains the pre-compaction turns. Reasoning records store `encrypted_content` with an empty `summary` — the model's thinking is not readable. Codex is mid-migration to a paginated-JSONL-plus-SQLite thread store (`codex migrate-rollouts`), and a background worker rewrites cold rollouts to `.jsonl.zst`; neither has happened yet on this machine (0 `.zst` files, 337 plain rollouts), but a reader must handle both. Forked/reverted threads get a second id appended to the filename (`rollout-<ts>-<thread>_<rollout>.jsonl`). Sub-agent sessions are their own rollout files tagged `SessionSource::SubAgent` with `agent_nickname`/`agent_role`/`agent_path` in `session_meta`. Two side files sit in `~/.codex`: `history.jsonl` (a global `{session_id, ts, text}` log of every prompt the user has ever typed — a genuinely useful cross-session index) and `session_index.jsonl` (`{id, thread_name, updated_at}`, append-only, last entry wins).

Worth noting for `recall`'s purposes: on this machine 56 of 60 sampled sessions have `source: "mcp"` and `originator: "codex_cli_rs"`. Sessions started via the MCP server are indistinguishable in layout from interactive ones.

### Gemini CLI

`GEMINI_CLI=1` is both sourced and documented — `docs/tools/shell.md` says *"When `run_shell_command` executes a command, it sets the `GEMINI_CLI=1` environment variable in the subprocess's environment."* The shell service also forces `TERM=xterm-256color` and `PAGER`/`GIT_PAGER` to `cat`. `GEMINI_SANDBOX` is read as configuration; the variable a sandboxed child sees is `SANDBOX` (container name, or literally `sandbox-exec`). `CLI_TITLE` and `GEMINI_CLI_VERSION` were searched for and do not exist.

The per-project directory is `sha256(cwd)` in hex — verified locally by brute-forcing this machine's one directory back to `/home/<user>/dev/<project>` (exact `sha256` match, no trailing slash). Inside: `chats/session-<ISO>-<8hex>.json`, `logs.json`, and (when enabled) `checkpoints/` and `shell_history`.

The chat file is a **single JSON document**, not JSONL:

```json
{"sessionId":"a99a5520-…","projectHash":"cfb37b32…","startTime":"2025-12-29T01:15:20.194Z",
 "lastUpdated":"2025-12-29T01:15:27.996Z","messages":[ … ]}
```

and a message is discriminated by `type`, not `role`. `type: "user"` carries only `{id, timestamp, type, content}`; `type: "gemini"` is the assistant and additionally carries `toolCalls`, `thoughts`, `model` and `tokens`. `info`, `error` and `warning` are also message types — the transcript mixes UI notices into the same array as conversation. A tool call is nested inside the assistant message rather than being its own record:

```json
{"id":"31ea7426-…","timestamp":"2025-12-29T01:17:45.116Z","type":"…","content":"…",
 "toolCalls":[{"id":"…","name":"…","args":{"file_path":"…"},
   "result":[{"functionResponse":{"id":"…","name":"…","response":{"output":"…"}}}],
   "status":"…","displayName":"…","description":"…"}],
 "thoughts":[{"subject":"…","description":"…","timestamp":"…"}],
 "model":"…","tokens":{"input":8455,"output":24,"cached":6364,"total":8544}}
```

**Quirks.** Current `main` has moved chat recording to **JSONL** (`session-<ts>-<8hex>.jsonl`, one metadata line then one line per message) and replaced the `sha256(cwd)` directory name with a slug from a `ProjectRegistry`, migrating old hash directories on startup. So a reader in the field will meet both layouts and both formats. The record type gained `kind?: 'main' | 'subagent'`, which is how sub-agent transcripts are told apart. `logs.json` is a separate, flat `{sessionId, messageId, type, message, timestamp}` array. A whole-document JSON file also means a reader cannot tail it — it is rewritten, not appended, in the older format.

### Cursor

The strongest evidence here is from the shipped bundle rather than the docs. `cursor-agent` 2026.03.11 contains, in the terminal-executor path, the literal:

```js
const y = {CURSOR_AGENT:"1"};  …  (0,f.Fn)({env:{CURSOR_AGENT:"1"}, userTerminalHint:…})
```

and Cursor's own terminal docs show `if [[ -n "$CURSOR_AGENT" ]]; then` as the recommended shell-config guard. Caveat worth carrying: Cursor staff acknowledged on the forum a CLI regression where the variable stopped being set, fixed "for the next CLI update" — presence is right but has been flaky. There is deliberately no session-ID variable; an open forum request asks for one, citing `CLAUDE_CODE_SESSION_ID`, `CODEX_THREAD_ID` and `OPENCODE_RUN_ID` as prior art.

Storage is documented nowhere and had to be read out of the bundle. The chat directory is computed as:

```js
createHash("md5").update(process.cwd()).digest("hex")  →  join(cursorHome(), "chats", hash)
```

giving `~/.cursor/chats/<md5(cwd)>/<session-uuid>/store.db`. That SQLite file has exactly two tables:

```sql
CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB);
CREATE TABLE meta  (key TEXT PRIMARY KEY, value TEXT);
```

It is a **content-addressed store**: verified locally that `sha256(data) == id` for 35 of 35 blobs. `meta` holds one row whose value is JSON `{"agentId":"7545d64b-…","latestRootBlobId":"08d9967b…"}` — the head pointer. Following it lands on a protobuf-encoded blob (leading bytes `0a 20`, i.e. field 1, length 32) that is a repeated list of 32-byte blob hashes; each of those is a JSON message in Vercel AI-SDK shape:

```json
{"role":"…","content":[{"type":"…","text":"…"}],"id":"…","providerOptions":{"cursor":{"requestId":"…"}}}
{"role":"…","content":[{"type":"…","toolCallId":"…","toolName":"…","result":"…",
  "experimental_content":[{"type":"…","text":"…"}]}],"id":"…",
  "providerOptions":{"cursor":{"highLevelToolCallResult":{"output":{"success":{"command":"…","stdout":"…","executionTime":3771}}}}}}
```

Roles observed: `system`, `user`, `assistant`, `tool`. So a tool result is its own `role: "tool"` message, not an attachment to the user turn.

Separately, `~/.cursor/projects/<encoded-cwd>/agent-transcripts/<session-id>/<session-id>.jsonl` holds a much flatter IDE-side JSONL — every line is exactly `{"role": …, "message": {"content": [{"type":"text","text": …}]}}`, no timestamps, no ids, no tool records (21 such files on this machine; the one sampled was 52 assistant lines to 4 user lines). Two different Cursor surfaces, two incompatible transcript formats. The same `projects/<encoded-cwd>/` directory also holds `repo.json` (`{"id": "<uuid>"}`), `terminals/`, `mcps/` and a `worker.log`.

**Quirks.** Reading Cursor's CLI store means implementing content-addressing plus a protobuf list decode — there is no linear log to scan. The blobs are immutable and shared, so a fork costs one new root. Nothing here is documented by Cursor; all of it is subject to silent change.

### OpenCode

**No identity variable.** The shell tool's environment is built as `{ ...process.env, ...extra.env }` — process env plus whatever a plugin's `shell.env` hook injects, and nothing else. Confirmed against the locally installed binary (v1.3.13): the full set of `OPENCODE_*` literals it contains is 60-odd config flags (`OPENCODE_CONFIG`, `OPENCODE_DB`, `OPENCODE_CLIENT`, `OPENCODE_PERMISSION`, …) with no bare `OPENCODE` and no `OPENCODE_RUN_ID`. `OPENCODE_CLIENT` is read (defaulting to `"cli"`), never set. A Cursor forum post cites `OPENCODE_RUN_ID` as existing prior art, but it is absent from 1.3.13 — if it exists it is newer, and it should not be relied on yet.

Storage has moved. The current backend is one global SQLite database, `$XDG_DATA_HOME/opencode/opencode.db` (`~/.local/share/opencode/opencode.db`), with `OPENCODE_DB` overriding it and non-default install channels getting `opencode-<channel>.db`. Locally that DB holds 3 sessions / 77 messages / 274 parts, last written May 2026; the legacy `storage/` JSON tree (`session/<projectID>/<sessionID>.json`, `message/<msgID>/…`, `part/<msgID>/prt_*.json`) is still on disk but stopped being written in January 2026 and carries a `storage/migration` marker containing `2`.

Schema is a thin relational skeleton over JSON blobs:

```sql
CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT,
  directory TEXT NOT NULL, title TEXT, version TEXT, share_url TEXT, revert TEXT, permission TEXT,
  time_created INTEGER, time_updated INTEGER, time_compacting INTEGER, time_archived INTEGER,
  workspace_id TEXT, path TEXT, agent TEXT, model TEXT);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT);
CREATE TABLE part    (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, data TEXT);
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, vcs TEXT, sandboxes TEXT, …);
```

`message.data` is JSON discriminated by `role`; only `assistant` carries `modelID`/`providerID`/`path.cwd`/`tokens`/`cost`:

```json
{"role":"…","time":{"created":1768615362091,"completed":1768615366956},"parentID":"…",
 "modelID":"…","providerID":"…","mode":"…","agent":"…","path":{"cwd":"…","root":"…"},
 "cost":0,"tokens":{"input":101,"output":229,"reasoning":192,"cache":{"read":27520,"write":0}},"finish":"…"}
```

`part.data` is discriminated by `type`, observed locally as `text`, `reasoning`, `tool`, `patch`, `step-start`, `step-finish`. A tool call and its result are one record — `{type:"tool", callID, tool, state:{status, input, output, metadata, time}}` — so `state.status` (`pending`/`running`/`completed`/`error`) is what tells an in-flight call from a finished one. Timestamps are Unix **milliseconds**, unlike every other agent here.

The best finding for a cross-agent reader: `project.id` is the **git repository's root commit SHA**. Verified locally — `git rev-list --max-parents=0 HEAD` in `/home/<user>/dev/<project>` returns exactly the 40-hex `project.id` in the database. Non-repo directories fall under a project literally named `global` with `worktree` `/`. That means OpenCode sessions survive a repo being moved or renamed, and that two clones of the same repo share a project — behaviour no path-keyed agent has.

**Quirks.** Compaction is recorded as a user-message part of `type: "compaction"` (with `auto`/`overflow` flags) and an assistant message flagged `summary: true` with `mode`/`agent` both `"compaction"`. Reverts store `{messageID, partID, snapshot, diff}`. Sub-sessions link by `session.parent_id`. And the repository itself moved: `github.com/sst/opencode` → `github.com/anomalyco/opencode` (unrelated to `github.com/opencode-ai/opencode`).

### GitHub Copilot CLI

**No identity variable.** Every documented `COPILOT_*` name (`COPILOT_HOME`, `COPILOT_MODEL`, `COPILOT_ALLOW_ALL`, `COPILOT_GITHUB_TOKEN`) is read as configuration. The MCP docs make the absence explicit from the other direction: *"The `PATH` variable is automatically inherited from your environment. All other environment variables must be configured here."* — nothing is auto-injected.

Sessions live under `~/.copilot/session-state/<session-id>/`, each holding an `events.jsonl` plus workspace artifacts (plans, checkpoints, tracked files); `~/.copilot` also holds a `session-store.db` SQLite database for cross-session data and per-process text logs `logs/process-{timestamp}-{pid}.log`. `COPILOT_HOME` relocates the lot (without migrating existing data). Not installed here, so the record shape inside `events.jsonl` is undetermined — the field-level schema is the biggest gap in this report.

**Quirks.** Sessions **sync to the user's GitHub account by default**, and deleting local files does not delete the synced copy — the only agent here with a server-side mirror, and a privacy consideration for any tool that indexes them. Auto-compaction fires near the token limit (`/compact` triggers it manually), so a stored transcript may be summarised rather than complete. Sub-agents run in an isolated context and stream `subagent.started`/`subagent.completed` lifecycle events back into the parent's event stream rather than getting their own file.

### Aider

**No identity variable, and no environment manipulation at all.** `aider/run_cmd.py` calls `subprocess.Popen(..., shell=True)` with no `env=` argument, so children inherit the parent environment verbatim. Every `AIDER_*` name in the docs (`AIDER_CHAT_HISTORY_FILE`, `AIDER_RESTORE_CHAT_HISTORY`, `AIDER_SET_ENV`, …) is read at startup. `--set-env` lets a *user* push arbitrary pairs into aider's own environment, which children then inherit — but that is user configuration, not an agent signal.

Aider is the outlier on storage too: history is **per-repo and human-readable**, written to the git root (falling back to cwd outside a repo) as `.aider.chat.history.md`, `.aider.input.history` and optionally `.aider.llm.history`. There is no session ID, no JSON, and no per-session file — one appended markdown log per repository. Turns are distinguished by markdown convention: `# aider chat started at {timestamp}` opens a session, `#### ` prefixes a user prompt, `> ` blockquotes tool output and errors, and assistant text carries no prefix at all. `--llm-history-file` writes plain text with an uppercase role name plus an ISO timestamp on its own line, then the content.

**Quirks.** On first run in a repo Aider offers to add `.aider*` to `.gitignore`, so these files are usually untracked — do not expect them in a clone. When Aider is driven as a Python library (`Coder.create()` / `coder.run()`) rather than interactively, it writes **no** history files at all, per an open issue. And because everything is one appended log per repo, sessions can only be separated by the `# aider chat started at` markers.

## Deltas

**Detection must be an ordered probe, not a switch, and it must have an honest unknown.** Three of the seven agents provide nothing. If `recall` is going to report "which agent am I running under", the answer for OpenCode, Copilot CLI and Aider has to be "cannot tell from the environment" — the fallback would be process-ancestry inspection, which is a different and much less portable mechanism. Suggested order, cheapest and most certain first: `CLAUDECODE`, `GEMINI_CLI`, `CURSOR_AGENT`, `CODEX_THREAD_ID` (then `CODEX_SESSION_ID`), else unknown.

**"Find the sessions for this repo" is four different algorithms, not one.** Claude Code and Cursor encode the cwd into a directory name (differently: dash-substitution vs `md5`); Gemini hashes it with `sha256` (and is migrating away from that); OpenCode uses the git root-commit SHA; Codex puts it nowhere in the path at all. Codex is the expensive case — answering "sessions for `/home/<user>/dev/recall`" means opening the first line of all 337 rollout files. That argues for `recall` maintaining its own index keyed by `(agent, project, session)` rather than resolving paths on each query.

**A dual-format reader is not optional for three agents.** Codex (JSONL → paginated JSONL + SQLite, plus `.jsonl.zst`), Gemini (single-JSON → JSONL, `sha256` dir → slug dir), and OpenCode (`storage/` JSON tree → `opencode.db`) are all mid-migration *right now*. Two of the three still have the legacy artefacts sitting on this machine alongside the new ones. Whatever `recall` does, it needs to read both and prefer the newer.

**Do not model a "message" as one record.** Claude Code, Codex and Cursor all split a tool call from its result into two records linked by an id (`sourceToolAssistantUUID`, `call_id`, `toolCallId`); Gemini and OpenCode keep them together. Any common internal model has to accommodate both, which probably means normalising toward the split form on ingest.

**Codex's `event_msg` stream is a double-count trap.** It restates user messages and assistant messages that already appear as `response_item` records. A reader that ingests both will show every user turn twice. Filter to `response_item` for conversation content, and treat `event_msg` as telemetry.

## Gems

**Cursor's chat store is a content-addressed Merkle-ish DAG, and it is verifiable.** `sha256(blob.data) == blob.id` held for 35/35 blobs sampled. `meta` holds a single JSON head pointer `{agentId, latestRootBlobId}`; the root is protobuf (`0a 20` = field 1, 32-byte length) listing child blob hashes. Whoever built this got deduplication and cheap forking for free — a fork is one new root blob. It is also, incidentally, a nice trick for any tool that wants to store many near-identical conversation variants.

**OpenCode keying projects by the git root-commit SHA is the best idea in this whole survey.** Path-keyed stores (five of the seven agents) break the moment a repo is moved, renamed, or cloned to a second checkout — and worktrees fragment into separate "projects". The root commit is stable across all of that, and it makes two clones of one repo the same project. Verified concretely: `git rev-list --max-parents=0 HEAD` in `/home/<user>/dev/<project>` → `6db9e2e234dd168ce2413c61fbc049f36884525f`, exactly the `project.id` in `opencode.db`.

**Codex ships a free cross-session prompt index.** `~/.codex/history.jsonl` is one `{session_id, ts, text}` line per prompt the user has ever submitted — 214 KB here covering the full history. For a search tool, that is a pre-built inverted-index input that avoids opening 337 rollouts, and the `session_id` links straight back to the full transcript.

**Version skew is measurable, not theoretical.** This machine's Claude Code corpus spans fourteen distinct `2.1.x` versions in a single store directory, and the `{"type":"summary","summary":…,"leafUuid":…}` record cited in essentially every third-party parser does not appear once in it. Anthropic's own wording is the honest framing: *"The entry format is internal to Claude Code and changes between versions, so scripts that parse these files directly can break on any release."* Any parser needs a "skip unknown record type" default and a version field in its own index.

**The four agents that do signal identity converged on the same design without coordinating**: a fixed uppercase name set to the string `1`, injected into the shell tool's environment. That is the de-facto convention. If `recall` ever wants to be detectable by *its* children, it should follow it.

## Caveats and data quality

**Two agents rest entirely on documentation.** Copilot CLI and Aider are not installed here. For Copilot the record-level schema of `events.jsonl` is completely undetermined — the docs name the file and nothing more, and a community DeepWiki page contradicted the official docs on the directory name (`sessions/` vs `session-state/`); the official page was taken as authoritative and the community claim discarded. This is the thinnest area of the report and the first thing a second pass should attack, ideally by installing the CLI and running one session.

**Cursor's storage is entirely undocumented.** No official Cursor page describes where `cursor-agent` puts transcripts. Everything in that section comes from direct inspection of this machine plus `strings`/`grep` over the shipped JS bundle. It is solid evidence for *this* version (2026.03.11-6dfa30c) and no guarantee at all about the next one. The `agent-transcripts/*.jsonl` format in particular was sampled from exactly one file.

**Local builds lag upstream `main`, in both directions.** Codex 0.135.0 here does not contain `CODEX_SESSION_ID` or `CODEX_PERMISSION_PROFILE`, which the current source has; Gemini CLI 0.22.4's on-disk chats are the old single-JSON format while `main` writes JSONL; OpenCode 1.3.13 has no `OPENCODE_RUN_ID`. Where local inspection and upstream source disagree, both are reported and the disagreement is the finding. Nothing in the "current source" column has been observed running.

**Documentation quotes were fetched through a summarising layer.** All four sub-agents used WebFetch, which renders pages through a small model before returning text. Quotes were requested verbatim and cross-checked for internal consistency (variable names matched across separately-fetched files), but they are not guaranteed byte-identical to the source. Quotes marked `direct-inspection` do not have this problem — they are `jq`/`sqlite3`/`strings` output from this machine.

**Named HTTP failures.** `api.github.com/search/code` returned 401 for unauthenticated code search in two separate sub-agents, so no whole-repo server-side grep was possible for Codex or OpenCode; `grep.app` returned 429 twice as a substitute. `docs.cursor.com/en/cli/overview` 308-redirects to the generic docs homepage — the working paths are `cursor.com/docs/cli/*`. All `docs.claude.com/en/docs/claude-code/*` URLs 301 to `code.claude.com/docs/en/*`. `docs.github.com/en/copilot/reference/copilot-cli-configuration-reference` and `.../extend-copilot-cli-with-mcp` are both 404 (correct paths: `cli-config-dir-reference`, `add-mcp-servers`). `learn.chatgpt.com/docs/codex/sandboxing` is 404 (correct: `learn.chatgpt.com/docs/sandboxing`). `raw.githubusercontent.com/anomalyco/opencode/dev/packages/opencode/src/tool/bash.ts` is 404 — the file is `tool/shell.ts`.

**Established absences, with the search that established them.** `CLI_TITLE` and `GEMINI_CLI_VERSION` do not exist in gemini-cli (searched `shellExecutionService.ts`, `shell-utils.ts`, `docs/tools/shell.md`). `experimental_resume` no longer exists anywhere in codex-rs (zero grep matches; current mechanism is the `codex resume` subcommand). No `CODEX_AGENT=1`-style generic flag exists in codex-rs. `isCompactSummary` is not in Claude Code's official docs — it is observed in real transcripts here, while the docs describe a `system`/`compact_boundary` record instead; both exist, in different files. No `{"type":"summary"}` record exists anywhere in this machine's 15-project Claude Code corpus.

**Not attempted.** No agent was executed to observe its child environment empirically — every env-var claim is from source, docs, or `strings` over a binary, except `CLAUDECODE` which was read from this process's own environment. Running one command under each installed agent and capturing `env` would upgrade four claims from `official-vendor` to `measured-independent` and is the single highest-value follow-up. Windows and macOS paths are reported only where documented; nothing was verified off Linux. The OpenCode `@opencode-ai/schema` package (the actual field-level `Part`/`ToolState` struct bodies) was unreachable — part shapes above come from real database rows here rather than the schema definition.

```claim
spec: 1
id: C-20260817-claude-code-sets-claudecode-1
promoted_to: vault:research/recall/research/C-20260817-claude-code-sets-claudecode-1.md
```

```claim
spec: 1
id: C-20260817-claude-code-child-env-names-observed
promoted_to: vault:research/recall/research/C-20260817-claude-code-child-env-names-observed.md
```

```claim
spec: 1
id: C-20260817-claude-transcript-path-encoded-cwd
promoted_to: vault:research/recall/research/C-20260817-claude-transcript-path-encoded-cwd.md
```

```claim
spec: 1
id: C-20260817-claude-jsonl-record-envelope
promoted_to: vault:research/recall/research/C-20260817-claude-jsonl-record-envelope.md
```

```claim
spec: 1
id: C-20260817-claude-tool-result-is-user-record
promoted_to: vault:research/recall/research/C-20260817-claude-tool-result-is-user-record.md
```

```claim
spec: 1
id: C-20260817-claude-many-nonchat-record-types
promoted_to: vault:research/recall/research/C-20260817-claude-many-nonchat-record-types.md
```

```claim
spec: 1
id: C-20260817-claude-subagent-separate-files
promoted_to: vault:research/recall/research/C-20260817-claude-subagent-separate-files.md
```

```claim
spec: 1
id: C-20260817-claude-tool-results-sidecar-dir
promoted_to: vault:research/recall/research/C-20260817-claude-tool-results-sidecar-dir.md
```

```claim
spec: 1
id: C-20260817-claude-format-unstable-by-design
promoted_to: vault:research/recall/research/C-20260817-claude-format-unstable-by-design.md
```

```claim
spec: 1
id: C-20260817-claude-no-summary-record-in-2-1
promoted_to: vault:research/recall/research/C-20260817-claude-no-summary-record-in-2-1.md
```

```claim
spec: 1
id: C-20260817-claude-transcript-lags-live-session
promoted_to: vault:research/recall/research/C-20260817-claude-transcript-lags-live-session.md
```

```claim
spec: 1
id: C-20260817-codex-injects-session-and-thread-id
promoted_to: vault:research/recall/research/C-20260817-codex-injects-session-and-thread-id.md
```

```claim
spec: 1
id: C-20260817-codex-local-binary-lacks-session-id
promoted_to: vault:research/recall/research/C-20260817-codex-local-binary-lacks-session-id.md
```

```claim
spec: 1
id: C-20260817-codex-sandbox-var-macos-only
promoted_to: vault:research/recall/research/C-20260817-codex-sandbox-var-macos-only.md
```

```claim
spec: 1
id: C-20260817-codex-network-disabled-var-conditional
promoted_to: vault:research/recall/research/C-20260817-codex-network-disabled-var-conditional.md
```

```claim
spec: 1
id: C-20260817-codex-sessions-dated-layout
promoted_to: vault:research/recall/research/C-20260817-codex-sessions-dated-layout.md
```

```claim
spec: 1
id: C-20260817-codex-rollout-envelope-and-types
promoted_to: vault:research/recall/research/C-20260817-codex-rollout-envelope-and-types.md
```

```claim
spec: 1
id: C-20260817-codex-session-meta-holds-cwd-and-git
promoted_to: vault:research/recall/research/C-20260817-codex-session-meta-holds-cwd-and-git.md
```

```claim
spec: 1
id: C-20260817-codex-turn-discrimination-two-level
promoted_to: vault:research/recall/research/C-20260817-codex-turn-discrimination-two-level.md
```

```claim
spec: 1
id: C-20260817-codex-event-msg-duplicates-turns
promoted_to: vault:research/recall/research/C-20260817-codex-event-msg-duplicates-turns.md
```

```claim
spec: 1
id: C-20260817-codex-compacted-keeps-replacement-history
promoted_to: vault:research/recall/research/C-20260817-codex-compacted-keeps-replacement-history.md
```

```claim
spec: 1
id: C-20260817-codex-zstd-and-sqlite-migration
promoted_to: vault:research/recall/research/C-20260817-codex-zstd-and-sqlite-migration.md
```

```claim
spec: 1
id: C-20260817-codex-global-history-jsonl-index
promoted_to: vault:research/recall/research/C-20260817-codex-global-history-jsonl-index.md
```

```claim
spec: 1
id: C-20260817-codex-session-index-is-names-only
promoted_to: vault:research/recall/research/C-20260817-codex-session-index-is-names-only.md
```

```claim
spec: 1
id: C-20260817-gemini-cli-sets-gemini-cli-1
promoted_to: vault:research/recall/research/C-20260817-gemini-cli-sets-gemini-cli-1.md
```

```claim
spec: 1
id: C-20260817-gemini-project-dir-is-sha256-cwd
promoted_to: vault:research/recall/research/C-20260817-gemini-project-dir-is-sha256-cwd.md
```

```claim
spec: 1
id: C-20260817-gemini-chat-file-single-json-doc
promoted_to: vault:research/recall/research/C-20260817-gemini-chat-file-single-json-doc.md
```

```claim
spec: 1
id: C-20260817-gemini-message-type-not-role
promoted_to: vault:research/recall/research/C-20260817-gemini-message-type-not-role.md
```

```claim
spec: 1
id: C-20260817-gemini-moving-to-jsonl-and-slugs
promoted_to: vault:research/recall/research/C-20260817-gemini-moving-to-jsonl-and-slugs.md
```

```claim
spec: 1
id: C-20260817-gemini-subagent-kind-field
promoted_to: vault:research/recall/research/C-20260817-gemini-subagent-kind-field.md
```

```claim
spec: 1
id: C-20260817-cursor-agent-sets-cursor-agent-1
promoted_to: vault:research/recall/research/C-20260817-cursor-agent-sets-cursor-agent-1.md
```

```claim
spec: 1
id: C-20260817-cursor-agent-var-has-regressed
promoted_to: vault:research/recall/research/C-20260817-cursor-agent-var-has-regressed.md
```

```claim
spec: 1
id: C-20260817-cursor-chats-dir-is-md5-cwd
promoted_to: vault:research/recall/research/C-20260817-cursor-chats-dir-is-md5-cwd.md
```

```claim
spec: 1
id: C-20260817-cursor-store-is-content-addressed
promoted_to: vault:research/recall/research/C-20260817-cursor-store-is-content-addressed.md
```

```claim
spec: 1
id: C-20260817-cursor-meta-holds-root-pointer
promoted_to: vault:research/recall/research/C-20260817-cursor-meta-holds-root-pointer.md
```

```claim
spec: 1
id: C-20260817-cursor-message-blobs-ai-sdk-shape
promoted_to: vault:research/recall/research/C-20260817-cursor-message-blobs-ai-sdk-shape.md
```

```claim
spec: 1
id: C-20260817-cursor-second-jsonl-transcript-surface
promoted_to: vault:research/recall/research/C-20260817-cursor-second-jsonl-transcript-surface.md
```

```claim
spec: 1
id: C-20260817-cursor-storage-undocumented
promoted_to: vault:research/recall/research/C-20260817-cursor-storage-undocumented.md
```

```claim
spec: 1
id: C-20260817-opencode-shell-tool-sets-nothing
promoted_to: vault:research/recall/research/C-20260817-opencode-shell-tool-sets-nothing.md
```

```claim
spec: 1
id: C-20260817-opencode-no-run-id-in-1-3-13
promoted_to: vault:research/recall/research/C-20260817-opencode-no-run-id-in-1-3-13.md
```

```claim
spec: 1
id: C-20260817-opencode-sqlite-is-current-store
promoted_to: vault:research/recall/research/C-20260817-opencode-sqlite-is-current-store.md
```

```claim
spec: 1
id: C-20260817-opencode-project-id-is-root-commit
promoted_to: vault:research/recall/research/C-20260817-opencode-project-id-is-root-commit.md
```

```claim
spec: 1
id: C-20260817-opencode-legacy-json-tree-abandoned
promoted_to: vault:research/recall/research/C-20260817-opencode-legacy-json-tree-abandoned.md
```

```claim
spec: 1
id: C-20260817-opencode-message-and-part-shapes
promoted_to: vault:research/recall/research/C-20260817-opencode-message-and-part-shapes.md
```

```claim
spec: 1
id: C-20260817-opencode-compaction-and-revert-records
promoted_to: vault:research/recall/research/C-20260817-opencode-compaction-and-revert-records.md
```

```claim
spec: 1
id: C-20260817-opencode-repo-moved-to-anomalyco
promoted_to: vault:research/recall/research/C-20260817-opencode-repo-moved-to-anomalyco.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-no-child-env-var
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-no-child-env-var.md
```

```claim
spec: 1
id: C-20260817-copilot-session-state-jsonl
promoted_to: vault:research/recall/research/C-20260817-copilot-session-state-jsonl.md
```

```claim
spec: 1
id: C-20260817-copilot-sessions-sync-to-github
promoted_to: vault:research/recall/research/C-20260817-copilot-sessions-sync-to-github.md
```

```claim
spec: 1
id: C-20260817-aider-run-cmd-passes-no-env
promoted_to: vault:research/recall/research/C-20260817-aider-run-cmd-passes-no-env.md
```

```claim
spec: 1
id: C-20260817-aider-vars-are-read-only-config
promoted_to: vault:research/recall/research/C-20260817-aider-vars-are-read-only-config.md
```

```claim
spec: 1
id: C-20260817-aider-history-is-markdown-per-repo
promoted_to: vault:research/recall/research/C-20260817-aider-history-is-markdown-per-repo.md
```

```claim
spec: 1
id: C-20260817-aider-scripted-mode-writes-no-history
promoted_to: vault:research/recall/research/C-20260817-aider-scripted-mode-writes-no-history.md
```

```claim
spec: 1
id: C-20260817-no-generic-agent-running-flag
promoted_to: vault:research/recall/research/C-20260817-no-generic-agent-running-flag.md
```
