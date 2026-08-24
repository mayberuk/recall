---
type: research-report
id: R-20260817-client-install-surfaces
date: 2026-08-17
scope: project
mode: harness@26a16c2f
question: For Claude Code, Codex CLI, Cursor, Gemini CLI, Windsurf, GitHub Copilot CLI, and OpenCode — how does a user install a stdio MCP server (command, config file, JSON/TOML shape, scope), does the client support an auto-loaded skill/instruction unit and how does its description trigger auto-invocation, can it install a Claude-Code-style plugin/marketplace bundling MCP + instructions, and what environment variables does it expose to MCP servers/child processes?
tags: [mcp, install, claude-code, codex, cursor, gemini-cli, windsurf, opencode, skills, plugins]
sha: 55fbee1
---

# Client install surfaces for MCP servers and auto-loaded skills

> **Provenance**
> - **Report ID:** R-20260817-client-install-surfaces
> - **Commissioning question:** How does each of the seven target clients handle stdio MCP server installation (command/config/scope), auto-loaded skill-equivalent instructions, Claude-Code-style plugin/marketplace bundling, and MCP-server environment variables?
> - **Mode:** harness@26a16c2f
> - **Builds on (does not re-research):** nothing prior in this repo on this topic — first pass. Does not cover the MCP spec itself or Go SDKs (sibling research owns that), transcript formats, or session storage.
> - **Method:** Seven parallel fork agents, one per client, each running WebFetch + WebSearch against that vendor's live docs on 2026-08-17 (`code.claude.com`, `platform.claude.com`, `developers.openai.com`/`learn.chatgpt.com`, `github.com/openai/codex`, `cursor.com/docs`, `geminicli.com`, `docs.windsurf.com`→`docs.devin.ai`, `docs.github.com`, `opencode.ai/docs`). Two of the seven (Claude Code, GitHub Copilot CLI) discovered mid-task that further delegation was unavailable ("Fork is not available inside a forked worker") and redid the full seven-client sweep solo instead of staying scoped; the orchestrating session then cross-checked their output against the five narrowly-scoped passes, resolved two discrepancies with direct re-fetches (Cursor plugins, OpenCode Skills), and merged the result into this file. WebFetch renders pages through a summarizing model rather than returning raw HTML; quotes below are the spans WebFetch returned inside quotation marks, the closest available approximation to verbatim page text — see Caveats.
> - Append-only. Do not edit claim blocks after they are written; supersede with a new
>   claim, and record status changes as events.

## Bottom line

