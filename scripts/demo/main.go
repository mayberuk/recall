// Command demo writes the session corpus the README's examples are taken from.
//
// The examples have to be reproducible by a reader, and the two obvious sources
// are not: a generated corpus plants unreadable needles on purpose, so nothing
// it answers reads like a question anyone would ask, and the author's own store
// cannot be published. This writes a small store of plausible sessions instead,
// fixed rather than seeded, so `scripts/demo.sh` prints the same bytes on any
// machine and every block in the README can be checked against it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// checkout is one working copy on disk. Two checkouts sharing a remote is the
// property recall exists for, so the corpus has to contain a pair.
type checkout struct {
	dir    string
	remote string
	branch string
}

// session is one transcript: turns alternate user, assistant, user, assistant.
type session struct {
	id       string
	checkout *checkout
	day      string
	turns    []string
}

var (
	payments     = &checkout{dir: "src/payments", remote: "git@github.com:acme/payments.git", branch: "main"}
	paymentsFix  = &checkout{dir: "src/payments-hotfix", remote: "git@github.com:acme/payments.git", branch: "hotfix/dedupe"}
	web          = &checkout{dir: "src/web", remote: "git@github.com:acme/web.git", branch: "main"}
	infra        = &checkout{dir: "src/infra", remote: "git@github.com:acme/infra.git", branch: "main"}
	allCheckouts = []*checkout{payments, paymentsFix, web, infra}
)

// sessions are ordered oldest first, which is the order `recall when` reports.
var sessions = []session{
	{
		id: "1a9c4f02-7b31-4e58-9d06-2f8a51c37e40", checkout: payments, day: "2026-05-12",
		turns: []string{
			"We're getting hammered on the charge endpoint. Should we go with a sliding window rate limiter or a token bucket?",
			"Token bucket, and the deciding factor is burst tolerance rather than accuracy. A sliding window gives you an exact count over the trailing minute, but it refuses the whole burst at the boundary — and our traffic is bursty by construction, because merchants batch their captures on the hour. A token bucket at 100 requests per second with a burst of 500 absorbs that batch and still holds the long-run average. The cost is that a sustained attacker gets 500 free requests before throttling engages, which the WAF already covers.",
			"What do we key the bucket on?",
			"Merchant id, not IP. IP keying punishes merchants behind a shared NAT and does nothing against a distributed caller. Merchant id is also the unit we bill and rate-limit contractually, so the limit we enforce is the limit in the contract.",
		},
	},
	{
		id: "6e2b8d15-4c70-4a93-b8e1-05d7c2914f63", checkout: paymentsFix, day: "2026-06-03",
		turns: []string{
			"Duplicate charges again. Two merchants hit the same idempotency key and one of them saw the other's receipt. How do we scope these keys?",
			"Scope the idempotency key per merchant, never globally. The key is a string the client chooses, so a global namespace means any two clients picking `order-1` collide — and the collision does not fail loudly, it returns the first caller's stored response to the second, which is exactly the cross-merchant receipt leak you just saw. Make the storage key the pair (merchant_id, idempotency_key) and the collision becomes impossible rather than unlikely.",
			"Do we need to migrate the existing rows?",
			"Yes, and it has to happen before the code ships or the first request after deploy reads a row it cannot interpret. The existing primary key is the bare idempotency_key, so the migration is: add merchant_id to the table, backfill it from the linked charge, then swap the unique constraint to the composite. Keep the old constraint until the backfill reports zero nulls — dropping it first turns a partial backfill into a silent duplicate.",
			"What TTL should the keys carry?",
			"24 hours. Long enough that a client retrying after a network partition still gets its stored response, short enough that the table does not grow without bound. Anything longer and you are storing responses nobody will ever ask for again.",
		},
	},
	{
		id: "c47f0a83-9e26-4b15-a730-6c81d4092be5", checkout: web, day: "2026-06-18",
		turns: []string{
			"The checkout test passes locally and fails in CI about one run in four. Any idea?",
			"It is a timezone assumption, not a race. The test builds an expected date with `new Date(...)` and compares it against a value the server rendered, and the CI container runs UTC while your laptop runs UTC-5. Between 19:00 and midnight local, the two disagree about which day it is — which is roughly a quarter of the runs, and matches the failure rate you are seeing. Pin TZ in the test rather than in the container, so the test states its own assumption.",
			"Why not just set TZ=UTC on the CI job?",
			"Because it hides the bug rather than fixing it. The same assumption is in the production formatter, and a CI job pinned to UTC would go green while a customer in Auckland still sees yesterday's date on their receipt.",
		},
	},
	{
		id: "9d51e6c7-3a84-4f20-b96d-71e0a385c2f9", checkout: infra, day: "2026-07-02",
		turns: []string{
			"Postgres is refusing connections under load. Do we raise max_connections?",
			"No — raise it and you trade connection errors for memory exhaustion, because each backend costs about 10 MB of work_mem before it does anything useful. The number to change is the pool size in the application, downward. You have eight application pods each holding a pool of 50, which asks Postgres for 400 backends against a max_connections of 200. Drop the per-pod pool to 20 and the arithmetic works out at 160, with headroom for the migration job.",
			"Won't a smaller pool just move the queue into the app?",
			"It does, and that is the point: a request queued in the application is a request you can see, time out, and shed. A connection refused at the database is an error you discover from a customer.",
		},
	},
	{
		id: "3b8a2c94-6d17-4e05-8f2b-c091d67a4e38", checkout: payments, day: "2026-07-21",
		turns: []string{
			"How long should we back off between retries on the acquirer API?",
			"Exponential with full jitter, capped at 30 seconds, and give up after five attempts. The cap matters more than the base: without it the fifth retry lands minutes later, by which point the customer has already pressed the button again and you are processing two charges instead of one. Full jitter rather than equal jitter because our retries are synchronised by the same hourly batch that drives the rate limiting, so retry storms are the failure mode to design against.",
			"Should a timeout be retried the same way as a 500?",
			"No. A 500 is safe to retry because the acquirer told you it did nothing. A timeout is ambiguous — the charge may well have succeeded — so a retry there needs the idempotency key to be safe, and without one it must not be retried at all.",
		},
	},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: demo <dir>")
		os.Exit(2)
	}
	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fail(err)
	}
	for _, c := range allCheckouts {
		if err := initCheckout(root, c); err != nil {
			fail(err)
		}
	}
	projects := filepath.Join(root, "projects")
	for _, s := range sessions {
		if err := writeSession(root, projects, s); err != nil {
			fail(err)
		}
	}
	fmt.Println(projects)
}

