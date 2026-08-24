---
type: research-report
id: R-20260817-mcp-spec-and-go-sdk
date: 2026-08-17
scope: project
mode: harness@26a16c2f
question: What is the current MCP specification revision and what must a compliant Go stdio tool server implement, and which Go SDK should recall use?
tags: [mcp, specification, go, sdk, stdio]
sha: 55fbee1
---

# MCP specification and Go SDK, as of 2026-08-17

> **Provenance**
> - **Report ID:** R-20260817-mcp-spec-and-go-sdk
> - **Commissioning question:** What is the current MCP specification revision and what must a compliant Go stdio tool server implement, and which Go SDK should recall use?
> - **Mode:** harness@26a16c2f
> - **Builds on (does not re-research):** nothing — first MCP pass for this repo
> - **Method:** Direct WebFetch of modelcontextprotocol.io specification pages (2026-07-28 and draft), blog.modelcontextprotocol.io, the spec repo's raw `schema/2026-07-28/schema.ts`, GitHub releases pages for `modelcontextprotocol/modelcontextprotocol`, `modelcontextprotocol/go-sdk` and `mark3labs/mcp-go`, plus pkg.go.dev for both Go modules. All retrievals 2026-08-17. Context7 MCP tools were **not** available in this session (see Caveats) — SDK facts come from pkg.go.dev and GitHub instead, which are the same primary sources Context7 indexes.
> - Append-only. Do not edit claim blocks after they are written; supersede with a new
>   claim, and record status changes as events.

## Bottom line

The current released MCP revision is **`2026-07-28`**, published 28 July 2026, and it is a hard break with everything the ecosystem was built on: MCP is now a **stateless request/response protocol**. The `initialize` / `notifications/initialized` handshake is gone, protocol-level sessions are gone, and every single request must carry its own protocol version and client capabilities in `_meta` under `io.modelcontextprotocol/*`. In its place, **servers MUST implement a new `server/discover` RPC** that advertises supported versions, capabilities, and identity. A draft revision exists (`/specification/draft`, schema at `schema/draft/schema.ts`) but its changelog is empty — "Changes since the most recent release will accumulate here." — so there is no substantive newer draft yet.

For a stdio tool server the minimum surface is now: **`server/discover` + `tools/list` + `tools/call`**, with the `tools` capability declared, newline-delimited JSON on stdout (no embedded newlines, nothing on stdout that is not a valid MCP message), `stderr` free for logging, every result carrying `resultType`, and version mismatches answered with `UnsupportedProtocolVersionError` (`-32022`). JSON-RPC batching is not part of the protocol (removed in `2025-06-18`); stdio framing is explicitly one message per line. Logging as a protocol feature is now **deprecated** — the official migration advice for stdio servers is literally "log to `stderr`".

On the Go side the choice is not close. **`github.com/modelcontextprotocol/go-sdk` v1.7.0 (28 July 2026)** is stable, past v1, and ships "full support for protocol version `2026-07-28`" plus backward compatibility down to `2024-11-05`. **`github.com/mark3labs/mcp-go`** is at stable **v0.58.0 (11 Aug 2026)**, which documents only `2025-11-25` — the *legacy* era — with `v1.0.0-beta.1` (12 Aug 2026) the first prerelease to add `2026-07-28`. For a new server in August 2026, use the official SDK: `mcp.NewServer` + `mcp.AddTool` + `server.Run(ctx, &mcp.StdioTransport{})` is about ten lines.

## Claims