All seven clients as of 2026-08-17 support stdio MCP servers via a JSON or TOML config file with `command`/`args`/`env` keys, and six of the seven ship a CLI subcommand to write that config (OpenCode's `opencode mcp add` is interactive-only, with no visible flag syntax). Every client except Cursor and Windsurf has shipped some form of skill-equivalent auto-loaded instruction unit with a `description`-triggers-selection mechanism this year: Claude Code's SKILL.md (the reference implementation others increasingly copy), Codex's SKILL.md under `.agents/skills` (a cross-tool "Agent Skills" convention it shares with ChatGPT and, per Claude's own docs, with other AI tools), Gemini CLI's new Agent Skills (`geminicli.com/docs/cli/skills/`, `SKILL.md`, `activate_skill` tool), GitHub Copilot CLI's custom agents (`.agent.md` files), and **OpenCode's own dedicated Skills feature** (`opencode.ai/docs/skills/` — a `SKILL.md` with `name`/`description` frontmatter, invoked via a native `skill({name: ...})` tool call, discovered not only from `.opencode/skills/` and `~/.config/opencode/skills/` but also directly from `.claude/skills/`, `~/.claude/skills/`, `.agents/skills/`, and `~/.agents/skills/` — meaning OpenCode reads Claude Code's own skill directories natively, on top of its separate sub-agent mechanism). Cursor and Windsurf instead rely on description-triggered *rules* (`.mdc` files / `trigger: model_decision`) rather than a distinct skill unit. On plugin/marketplace bundling, four clients now have a first-party marketplace that bundles MCP servers with instructions/skills — Claude Code (`plugin.json` + `marketplace.json`), Codex (`.codex-plugin/plugin.json` + `marketplace.json`, launched this year per practitioner sources), Cursor (2.5 "Plugins," two manifest formats), and GitHub Copilot CLI (`copilot plugin install`, default `copilot-plugins`/`awesome-copilot` marketplaces) — while Windsurf's "Plugin Store" is a VS Code/Open VSX IDE-extension registry unrelated to MCP+instruction bundling, and OpenCode has no first-party marketplace (community ones exist on GitHub/npm only). Windsurf itself is now documented under Cognition's `docs.devin.ai` (the vendor's own `docs.windsurf.com` 307-redirects there), and its rules docs describe `.windsurf/rules/` as still read but say "current builds prefer `.devin/rules/` going forward" — a rename in progress worth tracking before building an install target around the old path.

| Client | Install command | Config file (scope) | Stdio JSON/TOML shape | Skill-equivalent | Plugin/marketplace bundling MCP+instructions |
|---|---|---|---|---|---|
| Claude Code | `claude mcp add [opts] <name> -- <cmd> [args...]` | `.mcp.json` (project/shared), `~/.claude.json` (local/user); `-s/--scope local\|project\|user` | `{command, args, env}` (type defaults to stdio) | SKILL.md, `name`+`description` frontmatter, `~/.claude/skills`, `.claude/skills`, plugin `skills/` | Yes — `plugin.json` + `marketplace.json`, `.mcp.json` at plugin root or inline `mcpServers` |
| Codex CLI | `codex mcp add <name> --env K=V -- <cmd> [args...]` | `~/.codex/config.toml` (global; `add` always writes here) or `.codex/config.toml` (trusted project, edited manually) | `[mcp_servers.<name>]` with `command, args, env, env_vars, cwd, enabled` | SKILL.md under `.agents/skills` (repo/user/admin/bundled tiers), shared "Agent Skills" convention | Yes — `.codex-plugin/plugin.json` (skills+mcpServers+apps+hooks) + `marketplace.json` |
| Cursor | none confirmed (config-file only) | `.cursor/mcp.json` (project) / `~/.cursor/mcp.json` (global) | `{type: "stdio", command, args, env}` | No — `.cursor/rules/*.mdc` (`description`/`globs`/`alwaysApply`) plus `AGENTS.md`, not a distinct skill unit | Yes — Cursor 2.5 Plugins: Agent Plugins (`plugin.json`, skills+MCP) and Cursor Plugins (`.cursor-plugin/plugin.json`, rules+agents+commands+hooks) |
| Gemini CLI | `gemini mcp add <name> <cmd> [args...]` | `~/.gemini/settings.json` (user) / `.gemini/settings.json` (project), `mcpServers` key | `{command, args, env}` (cwd also supported per issue reports) | Yes (new in 2026) — Agent Skills, SKILL.md, `~/.gemini/skills` or `~/.agents/skills` (user) / `.gemini/skills` or `.agents/skills` (workspace), `--scope user\|workspace` on skill install, `activate_skill` tool | Yes — Extensions: `gemini-extension.json` (`mcpServers` + `contextFileName`/`GEMINI.md`), install via `gemini extensions install <source>` |
| Windsurf | none found (UI "Add Server" flow; config-file editing) | `~/.codeium/mcp_config.json` (global only found) | `{command, args, env}` | No — `.windsurf/rules/*` with `trigger: always_on\|manual\|model_decision\|glob`; docs note migration toward `.devin/rules/` | No — "Plugin Store" is an IDE-extension (VS Code/Open VSX) registry, not an MCP+rules bundler |
| GitHub Copilot CLI | `copilot mcp add SERVER-NAME -- COMMAND [ARGS...]` | `~/.copilot/mcp-config.json` (user) / `.mcp.json` or `.github/mcp.json` (repo, searched cwd→root) | `{type: "local", command, args, env, tools}` | Yes — custom agents, `.agent.md` files, `.github/agents/` (project) / `~/.copilot/agents/` (user), `description` field triggers selection; also reads AGENTS.md/`copilot-instructions.md`/CLAUDE.md/GEMINI.md | Yes — default marketplaces `copilot-plugins`/`awesome-copilot`, `marketplace.json`, `copilot plugin install` / `enabledPlugins` in settings |
| OpenCode | `opencode mcp add` (interactive wizard, no documented flag syntax) | `opencode.json` (global `~/.config/opencode/opencode.json`, project root; merged, project wins conflicts) | `{type: "local", command: [...], environment: {...}, enabled}` | Yes — dedicated Skills feature: SKILL.md, `name`+`description` frontmatter, native `skill({name})` tool call; discovered from `.opencode/skills/`, `~/.config/opencode/skills/`, and — cross-tool — `.claude/skills/`, `~/.claude/skills/`, `.agents/skills/`, `~/.agents/skills/` (reads Claude Code's own skill directories). Separately also has sub-agents (`.opencode/agents/`, description-triggered) and an always-loaded AGENTS.md | No first-party marketplace — plugins are JS/TS modules (`.opencode/plugins/` or npm via `opencode.json`); community marketplace repos exist outside the vendor |

## Claims

<!-- dp research add-claim writes ```claim blocks here. Prose explaining a claim goes
     immediately above it. Do not hand-author a fence. -->

## Deltas

For the planned MCP server + auto-invoked skill install path: (1) a one-step installer needs seven distinct recipes — there is no shared config format even at the JSON-shape level (Cursor/Windsurf/Gemini use `mcpServers`/`mcp`+bare object, Copilot CLI uses `type: "local"`, OpenCode uses `type: "local"` with `command` as an *array* not a string, Codex is TOML not JSON at all). (2) The "auto-invoked skill" half of the plan can reuse near-identical SKILL.md content for Claude Code, Codex, Gemini CLI, and OpenCode — all four now converge on a `name`+`description` YAML-frontmatter SKILL.md discovered from a user/workspace tier pair, which appears to be the emerging "Agent Skills" cross-tool standard Claude Code's own docs point to; OpenCode goes further and reads `.claude/skills/` directly, so a single `SKILL.md` written for Claude Code's skill directory is picked up by OpenCode with no changes. Cursor and Windsurf don't have a description-gated skill unit at all — for those two, the closest equivalent is a rule/instructions file, which is either always-on or matched by file glob, not by a natural-language trigger description in the same sense. (3) A Claude-Code-style plugin bundling MCP+skills together is now also possible for Codex, Cursor, and GitHub Copilot CLI — worth deciding whether `recall`'s distribution targets that heavier unit for those three rather than a bare MCP-server install.

## Gems

- Codex, Gemini CLI, and Copilot CLI custom agents all independently landed on the same shape this year: a markdown file with `name`+`description` frontmatter, auto-selected by matching the user's request against the description, discovered from a small number of fixed directories (repo-local, then user-home, sometimes an admin/system tier). Claude Code's SKILL.md predates and appears to be the template each is converging toward; Claude Code's own docs explicitly cite an "Agent Skills open standard" that "works across multiple AI tools."
- Windsurf is no longer documented at `docs.windsurf.com` as a standalone product — that domain 307-redirects to `docs.devin.ai/windsurf/...`, i.e. it's folded into Cognition's Devin documentation. Its rules docs say `.windsurfrules` and `.windsurf/rules/` "are still read, but current builds prefer `.devin/rules/` going forward" — a live path migration, not a hypothetical one.
- Codex's plugin marketplace (`.codex-plugin/plugin.json`, `marketplace.json` at `~/.agents/plugins/` or `$REPO_ROOT/.agents/plugins/`) reuses the same `.agents/` root directory convention as its SKILL.md discovery tiers — the two features share a filesystem namespace, which is convenient if `recall` wants to ship both a skill and an MCP entry from one Codex plugin package.
- GitHub Copilot CLI's instruction-file discovery explicitly lists CLAUDE.md and GEMINI.md as files it reads in addition to its own AGENTS.md/`copilot-instructions.md` — it treats other vendors' project-context files as first-class inputs, which is unusual cross-client interoperability worth knowing about if `recall`'s own memory files need to be legible to more than one client at once.
- OpenCode's Skills feature (`opencode.ai/docs/skills/`) directly scans `.claude/skills/` and `~/.claude/skills/` alongside its own `.opencode/skills/` — a single SKILL.md authored for Claude Code is discovered by OpenCode unmodified. If `recall` ships one SKILL.md targeting Claude Code's skill directory convention, OpenCode support may be close to free.

## Caveats and data quality

- **WebFetch is not a literal HTML dump.** Every quote above and in the findings JSON came back from WebFetch's summarizing model, not raw page text. Spans inside quotation marks in the tool output are the highest-confidence approximation of verbatim wording available in this pass, and that's what's cited; anything WebFetch paraphrased without quote marks was treated as directional, not quotable, and excluded from findings. A follow-up pass with raw HTML fetching (e.g. `curl` + manual excerpt) would tighten this.
- **No sub-agent delegation was available.** This report's commissioning instructions assumed parallel forked research agents; the environment refused with "Fork is not available inside a forked worker," so all ~30 fetches ran sequentially in one session. Coverage is complete per the stop condition, but cross-checking via a second independent fetch of the same page (corroboration) was done only where a discrepancy surfaced (Windsurf's config path), not systematically.
- **Windsurf config path has a live discrepancy.** The official `docs.devin.ai` page (redirected from `docs.windsurf.com`) states the config file is `~/.codeium/mcp_config.json`; several third-party guides (Fastio, mcp-confluent, Jentic) say `~/.codeium/windsurf/mcp_config.json` (with a `windsurf/` subdirectory). This report follows the official-vendor page but flags the conflict — verify against a live install before shipping an installer that writes to either path.
- **Cursor and Windsurf environment-variable exposure to MCP servers is a named absence, not a confirmed empty set.** Neither vendor's docs enumerate variables passed automatically to spawned MCP server processes (both only document user-declared `env`/interpolation *into* server config, e.g. `${env:NAME}`). This may mean nothing is exposed beyond what the user configures, or it may mean the docs simply don't cover it — genuinely unclear from this pass.
- **Gemini CLI's MCP-specific `--scope` flag (as opposed to the skill-install `--scope`) was not directly confirmed.** The `user`/`workspace` scope flag is documented for `gemini extensions install`/skill install; for `gemini mcp add` specifically, scope is achieved by which `settings.json` file you edit (or Gemini CLI defaults to), and this report does not assert an equivalent `-s` flag exists on `mcp add` itself without a direct quote — treat as unconfirmed rather than assumed.
- **Codex plugin marketplace launch date (cited as "March 27, 2026" by one practitioner source, unite.ai) is practitioner-sourced, not vendor-confirmed** — included in Deltas/Gems context but not as a dated claim in the findings JSON.
- **OpenCode's `opencode mcp add` flag syntax is thin.** The CLI reference page confirms the subcommand exists and is interactive, but no non-interactive flag syntax (equivalent to `--env` or positional command args) was found in the docs — unlike every other client's `mcp add`, OpenCode's may be wizard-only.
- **This report is a merge of two independent full-sweep passes plus five narrowly-scoped single-client passes.** Two of the seven commissioned agents (assigned Claude Code and GitHub Copilot CLI respectively) each hit "Fork is not available inside a forked worker" and, rather than stopping, redid the entire seven-client sweep solo instead of staying in scope — producing two overlapping drafts of this same file, of which this file reflects the first. Both comprehensive passes independently missed OpenCode's dedicated Skills feature (`opencode.ai/docs/skills/`), describing only its sub-agents mechanism instead; this was caught by comparing against the narrowly-scoped OpenCode-only pass (which did find it) and confirmed with a direct re-fetch, both agreeing on the exact frontmatter and cross-tool discovery paths now in the table above. Treat this as a general caution about broad research passes: a wide sweep across seven vendors' docs sites is prone to missing a page that a narrowly-scoped pass on one vendor will find. The Cursor plugin-bundling claim, by contrast, was in both comprehensive passes but absent from the narrowly-scoped Cursor pass (which didn't think to check `/docs/plugins`) — resolved in favor of "plugins exist" after a direct re-fetch of `cursor.com/docs/reference/plugins` and `cursor.com/docs/plugins` confirmed the manifest schema verbatim.

```claim
spec: 1
id: C-20260817-claude-mcp-add-stdio-syntax
promoted_to: vault:research/recall/research/C-20260817-claude-mcp-add-stdio-syntax.md
```

```claim
spec: 1
id: C-20260817-claude-mcp-scope-flag
promoted_to: vault:research/recall/research/C-20260817-claude-mcp-scope-flag.md
```

```claim
spec: 1
id: C-20260817-claude-mcp-json-shape
promoted_to: vault:research/recall/research/C-20260817-claude-mcp-json-shape.md
```

```claim
spec: 1
id: C-20260817-claude-skill-frontmatter-limits
promoted_to: vault:research/recall/research/C-20260817-claude-skill-frontmatter-limits.md
```

```claim
spec: 1
id: C-20260817-claude-skill-description-third-person
promoted_to: vault:research/recall/research/C-20260817-claude-skill-description-third-person.md
```

```claim
spec: 1
id: C-20260817-claude-skills-directory-locations
promoted_to: vault:research/recall/research/C-20260817-claude-skills-directory-locations.md
```

```claim
spec: 1
id: C-20260817-claude-plugin-root-var-mcp
promoted_to: vault:research/recall/research/C-20260817-claude-plugin-root-var-mcp.md
```

```claim
spec: 1
id: C-20260817-claude-plugin-env-vars-to-mcp
promoted_to: vault:research/recall/research/C-20260817-claude-plugin-env-vars-to-mcp.md
```

```claim
spec: 1
id: C-20260817-claude-plugin-marketplace-file
promoted_to: vault:research/recall/research/C-20260817-claude-plugin-marketplace-file.md
```

```claim
spec: 1
id: C-20260817-codex-mcp-add-syntax
promoted_to: vault:research/recall/research/C-20260817-codex-mcp-add-syntax.md
```

```claim
spec: 1
id: C-20260817-codex-config-toml-scope
promoted_to: vault:research/recall/research/C-20260817-codex-config-toml-scope.md
```

```claim
spec: 1
id: C-20260817-codex-agents-md-chain
promoted_to: vault:research/recall/research/C-20260817-codex-agents-md-chain.md
```

```claim
spec: 1
id: C-20260817-codex-skills-frontmatter-locations
promoted_to: vault:research/recall/research/C-20260817-codex-skills-frontmatter-locations.md
```

```claim
spec: 1
id: C-20260817-codex-plugin-manifest
promoted_to: vault:research/recall/research/C-20260817-codex-plugin-manifest.md
```

```claim
spec: 1
id: C-20260817-cursor-mcp-json-paths
promoted_to: vault:research/recall/research/C-20260817-cursor-mcp-json-paths.md
```

```claim
spec: 1
id: C-20260817-cursor-rules-description-trigger
promoted_to: vault:research/recall/research/C-20260817-cursor-rules-description-trigger.md
```

```claim
spec: 1
id: C-20260817-cursor-no-skills-system
promoted_to: vault:research/recall/research/C-20260817-cursor-no-skills-system.md
```

```claim
spec: 1
id: C-20260817-cursor-plugin-formats
promoted_to: vault:research/recall/research/C-20260817-cursor-plugin-formats.md
```

```claim
spec: 1
id: C-20260817-gemini-mcp-add-scope
promoted_to: vault:research/recall/research/C-20260817-gemini-mcp-add-scope.md
```

```claim
spec: 1
id: C-20260817-gemini-settings-json-paths
promoted_to: vault:research/recall/research/C-20260817-gemini-settings-json-paths.md
```

```claim
spec: 1
id: C-20260817-gemini-mcp-env-redaction
promoted_to: vault:research/recall/research/C-20260817-gemini-mcp-env-redaction.md
```

```claim
spec: 1
id: C-20260817-gemini-agent-skills-tiers
promoted_to: vault:research/recall/research/C-20260817-gemini-agent-skills-tiers.md
```

```claim
spec: 1
id: C-20260817-gemini-extension-manifest
promoted_to: vault:research/recall/research/C-20260817-gemini-extension-manifest.md
```

```claim
spec: 1
id: C-20260817-windsurf-devin-docs-redirect
promoted_to: vault:research/recall/research/C-20260817-windsurf-devin-docs-redirect.md
```

```claim
spec: 1
id: C-20260817-windsurf-mcp-config-path
promoted_to: vault:research/recall/research/C-20260817-windsurf-mcp-config-path.md
```

```claim
spec: 1
id: C-20260817-windsurf-rules-triggers
promoted_to: vault:research/recall/research/C-20260817-windsurf-rules-triggers.md
```

```claim
spec: 1
id: C-20260817-windsurf-workflows-manual-only
promoted_to: vault:research/recall/research/C-20260817-windsurf-workflows-manual-only.md
```

```claim
spec: 1
id: C-20260817-windsurf-no-plugin-bundle
promoted_to: vault:research/recall/research/C-20260817-windsurf-no-plugin-bundle.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-mcp-add-syntax
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-mcp-add-syntax.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-mcp-scope-paths
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-mcp-scope-paths.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-path-only-env
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-path-only-env.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-custom-agent-files
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-custom-agent-files.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-cross-vendor-instructions
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-cross-vendor-instructions.md
```

```claim
spec: 1
id: C-20260817-copilot-cli-marketplace-defaults
promoted_to: vault:research/recall/research/C-20260817-copilot-cli-marketplace-defaults.md
```

```claim
spec: 1
id: C-20260817-opencode-mcp-local-shape
promoted_to: vault:research/recall/research/C-20260817-opencode-mcp-local-shape.md
```

```claim
spec: 1
id: C-20260817-opencode-config-merge-precedence
promoted_to: vault:research/recall/research/C-20260817-opencode-config-merge-precedence.md
```

```claim
spec: 1
id: C-20260817-opencode-mcp-add-interactive
promoted_to: vault:research/recall/research/C-20260817-opencode-mcp-add-interactive.md
```

```claim
spec: 1
id: C-20260817-opencode-skills-feature
promoted_to: vault:research/recall/research/C-20260817-opencode-skills-feature.md
```

```claim
spec: 1
id: C-20260817-opencode-agents-md-discovery
promoted_to: vault:research/recall/research/C-20260817-opencode-agents-md-discovery.md
```

```claim
spec: 1
id: C-20260817-opencode-plugins-are-hooks-no-marketplace
promoted_to: vault:research/recall/research/C-20260817-opencode-plugins-are-hooks-no-marketplace.md
```
