GO ?= go

# The band this threshold has to sit in was measured, not guessed. Noise
# ceiling: rank's collapse benchmark reports 79 allocations seven runs in eight
# and 80 in the eighth, a 1.3% move, because a few very large blocks per
# operation put it at the mercy of the allocator. Signal floor: one extra
# allocation in scan.Search is a 2.7% move on the smallest benchmark. Two
# percent is the gap between them. It is a narrow gap — widen it to silence a
# failure and the next real regression goes through it.
ALLOC_THRESHOLD ?= 0.02

.PHONY: build cross test vet bench bench-gate bench-micro bench-compare bench-baseline clean

build:
	CGO_ENABLED=0 $(GO) build -o recall ./cmd/recall

# The release workflow's six targets plus linux/386 and linux/riscv64, which
# have no assembly path and so are the ones a change under internal/scan can
# break. Building them here rather than only on a pushed tag is the difference
# between finding that out now and finding it out during a release. Nothing is
# kept: this asks whether the compile succeeds, not what it produced.
CROSS_TARGETS = linux/amd64 linux/arm64 linux/386 linux/riscv64 \
                darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

cross:
	@set -e; for t in $(CROSS_TARGETS); do \
		echo "cross: $$t"; \
		CGO_ENABLED=0 GOOS=$${t%%/*} GOARCH=$${t##*/} $(GO) build -o /dev/null ./cmd/recall; \
	done

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# bench writes bench/RESULTS.md from a full run against a generated corpus, and
# fails if a wall-clock gate was breached on the way.
bench:
	$(GO) run ./bench/cmd/benchrun report

# bench-gate enforces the wall-clock thresholds alone, which is the quick check.
bench-gate:
	$(GO) run ./bench/cmd/benchrun gate

bench-micro:
	$(GO) test -run '^$$' -bench . -benchmem -count=1 $$($(GO) run ./bench/cmd/benchrun packages)

# bench-compare fails on an allocation regression against the committed
# baseline. Allocation counts are what CI can judge: they are the same number on
# any machine, where a nanosecond is not.
bench-compare:
	$(MAKE) bench-micro | $(GO) run ./bench/cmd/benchrun compare -threshold $(ALLOC_THRESHOLD)

bench-baseline:
	$(GO) run ./bench/cmd/benchrun baseline

clean:
	rm -f recall
	rm -rf "$${TMPDIR:-/tmp}/recall-bench"