// initCheckout makes a working copy that resolves to a shared repo identity.
// The remote is what recall keys a repo on, so the two payments checkouts must
// carry the same one and nothing else about them need match.
func initCheckout(root string, c *checkout) error {
	dir := filepath.Join(root, c.dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch", c.branch},
		{"remote", "add", "origin", c.remote},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
		}
	}
	return nil
}

// writeSession lays one transcript down where Claude Code would have: under a
// project directory named for the cwd with every separator replaced by a dash.
func writeSession(root, projects string, s session) error {
	cwd := filepath.Join(root, s.checkout.dir)
	dir := filepath.Join(projects, strings.ReplaceAll(cwd, string(os.PathSeparator), "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, s.id+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	start, err := time.Parse(time.RFC3339, s.day+"T09:00:00Z")
	if err != nil {
		return err
	}
	var last time.Time
	var parent string
	for i, text := range s.turns {
		uuid := fmt.Sprintf("%s-%04d-4000-8000-%012d", s.id[:8], i, i)
		last = start.Add(time.Duration(i) * 4 * time.Minute)
		ts := last.Format("2006-01-02T15:04:05.000Z")
		rec := map[string]any{
			"parentUuid": nullable(parent), "isSidechain": false,
			"uuid": uuid, "timestamp": ts, "cwd": cwd, "sessionId": s.id,
			"version": "2.1.231", "gitBranch": s.checkout.branch, "userType": "external",
		}
		if i%2 == 0 {
			rec["type"] = "user"
			rec["promptSource"] = "typed"
			rec["origin"] = map[string]any{"kind": "human"}
			rec["entrypoint"] = "cli"
			rec["message"] = map[string]any{"role": "user", "content": text}
		} else {
			rec["type"] = "assistant"
			rec["message"] = map[string]any{
				"role": "assistant", "model": "claude-opus-5",
				"id": "msg_" + uuid, "type": "message",
				"content": []any{map[string]any{"type": "text", "text": text}},
				"usage": map[string]any{
					"input_tokens": 4, "output_tokens": len(text) / 4,
					"cache_creation_input_tokens": 1200, "cache_read_input_tokens": 900,
					"service_tier": "standard",
				},
			}
		}
		if err := enc.Encode(rec); err != nil {
			return err
		}
		parent = uuid
	}
	// A transcript's mtime is what recall reads as the live boundary, so a file
	// written today carrying turns from May would be reported as months of skew.
	// Real stores do not look like that; the demo should not either.
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, last, last)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "demo:", err)
	os.Exit(1)
}
