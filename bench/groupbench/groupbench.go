// Package groupbench measures reading turns through a single-agent
// archive.Group against reading the same store directly.
//
// It is a separate package from bench, not a file inside it: bench is
// imported by internal/archive's and internal/strip's own test files to
// generate a measurement corpus, so bench itself must never import either
// package back, or building those packages' tests would fail on the import
// cycle. Everything here that needs archive.Group lives on this side of that
// boundary instead.
package groupbench

import (
	"fmt"
	"os"
	"testing"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/repo"
	"github.com/mayberuk/recall/internal/schema"
	"github.com/mayberuk/recall/internal/strip"
)

// cmd/recall registers providers in its own init() because it is the one
// place that calls archive.Select or archive.Registered outside a test. This
// package is another: MeasureGroupAllocs is the only caller in the tree that
// opens a Group outside cmd/recall, so it registers claude-code the same way
// rather than depending on cmd/recall's init() having already run in the
// same process.
func init() {
	archive.Register(strip.ClaudeCode())
}

// GroupAllocs is what reading turns through a single-agent Group costs
// against reading the same store directly.
//
// Group.Turns returns the one store's slice untouched when the group holds a
// single store — the path every search takes today — so the two calls should
// allocate identically. A copy or a merge creeping onto that path would show
// up as Grouped exceeding Direct; every wall-clock gate would still pass on a
// copy small enough not to move the clock, which is why this is measured in
// allocations rather than time.
type GroupAllocs struct {
	Direct, Grouped float64
}

// Breached reports whether the group read allocated more than the direct
// read it is supposed to be a pass-through for.
func (a GroupAllocs) Breached() bool { return a.Grouped > a.Direct }

// MeasureGroupAllocs opens a single-agent group over the corpus at
// corpusRoot and compares Store.Turns against Group.Turns.
//
// dir is a fresh archive directory: the group has to be updated once before
// either read is timed, and reusing a directory another store already wrote
// to would measure an incremental read instead of the first one every search
// actually performs.
//
// OpenGroup resolves each store's root from its provider rather than from
// any Root a caller passes in, so CLAUDE_PROJECTS_DIR is how a single-agent
// group is pointed at a generated corpus instead of the operator's own
// session store; it is restored before this returns.
func MeasureGroupAllocs(corpusRoot, dir string) (GroupAllocs, error) {
	prior, hadPrior := os.LookupEnv("CLAUDE_PROJECTS_DIR")
	if err := os.Setenv("CLAUDE_PROJECTS_DIR", corpusRoot); err != nil {
		return GroupAllocs{}, fmt.Errorf("groupbench: cannot point the claude-code provider at the corpus: %w", err)
	}
	defer func() {
		if hadPrior {
			_ = os.Setenv("CLAUDE_PROJECTS_DIR", prior)
		} else {
			_ = os.Unsetenv("CLAUDE_PROJECTS_DIR")
		}
	}()

	g, err := archive.OpenGroup(
		archive.Selection{Agents: []schema.Agent{schema.AgentClaudeCode}},
		archive.Options{Dir: dir, Resolve: repo.New().Repo},
	)
	if err != nil {
		return GroupAllocs{}, fmt.Errorf("groupbench: OpenGroup: %w", err)
	}
	if _, err := g.Update(); err != nil {
		return GroupAllocs{}, fmt.Errorf("groupbench: Group.Update: %w", err)
	}
	if n := len(g.Stores()); n != 1 {
		return GroupAllocs{}, fmt.Errorf("groupbench: group opened %d store(s), want exactly 1 for a single-agent measurement", n)
	}
	direct := g.Stores()[0]

	var readErr error
	directAllocs := testing.AllocsPerRun(8, func() {
		if _, err := direct.Turns(); err != nil {
			readErr = err
		}
	})
	if readErr != nil {
		return GroupAllocs{}, fmt.Errorf("groupbench: Store.Turns: %w", readErr)
	}
	groupedAllocs := testing.AllocsPerRun(8, func() {
		if _, err := g.Turns(); err != nil {
			readErr = err
		}
	})
	if readErr != nil {
		return GroupAllocs{}, fmt.Errorf("groupbench: Group.Turns: %w", readErr)
	}
	return GroupAllocs{Direct: directAllocs, Grouped: groupedAllocs}, nil
}
