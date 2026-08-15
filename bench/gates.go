// Package bench is recall's measurement harness.
//
// It holds the wall-clock thresholds the no-index architecture rests on, the
// generated corpus every measurement runs against, and the scenarios that time
// the built binary. Measuring against a generated corpus rather than the
// author's session store is what makes a number here comparable with one taken
// on another machine, or a year from now.
package bench

import "time"

// Gate is one wall-clock threshold. A breach is a failure and the remedy is
// never an index: the architecture has no index because linear scanning
// measured fast, so the measurement is the load-bearing part.
type Gate struct {
	Name  string
	Limit time.Duration
}

// The gates, previously spelled out in three separate package test files. They
// are stated once here so a threshold cannot be relaxed in one place and left
// standing in another.
var (
	FindConversation   = Gate{"find, conversation tier", 250 * time.Millisecond}
	FindAllTiers       = Gate{"find, all tiers", 1200 * time.Millisecond}
	StripCold          = Gate{"cold strip of the whole corpus", 4 * time.Second}
	ArchiveIncremental = Gate{"incremental archive update", 1500 * time.Millisecond}
)

// ArchiveCold is the same threshold as the cold strip, under the name it is
// enforced by: building the archive is a cold strip plus the writing, and the
// budget for the pair is what a caller waits through on first run.
var ArchiveCold = Gate{"cold archive build", StripCold.Limit}

// The archive load budgets are the find gates minus the scan they leave room
// for: a find that spends its whole budget reading the archive has nothing left
// to search with. The subtracted figures are the measured scan cost over the
// author's corpus, 54.6 ms conversation and 307.7 ms all tiers.
var (
	LoadConversation = Gate{"archive load, conversation tier", FindConversation.Limit - 55*time.Millisecond}
	LoadAllTiers     = Gate{"archive load, all tiers", FindAllTiers.Limit - 310*time.Millisecond}
)

// Gates is every threshold `make bench-gate` enforces, in the order it reports
// them.
func Gates() []Gate {
	return []Gate{
		FindConversation, FindAllTiers, StripCold,
		ArchiveCold, ArchiveIncremental, LoadConversation, LoadAllTiers,
	}
}
