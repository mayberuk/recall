# Contributing

## Build

```sh
go build -o ~/.local/bin/recall ./cmd/recall
```

Static binary, `CGO_ENABLED=0`, Go 1.24, one external dependency
(`github.com/tidwall/gjson`).

## Test

Four tiers, in the order you'd normally reach for them:

| Tier | Command | When |
|---|---|---|
| Unit | `go test ./...` | Every change. Table-driven, deterministic, no real corpus needed. |
| Race | `go test -race ./...` | Before any change touching concurrency (the archive writer, the corpus walk). |
| Integration | `go test ./tests/integration/...` | Before any change that crosses package boundaries (strip → repo → archive → cmd). |
| Acceptance | `./tests/acceptance/run.sh` | Before claiming a behavioral change is done. Runs the acceptance cases against a built binary and writes raw evidence to `logs/acceptance/<case>/` for a separate judge to grade — the runner never grades its own output. |

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

## The one external dependency

`gjson` is the sole third-party import, and adding a second one is a decision worth writing down,
not a quiet `go get`. It earned its place on measurement: stripping the full corpus takes 1.31 s
with `gjson` versus 7.21 s with the standard library's `encoding/json` — 5.5x, because `gjson`
extracts the one JSON path a caller asks for instead of unmarshaling every field of every record.

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
