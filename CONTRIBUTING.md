# Contributing

## Build

```sh
go build -o ~/.local/bin/recall ./cmd/recall
```

Static binary, `CGO_ENABLED=0`, Go 1.25, two external dependencies
(`github.com/tidwall/gjson`, `github.com/modelcontextprotocol/go-sdk`).

Go 1.25 is the toolchain floor. This drops Go 1.24 support — not by preference, but because
`github.com/modelcontextprotocol/go-sdk` declares `go 1.25.0` in its own `go.mod`, and that
requirement carries into this module's directive.

`internal/scan` carries hand-written assembly for arm64 and amd64 — the case-folding pass, in
`fold_arm64.s` and `fold_amd64.s`. Neither is required: `fold_noasm.go` supplies a stub for any
other architecture, and the pure-Go word-at-a-time loop in `fold.go` runs underneath on all of
them, so `linux/386` and `linux/riscv64` build and pass with no vector code at all.

If you touch either file, the bar is the byte-at-a-time reference in `fold_test.go`, not the
Go implementation beside it — holding assembly to a faster Go version lets a shared misreading
pass both. Run `go test -fuzz=FuzzFold ./internal/scan` for a minute, and again under
`GOARCH=amd64` if you have a way to execute it (on Apple silicon, Rosetta will).

## Test

Five tiers, in the order you'd normally reach for them:

| Tier | Command | When |
|---|---|---|
| Unit | `go test ./...` | Every change. Table-driven, deterministic, no real corpus needed. |
| Race | `go test -race ./...` | Before any change touching concurrency (the archive writer, the corpus walk). |
| Integration | `go test ./tests/integration/...` | Before any change that crosses package boundaries (strip → repo → archive → cmd). |
| Differential | `go test ./tests/differential/` | Before any change meant to be output-neutral, which every optimization is. |
| Acceptance | `./tests/acceptance/run.sh` | Before claiming a behavioral change is done. Runs the acceptance cases against a built binary and writes raw evidence to `logs/acceptance/<case>/` for a separate judge to grade — the runner never grades its own output. |

The differential tier is the one that catches an optimization that changed an answer. It builds
the binary from the `perf-baseline` tag in a throwaway git worktree, runs both binaries over a
fixed query battery against one generated corpus, and requires byte-identical stdout, stderr and
exit code. Set `RECALL_DIFF_BASE` to compare against a different ref; if the ref does not resolve
— a shallow clone has no tags — the harness skips and says so.

The harness sets `RECALL_TURNS_PER_RANGE` for the binary under test, which lowers how many turns a
goroutine's slice of the corpus must hold to be worth cutting. Its corpus is deliberately small
enough to build twice per run, and without that override it is under the threshold — every case
would compare two single-pass scans and the concurrent path would go unrun. The baseline predates
sharding, so it is left alone: its output is the single-pass answer by construction, and that is
what the binary under test has to match.

If you add a query shape to the matcher, add it to that battery. A battery case that agrees with
the baseline because it never reaches your code is worse than no case at all, and every gap found
so far was exactly that — including the harness itself never sharding, which went unnoticed
through a whole wave of parallelism work. Prove a new case bites by breaking the code it covers
and watching it fail.

Before claiming a performance change, run it against the generated corpus CI uses:

```sh
make bench-gate     # the wall-clock thresholds
make bench-compare  # allocation counts against the committed baseline
```

`bench-gate` is what "before claiming any performance change" means in practice — quote its
numbers. The architecture has no index *because* linear scanning measured fast; a claim that a
change is faster (or that it didn't regress) needs one of these runs, not a fixture-only pass.
Thresholds and the baselines they're measured against are in `docs/design.md`.

A few packages carry a separate, opt-in gate behind an environment variable, because they assert
against your actual `~/.claude/projects/` corpus rather than the generated one:

```sh
RECALL_REAL_CORPUS=1 go test ./...
```

This measures against a real session store, which `make bench-gate` and `make bench-compare`
never touch — reach for it when a claim needs to hold on real data, not only on the generator's.

## The external dependencies

A dependency is a decision written down, not a `go get`. Two direct dependencies currently earn
their place, each for a different reason.

`gjson` earned its place on measurement: stripping the full corpus takes 1.31 s with `gjson`
versus 7.21 s with the standard library's `encoding/json` — 5.5x, because `gjson` extracts the
one JSON path a caller asks for instead of unmarshaling every field of every record.

`github.com/modelcontextprotocol/go-sdk` earned its place on reasoning rather than measurement:
the Model Context Protocol wire format is a spec, not a format this repo should re-implement, and
an official v1 implementation is cheaper to hold correct across protocol revisions than a
hand-rolled decoder would be. The alternative — reading the spec once and writing a client by
hand — is exactly the wrong side of the standard above: a protocol surface that changes out from
under a hand-rolled parser is a maintenance cost this repo has no reason to carry itself.

`docs/design.md` declined `golang.org/x/sys` as a *direct* dependency, on the grounds that
`golang.org/x/sys/cpu` would break the one-dependency rule for a wider register on a step already
fast enough. That rule was about direct dependencies. The SDK brings `golang.org/x/sys` in
indirectly, along with seven other indirect modules — nobody chose those, they came along with
the SDK the way `github.com/tidwall/match` and `github.com/tidwall/pretty` come along with
`gjson`, and the gate only ever policed the direct set. Indirect is not the same as direct, but
the rule's spirit is worth stating plainly rather than leaving it to inference: this repo still
would not add `golang.org/x/sys/cpu` as a direct import for the reason `docs/design.md` gives,
and the SDK's indirect pull of the package doesn't change that.

## `~/.claude/projects` is read-only

It is the only copy of the session corpus. If a task looks like it needs to write, move, rename,
or delete anything under it, the task is specified wrong — stop and raise it rather than finding
a way to make it work.

## Comments

Say the why, not the what. Default to no comment; names and types carry the what. A comment
earns its place for an invariant, a non-obvious constraint, a rejected alternative, or a
workaround naming its cause — never for restating the line below it.

## Before you open a PR

- `go vet ./...` and `gofmt -l .` (no output) are both clean.
- The tier that matches the size of your change (see the table above) passes.
- If you touched anything on the hot path (`internal/scan`, `internal/archive`,
  `internal/strip`), `make bench-gate` and `make bench-compare` ran and their numbers are in the
  PR description.
