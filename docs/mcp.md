# Reaching recall from a coding agent

`recall` answers the same four questions over the Model Context Protocol that it answers at a
shell. This is the client-by-client detail; [the README](../README.md#let-your-agent-ask) has the
one-command version.

`recall mcp serve` speaks the Model Context Protocol over stdio, revision `2026-07-28`, and
exposes five read-only tools: `recall_find`, `recall_guide`, `recall_show`, `recall_turns` and
`recall_when`. Each one runs the CLI verb it is named after and hands back the same view
`--format json` prints, coverage footer included, so a tool call and a command line cannot
disagree about what the corpus holds or about what was left out of it. `recall doctor` is
deliberately not a tool: it answers a question about archive integrity rather than about past
sessions, and every listed tool spends a session's roster budget whether it is called or not.

You never run `recall mcp serve` yourself; a client spawns it. Getting recall into a client's
config is two commands, in this order. Paths in the two blocks below come from a real run with
the home directory rewritten to `/home/you`, and the binary was at `~/.local/bin/recall`.

### `recall mcp config <client>`, which writes nothing

It prints the file the entry belongs in, a second place that client also reads one from, the
client's own registration command where there is one, the entry itself, and a line saying
nothing was written:

```
$ recall mcp config claude-code
claude-code — Claude Code
  file   /home/you/.claude.json
  also   or .mcp.json at a repo root, which shares the entry with everyone who checks that repo out
  add    claude mcp add recall -- /home/you/.local/bin/recall mcp serve

{
  "mcpServers": {
    "recall": {
      "command": "/home/you/.local/bin/recall",
      "args": [
        "mcp",
        "serve"
      ],
      "env": {}
    }
  }
}

nothing was written. `recall mcp install claude-code` is the opt-in that puts it in place.
```

The snippet is the whole file a client would hold if recall were its only server, so it pastes
into an empty config or reads off as the one key to merge by hand. The path is absolute because
a client spawns the server from its own working directory, and a relative command there
resolves against whichever directory that happens to be.

### `recall mcp install <client>`, the opt-in

This one writes, and only when asked. Where the client ships its own registration command it
runs that rather than touching the file, because the vendor owns its config format and a merge
recall writes can be wrong in ways only that vendor's parser would notice. Where there is none
(Cursor, and OpenCode, whose `mcp add` is an interactive wizard with no documented flag syntax)
recall merges the single `recall` key in itself and carries every other key across as the
bytes it arrived as. **It copies any file it merges into to `<path>.recall-backup` first**; a
file that does not exist yet gets no backup, because an empty one could later be restored over
a good config. A `recall` entry that is already there and differs stops the install and names
`--force`; one that matches is a skip rather than a rewrite. A vendor command that is not on
`PATH` stops the install too, and prints the entry so the next move is on the screen rather than
in these docs. `--dry-run` prints every path the run would create or modify and writes none of
them:

```
$ recall mcp install claude-code --dry-run
recall mcp install claude-code --dry-run — Claude Code, nothing below was written
  run    claude mcp add recall -- /home/you/.local/bin/recall mcp serve
  mkdir  /home/you/.claude/skills/recall
  write  /home/you/.claude/skills/recall/SKILL.md  (recall's own instruction file)
```

An install also puts recall's `SKILL.md` where that client reads skills from. That half is what
gets the tools reached for rather than merely listed: the skill's description fires on the shape
of the question (*what did we decide about this*, *have we hit this before*) rather than on
recall's name, which is not a word anyone thinks of mid-task.

### The clients

| `<client>` | config file | what `install` does | instruction file |
|---|---|---|---|
| `claude-code` | `~/.claude.json`, or `.mcp.json` at a repo root, which is shared with everyone who checks the repo out | runs `claude mcp add` | `~/.claude/skills/recall/SKILL.md` |
| `codex` | `~/.codex/config.toml` (`$CODEX_HOME/config.toml` if set); `codex mcp add` always writes the global file, so a project's own `.codex/config.toml` is a hand edit | runs `codex mcp add` | `~/.agents/skills/recall/SKILL.md` |
| `gemini` | `~/.gemini/settings.json` | runs `gemini mcp add` | `~/.gemini/skills/recall/SKILL.md` |
| `copilot` | `~/.copilot/mcp-config.json` | runs `copilot mcp add` | none, because it has custom agents rather than skills, in a shape recall ships no asset for, and it reads `AGENTS.md` and `CLAUDE.md` from the repo anyway |
| `cursor` | `~/.cursor/mcp.json` | merges recall's key, after a backup | printed, not written: `.cursor/rules/recall.mdc` in the workspace |
| `opencode` | `~/.config/opencode/opencode.json` (`$XDG_CONFIG_HOME/opencode/opencode.json` if set) | merges recall's key, after a backup | `~/.agents/skills/recall/SKILL.md`, and OpenCode reads that directory and `~/.claude/skills` natively, so it needs no copy of its own |
| `windsurf` | `~/.codeium/mcp_config.json` | nothing: it prints the entry | printed, not written: `.windsurf/rules/recall.md` in the workspace, though current builds prefer `.devin/rules/` |

**Windsurf is print-only.** `recall mcp install windsurf` writes nothing and prints the same
entry `recall mcp config windsurf` does. The vendor's own page says the config file is
`~/.codeium/mcp_config.json`; several third-party guides say `~/.codeium/windsurf/mcp_config.json`,
one directory deeper. Writing to the wrong one is the worst outcome on offer: an install that
reports success and a client that never sees recall. So recall prints the entry with the
documented path and leaves the one judgement it cannot make where it belongs.

**Cursor's rule file is print-only too, for a different reason**: its config entry is merged
normally, it is only the rule that is printed. Cursor documents `.cursor/rules` at the project
level only, and its global User Rules live in its settings rather than in a directory it scans,
so a file written to a guessed path under `~` would be read by nothing. recall prints the rule
with the documented workspace path and leaves the placing to you. Windsurf's rule is printed on
the same grounds.

### The plugin and extension route

For Claude Code, Codex and Gemini there is a second way in that does not involve
`recall mcp install` at all. `integrations/` in this repo is a checked-in export of the same
assets (a plugin marketplace for Claude Code, a plugin for Codex, an extension for Gemini)
and `recall mcp export <dir>` regenerates it from the binary. Each is consumed by that
*client's own* plugin command: recall installs no marketplace entry and knows nothing about one.

Those manifests register the server as a bare `"command": "recall"` with
`"args": ["mcp", "serve"]`, so **`recall` has to be on your `PATH`** for that route to work, because a
file checked into a repo cannot know where the binary will sit on your machine. `recall mcp
install` is the other case: it resolves its own absolute path with `os.Executable`, so it names
the exact binary you ran it from.