<!-- dp research add-claim writes ```claim blocks here. Prose explaining a claim goes
     immediately above it. Do not hand-author a fence. -->

## Deltas

**The plan almost certainly assumed the wrong protocol shape.** Anyone designing an MCP surface from pre-2026 knowledge will start by implementing an `initialize` handshake and negotiating capabilities once per connection. That is now the *legacy* era. If `recall`'s MCP server is written against `2026-07-28`, it must implement `server/discover`, must not assume any per-connection state, and must read the protocol version and client capabilities out of `_meta` on **every** request. The spec is blunt about the consequence: "an open connection, such as a STDIO process, is not a conversation or session".

**Statelessness is a design constraint on `recall`'s tools, not just plumbing.** Anything that would naturally have been "search, then page through results on this connection" has to become an explicit server-minted handle passed back as an ordinary tool argument — the spec's "Stateful Tools" section spells out the handle pattern, its authorization, opacity, lifetime, and expiry-error obligations. Cursor-based pagination on `tools/list` still exists, but per-connection result-set state does not: `tools/list` "MUST NOT vary per-connection".

**Don't hand-roll the wire.** Given that the protocol changed this fundamentally three weeks before this report, and that the official Go SDK already implements dual-era negotiation ("The SDK negotiates the highest mutually-supported version at connect time"), writing the JSON-RPC layer by hand would be re-implementing dual-era compatibility for no gain. Pin `github.com/modelcontextprotocol/go-sdk` at v1.7.0+.

**Two logging decisions fall out for free.** Protocol logging (`logging/setLevel`, `notifications/message`) is deprecated and `logging/setLevel` was removed outright; log level now arrives per-request in `_meta` and servers "MUST NOT emit `notifications/message` for requests that did not include this field". The recommended path for a stdio server is `stderr`, which the spec explicitly blesses and which clients "SHOULD NOT assume ... indicates error conditions". So: keep `recall`'s existing logger, point it at stderr, and skip the logging capability entirely.

**Structured output is cheap and worth taking.** `outputSchema` + `structuredContent` are now loose enough to carry any JSON value under any JSON Schema 2020-12 keyword set, and the Go SDK derives both from Go struct types via `jsonschema` tags — the quickstart returns a typed `Output` struct directly. For a search tool returning session hits, that is strictly better than stuffing formatted text into a `TextContent` block. Note the backward-compatibility nudge: a tool returning structured content "SHOULD also return the serialized JSON in a TextContent block".

## Gems

- **The era model is the useful mental frame.** The spec names it: *Modern* = `2026-07-28` and later (per-request metadata), *Legacy* = `2025-11-25` and earlier (`initialize` handshake), *Dual-era* = supports both. The versioning page ships a full 7-row client×server compatibility matrix, which is the fastest way to reason about "what breaks if the client is old".
- **The stdio backward-compat probe is a neat trick worth stealing.** A dual-era client sends `server/discover` first; a `DiscoverResult` means modern, a recognized modern error means modern-but-wrong-version, and *anything else or a timeout* means legacy. The spec explicitly warns not to key the fallback to one error code, because legacy servers "respond to unknown pre-`initialize` requests with implementation-defined errors (commonly `-32601` or `-32602`) or not at all."
- **Deterministic tool ordering is now a documented performance lever, not a nicety.** "Servers **SHOULD** return tools in a deterministic order ... improves LLM prompt cache hit rates when tools are included in model context." Free win: sort the tool list.
- **A formal deprecation policy now exists** — Active / Deprecated / Removed, with a minimum twelve-month deprecation window and a public registry of deprecated features. That makes it reasonable to depend on a feature that is merely deprecated (Roots, Sampling, Logging, HTTP+SSE) for at least a year, rather than treating deprecation as imminent removal.
- **The stdio framing is transport-agnostic on purpose.** "The wire format (one newline-delimited JSON-RPC message per line over a reliable bidirectional byte stream) works unchanged over Unix domain sockets, TCP connections, or any similar channel." Useful if `recall` ever wants a socket mode without inventing a framing.
- **Tool-name rules are all SHOULD, never MUST**: 1–128 chars, case-sensitive, `[A-Za-z0-9_.-]` only, unique within a server. Uniqueness is scoped to one server, and the spec explicitly says `serverInfo.name` "**SHOULD NOT** be relied upon for disambiguation" by aggregating clients.

## Caveats and data quality

- **Context7 was unavailable.** The task specified loading Context7 MCP tools via `ToolSearch("context7")`; the search returned only `SendMessage`, `WebFetch`, and `WebSearch` — no `resolve-library-id` or `query-docs` tool is registered in this session. No HTTP error, simply absent from the deferred-tool roster. Go SDK facts were sourced instead from pkg.go.dev, the GitHub releases pages, and the raw README, which are the upstream primaries.
- **Verbatim fidelity of a few SDK quotes is one step removed.** `WebFetch` summarizes a page through a small model, so a handful of GitHub-releases quotes (the go-sdk v1.7.0 release-note phrases, the mcp-go v1.0.0-beta.1 commit-title) are the fetcher's extraction of the page rather than a byte-exact scrape. The load-bearing spec quotes were returned as full page text and are exact. If a claim will be cited externally, re-verify the two go-sdk release-note sentences against the release page directly.
- **`ToolAnnotations` no longer appears in the 2026-07-28 prose.** The tools page now describes `annotations` only as "Optional properties describing tool behavior" plus the untrusted-source warning; the hint table that used to be on that page is gone. The four hints (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) and their defaults were recovered from the authoritative `schema/2026-07-28/schema.ts`, which is the stated source of truth. The rendered `/specification/2026-07-28/schema` page truncated before reaching `ToolAnnotations` on fetch — the raw TypeScript worked.
- **`mark3labs/mcp-go` release dates are year-ambiguous.** The GitHub releases page renders recent dates as "Aug 12" / "Aug 11" without a year; 2026 is inferred from pkg.go.dev, which stamps v0.58.0 as 11 Aug 2026. The v1.0.0-beta.1 date (12 Aug) is inferred by adjacency and should be re-confirmed if it matters.
- **Thin: authorization.** Question 2 is answered for authorization at changelog granularity (RFC 9207 `iss` validation, DCR deprecated in favor of Client ID Metadata Documents, `application_type` in DCR, credentials keyed by issuer). The authorization spec pages themselves were not fetched — irrelevant for a stdio server, which the spec says "**SHOULD NOT** follow this specification, and instead retrieve credentials from the environment", but a second pass should read them before any HTTP transport work.
- **Not researched, by scope:** how clients install/configure MCP servers, and transcript formats.

```claim
spec: 1
id: C-20260817-mcp-latest-revision-2026-07-28
promoted_to: vault:research/recall/research/C-20260817-mcp-latest-revision-2026-07-28.md
```

```claim
spec: 1
id: C-20260817-draft-changelog-empty
promoted_to: vault:research/recall/research/C-20260817-draft-changelog-empty.md
```

```claim
spec: 1
id: C-20260817-initialize-handshake-removed
promoted_to: vault:research/recall/research/C-20260817-initialize-handshake-removed.md
```

```claim
spec: 1
id: C-20260817-sessions-and-session-id-removed
promoted_to: vault:research/recall/research/C-20260817-sessions-and-session-id-removed.md
```

```claim
spec: 1
id: C-20260817-server-discover-mandatory
promoted_to: vault:research/recall/research/C-20260817-server-discover-mandatory.md
```

```claim
spec: 1
id: C-20260817-sse-resumability-removed
promoted_to: vault:research/recall/research/C-20260817-sse-resumability-removed.md
```

```claim
spec: 1
id: C-20260817-http-sse-transport-deprecated
promoted_to: vault:research/recall/research/C-20260817-http-sse-transport-deprecated.md
```

```claim
spec: 1
id: C-20260817-roots-sampling-logging-deprecated
promoted_to: vault:research/recall/research/C-20260817-roots-sampling-logging-deprecated.md
```

```claim
spec: 1
id: C-20260817-ping-and-setlevel-removed
promoted_to: vault:research/recall/research/C-20260817-ping-and-setlevel-removed.md
```

```claim
spec: 1
id: C-20260817-mrtr-replaces-server-requests
promoted_to: vault:research/recall/research/C-20260817-mrtr-replaces-server-requests.md
```

```claim
spec: 1
id: C-20260817-resulttype-required-on-all-results
promoted_to: vault:research/recall/research/C-20260817-resulttype-required-on-all-results.md
```

```claim
spec: 1
id: C-20260817-tasks-moved-to-extension
promoted_to: vault:research/recall/research/C-20260817-tasks-moved-to-extension.md
```

```claim
spec: 1
id: C-20260817-subscriptions-listen-replaces-get
promoted_to: vault:research/recall/research/C-20260817-subscriptions-listen-replaces-get.md
```

```claim
spec: 1
id: C-20260817-cacheable-list-results-ttlms
promoted_to: vault:research/recall/research/C-20260817-cacheable-list-results-ttlms.md
```

```claim
spec: 1
id: C-20260817-authorization-hardening-iss-and-dcr
promoted_to: vault:research/recall/research/C-20260817-authorization-hardening-iss-and-dcr.md
```

```claim
spec: 1
id: C-20260817-version-negotiation-no-handshake
promoted_to: vault:research/recall/research/C-20260817-version-negotiation-no-handshake.md
```

```claim
spec: 1
id: C-20260817-meta-required-fields-per-request
promoted_to: vault:research/recall/research/C-20260817-meta-required-fields-per-request.md
```

```claim
spec: 1
id: C-20260817-stdio-framing-rules
promoted_to: vault:research/recall/research/C-20260817-stdio-framing-rules.md
```

```claim
spec: 1
id: C-20260817-stdio-no-server-requests
promoted_to: vault:research/recall/research/C-20260817-stdio-no-server-requests.md
```

```claim
spec: 1
id: C-20260817-no-jsonrpc-batching
promoted_to: vault:research/recall/research/C-20260817-no-jsonrpc-batching.md
```

```claim
spec: 1
id: C-20260817-tools-capability-and-list-invariance
promoted_to: vault:research/recall/research/C-20260817-tools-capability-and-list-invariance.md
```

```claim
spec: 1
id: C-20260817-tool-naming-rules
promoted_to: vault:research/recall/research/C-20260817-tool-naming-rules.md
```

```claim
spec: 1
id: C-20260817-tool-annotations-hints-defaults
promoted_to: vault:research/recall/research/C-20260817-tool-annotations-hints-defaults.md
```

```claim
spec: 1
id: C-20260817-annotations-untrusted
promoted_to: vault:research/recall/research/C-20260817-annotations-untrusted.md
```

```claim
spec: 1
id: C-20260817-outputschema-structuredcontent-rules
promoted_to: vault:research/recall/research/C-20260817-outputschema-structuredcontent-rules.md
```

```claim
spec: 1
id: C-20260817-schema-keywords-loosened
promoted_to: vault:research/recall/research/C-20260817-schema-keywords-loosened.md
```

```claim
spec: 1
id: C-20260817-go-sdk-v1-7-0-current
promoted_to: vault:research/recall/research/C-20260817-go-sdk-v1-7-0-current.md
```

```claim
spec: 1
id: C-20260817-go-sdk-v1-stable
promoted_to: vault:research/recall/research/C-20260817-go-sdk-v1-stable.md
```

```claim
spec: 1
id: C-20260817-go-sdk-stdio-server-shape
promoted_to: vault:research/recall/research/C-20260817-go-sdk-stdio-server-shape.md
```

```claim
spec: 1
id: C-20260817-go-sdk-negotiates-versions
promoted_to: vault:research/recall/research/C-20260817-go-sdk-negotiates-versions.md
```

```claim
spec: 1
id: C-20260817-mcp-go-v0-58-legacy-spec
promoted_to: vault:research/recall/research/C-20260817-mcp-go-v0-58-legacy-spec.md
```

```claim
spec: 1
id: C-20260817-mcp-go-v1-beta-adds-2026-spec
promoted_to: vault:research/recall/research/C-20260817-mcp-go-v1-beta-adds-2026-spec.md
```

```claim
spec: 1
id: C-20260817-mcp-go-not-production-stable
promoted_to: vault:research/recall/research/C-20260817-mcp-go-not-production-stable.md
```

```claim
spec: 1
id: C-20260817-stdio-auth-from-environment
promoted_to: vault:research/recall/research/C-20260817-stdio-auth-from-environment.md
```

```claim
spec: 1
id: C-20260817-feature-lifecycle-12-month-window
promoted_to: vault:research/recall/research/C-20260817-feature-lifecycle-12-month-window.md
```
