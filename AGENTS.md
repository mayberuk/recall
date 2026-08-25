# AGENTS.md

Contract for a coding agent working **on recall's source**. If you are an agent *calling* recall
to search past sessions, you want [docs/agents.md](docs/agents.md) instead.

recall is a Go CLI that searches every past coding-agent session transcript on the machine. Single
static binary, no index, no daemon, no config file.

## Commands

```sh
go build -o recall ./cmd/recall     # or: make build   (CGO_ENABLED=0)
go test ./...                       # every change
go test -race ./...                 # anything touching the archive writer or the corpus walk
go test ./tests/differential/       # anything meant to be output-neutral
make cross                          # 8 targets, incl. the two with no assembly path
./scripts/demo.sh                   # rebuild the output quoted in README and docs/examples.md
```

Before opening a PR, all of: `gofmt -l .` (empty), `go vet ./...`, `staticcheck ./...`,
`go mod tidy -diff`, `./scripts/deps-gate.sh`, `./scripts/coverage-gate.sh`, `make bench-compare`.
CI runs exactly these.

## Hard boundaries

- **Two direct dependencies, and `scripts/deps-gate.sh` fails the build on a third.** It is an
  exact-match allowlist, not a prefix match. Adding one is a design decision, not a convenience.
  argue it in the PR before writing the import.
- **Never write under a session store.** `~/.claude/projects` and `~/.codex/sessions` are read-only
  to this program, always. They are the user's only copy.
- **Go 1.25 is the floor**, because the MCP SDK declares it. Do not raise it further without cause.
- **Do not edit `bench/RESULTS.md` or the allocation baseline by hand.** They are generated:
  `make bench` and `make bench-baseline`.
- **Output-neutral means byte-identical.** `tests/differential` builds the binary from the
  `perf-baseline` tag and requires matching stdout, stderr and exit code. If you changed an
  answer on purpose, say so in the PR and update the baseline deliberately.

## Style

- **Comments say why, not what.** Names and types carry the *what*. Comment the non-obvious
  constraint, the measurement behind a threshold, the invariant that isn't visible locally. This
  codebase is dense with such comments and that is deliberate. Match it, and delete any comment
  that only restates the line under it.
- No banner comments, no commented-out code, no TODO/FIXME left behind, no changelog or
  attribution in source. Git remembers.
- Doc comments on the exported surface where the name and signature aren't self-evident.
- Errors carry an `fperr` code; every non-zero exit prints `ERROR_CODE=<slug>` on stderr.

## Testing

A test that would pass against wrong behaviour is not coverage. Specifically:

- Derive the expected value from the requirement, never by running the new code and pasting what
  it returned.
- A count is not a claim about *which* thing survived. Assert on identity where identity is what
  the requirement is about.
- Any criterion worth asserting is worth a **negative control**, a case that must fail. Several
  tests here exist only to be the control for a neighbour; don't delete one for being redundant.
- Don't let the setup pre-supply the answer the assertion is checking for.

## Where things live

| Path | What |
|---|---|
| `cmd/recall` | verbs, flag parsing, the MCP sub-command |
| `internal/scan` | the linear search, incl. hand-written arm64/amd64 fold assembly |
| `internal/archive` | the tiered store, cursors, dedup, multi-agent groups |
| `internal/strip` | per-agent transcript decoding; each agent registers a `Provider` |
| `internal/render` | every output form: text, `--json`, `--format jsonl`, the `──` footer |
| `internal/mcp` | the MCP server, its five tools, and the client install recipes |
| `tests/differential` | byte-for-byte comparison against the `perf-baseline` tag |
| `scripts/demo` | the fixed corpus behind every example in the docs |

Depth on any of this: [CONTRIBUTING.md](CONTRIBUTING.md). Where a decision is load-bearing it is
recorded **with its measurement** in the comment next to the code it constrains; if you change one,
change that comment too.
