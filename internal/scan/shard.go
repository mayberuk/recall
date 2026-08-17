package scan

import (
	"os"
	"runtime"
	"strconv"
	"sync"

	"github.com/mayberuk/recall/internal/schema"
)

// RangeFloorEnv overrides how many turns a goroutine's slice of the corpus must
// hold to be worth cutting. It exists for a test that drives the built binary
// rather than calling into this package: the differential harness compares this
// binary against a pre-sharding one, and on a corpus small enough to build twice
// per run it would otherwise compare two single-pass scans and prove nothing
// about the concurrent path.
const RangeFloorEnv = "RECALL_TURNS_PER_RANGE"

// minShardTurns is the smallest slice of the corpus worth handing to its own
// goroutine. Below it the scheduling costs more than the scan saves, and a
// small corpus is already fast. Tests in this package shrink it directly;
// nothing else writes it.
var minShardTurns = rangeFloor(os.Getenv(RangeFloorEnv), 2048)

// rangeFloor takes the override only when it parses to a usable count, so a
// misspelt value leaves the measured default in place rather than turning the
// scan sequential behind the caller's back.
func rangeFloor(env string, dflt int) int {
	if n, err := strconv.Atoi(env); err == nil && n > 0 {
		return n
	}
	return dflt
}

// shard is one worker's slice of the corpus and everything it found there.
//
// Every field is per-shard state that the merge folds together. Nothing here is
// shared between goroutines: each worker forks the matcher, because the matcher
// carries a scratch slice it rewrites for every turn.
type shard struct {
	// need is the term count this shard settled on, and hits are its turns
	// carrying exactly that many. below are its turns carrying one fewer. The
	// merge needs both, because a shard that found nothing better than two terms
	// contributes its hits to the global below when another shard found three.
	need         int
	hits         []schema.Hit
	below        []schema.Hit
	carried      []bool
	belowCarried []bool

	turns    int
	scanned  int
	sessions map[string]*sessionState
}

// sessionsPerShard sizes a range's session map. Sizing every range for the whole
// corpus was allocating a 128-bucket map per goroutine; a range holds a slice of
// the corpus and a session's turns are adjacent, so it sees far fewer than that
// and the map grows if a corpus proves otherwise.
const sessionsPerShard = 16

// shardCount is how many ranges the corpus is worth cutting into.
func shardCount(n int) int {
	if n == 0 {
		return 0
	}
	return max(min(runtime.GOMAXPROCS(0), n/minShardTurns), 1)
}

// scanShards runs the walk over every range and returns them in input order.
//
// Ranges are contiguous rather than strided, for two reasons: they concatenate
// back into input order, and a session's turns are adjacent, so most sessions
// stay whole inside one range and the merge has less to reconcile.
//
// A corpus too small to cut runs inline on the caller's goroutine and reuses the
// caller's matcher, so it pays for none of this.
func scanShards(turns []schema.Turn, q Query, m *matcher, want map[schema.Tier]bool) []shard {
	workers := shardCount(len(turns))
	out := make([]shard, workers)
	switch workers {
	case 0:
		return out
	case 1:
		out[0] = scanRange(turns, q, m, want)
		return out
	}

	per := len(turns) / workers
	var wg sync.WaitGroup
	for i := range workers {
		lo := i * per
		hi := lo + per
		if i == workers-1 {
			hi = len(turns)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A matcher per range, because the one thing a matcher holds that
			// is not read-only is the scratch slice mark rewrites once per turn.
			local := m.fork()
			out[i] = scanRange(turns[lo:hi], q, &local, want)
		}()
	}
	wg.Wait()
	return out
}

// overRanges runs fn over each contiguous range of [0,n) concurrently and
// returns one result per range, in input order.
//
// scanShards keeps its own copy of this loop rather than calling it: the hit
// path has to hand each range a forked matcher, and taking the single-range case
// without one is what keeps a corpus too small to cut as cheap as it was before
// there was a merge at all.
func overRanges[T any](n int, fn func(lo, hi int) T) []T {
	workers := shardCount(n)
	switch workers {
	case 0:
		return nil
	case 1:
		return []T{fn(0, n)}
	}

	out := make([]T, workers)
	per := n / workers
	var wg sync.WaitGroup
	for i := range workers {
		lo := i * per
		hi := lo + per
		if i == workers-1 {
			hi = n
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = fn(lo, hi)
		}()
	}
	wg.Wait()
	return out
}

// scanRange is the whole search, over one slice of the corpus. Run over the
// whole corpus it is the original single-pass algorithm unchanged.
func scanRange(turns []schema.Turn, q Query, m *matcher, want map[schema.Tier]bool) shard {
	sh := shard{sessions: make(map[string]*sessionState, sessionsPerShard)}

	lastID := ""
	var last *sessionState

	// need is the term count a turn must carry to be kept, and it only ever
	// rises: the first turn carrying more terms than anything before it makes
	// every hit collected so far obsolete in one comparison, which is what
	// turns "best partial match" into a single pass rather than one pass per
	// relaxation level.
	need := 1
	if m.strict {
		need = len(m.terms)
	}
	carried := make([]bool, len(m.terms))

	// A long turn carries more query terms by carrying more words, so keeping
	// only the very best count hands a degraded query to whatever injected
	// 20 KB summary happened to contain everything. One level of slack lets the
	// short, dense turns compete, and ranking then decides between them. It
	// applies only when the query could not be met in full: a satisfiable query
	// keeps the strict all-terms result it has always had.
	var below []schema.Hit
	belowCarried := make([]bool, len(m.terms))

	var buf []byte
	var spans []span
	for i := range turns {
		turn := &turns[i]
		sh.turns++

		if turn.Session != lastID || last == nil {
			lastID = turn.Session
			if last = sh.sessions[lastID]; last == nil {
				last = &sessionState{}
				sh.sessions[lastID] = last
			}
		}
		if turn.Tier == schema.TierConversation {
			last.conversation++
		}
		if !want[turn.Tier] || (q.Keep != nil && !q.Keep(turn)) {
			continue
		}
		last.scanned = true
		sh.scanned++

		if len(m.terms) == 0 {
			continue
		}
		buf = fold(buf, turn.Text)
		if m.excluded(buf) {
			continue
		}
		found := m.mark(buf, need-1)
		if found < need-1 || found == 0 {
			continue
		}
		switch {
		case found == need-1:
			// Held in case the query turns out not to be satisfiable at all.
			// Once one turn has carried every term it never will be used, and
			// holding it costs a hit per occurrence for the rest of the walk.
			if need < len(m.terms) {
				below = appendHits(below, turn, m, &spans, buf, found, belowCarried)
			}
			continue
		case found > need:
			if found == need+1 {
				below, belowCarried = sh.hits, carried
			} else {
				below, belowCarried = nil, make([]bool, len(m.terms))
			}
			need = found
			sh.hits, carried = nil, make([]bool, len(m.terms))
			if need == len(m.terms) {
				below, belowCarried = nil, make([]bool, len(m.terms))
			}
		}
		for j := range carried {
			carried[j] = carried[j] || m.carried[j]
		}
		spans = m.collect(spans, buf)
		for _, s := range spans {
			sh.hits = append(sh.hits, schema.Hit{
				Session: turn.Session,
				UUID:    turn.UUID,
				TS:      turn.TS,
				Tier:    turn.Tier,
				Author:  turn.Author,
				Agent:   turn.Agent,
				Repo:    turn.Repo,
				Branch:  turn.Branch,
				Offset:  s.offset,
				Length:  s.length,
				Match:   s.kind,
				Terms:   found,
				Text:    turn.Text,
			})
		}
	}

	sh.need, sh.below, sh.carried, sh.belowCarried = need, below, carried, belowCarried
	return sh
}

// mergeShards folds the shards back into one answer.
//
// The rule rests on the walk being order-independent in what it ends up with:
// need only rises, and every rise clears the hits below it, so a completed walk
// holds exactly the turns carrying the most terms anything in its range carried,
// plus the turns one level below when the query was not satisfiable. Two shards
// that saw the same turns in a different order would finish in the same state,
// which is what makes cutting the corpus into ranges safe.
//
// So the global answer is the best of the local bests. A shard that reached the
// global best contributes its hits to the hits and its below to the below; a
// shard that reached exactly one less contributes its hits to the below, because
// that is the same level; anything further down contributed nothing a single pass
// would have kept either.
func mergeShards(shards []shard, res *Result, terms int) merged {
	var out merged
	best := 0
	for i := range shards {
		if len(shards[i].hits) > 0 && shards[i].need > best {
			best = shards[i].need
		}
	}
	out.need = best

	// One shard is the whole corpus, so its state is already the answer. Taking
	// it rather than copying it into fresh containers is what keeps a corpus too
	// small to shard exactly as cheap as it was before there was a merge at all:
	// the copy is a second allocation of every hit, and a search that matches
	// often has more hit bytes than corpus bytes.
	if len(shards) == 1 {
		return single(&shards[0], res, out, terms)
	}
	out.carried = make([]bool, terms)
	out.belowCarried = make([]bool, terms)

	sessions := make(map[string]*sessionState, 128)
	for i := range shards {
		sh := &shards[i]
		res.Turns += sh.turns
		res.TurnsScanned += sh.scanned
		for id, state := range sh.sessions {
			merged := sessions[id]
			if merged == nil {
				merged = &sessionState{}
				sessions[id] = merged
			}
			merged.conversation += state.conversation
			merged.scanned = merged.scanned || state.scanned
		}
		if best == 0 {
			continue
		}

		// Shards are visited in input order and each covers a contiguous range,
		// so appending in this order is appending in input order — which is the
		// order a single pass produced and the order ranking is handed.
		switch sh.need {
		case best:
			out.hits = append(out.hits, sh.hits...)
			or(out.carried, sh.carried)
			if best < terms {
				out.below = append(out.below, sh.below...)
				or(out.belowCarried, sh.belowCarried)
			}
		case best - 1:
			if best < terms {
				out.below = append(out.below, sh.hits...)
				or(out.belowCarried, sh.carried)
			}
		}
	}

	res.Sessions = len(sessions)
	res.TurnsBySession = make(map[string]int, len(sessions))
	for id, state := range sessions {
		res.TurnsBySession[id] = state.conversation
		if state.scanned {
			res.SessionsScanned++
		}
	}
	return out
}

// single is mergeShards for the unsharded corpus, which owns its results outright.
func single(sh *shard, res *Result, out merged, terms int) merged {
	res.Turns = sh.turns
	res.TurnsScanned = sh.scanned
	res.Sessions = len(sh.sessions)
	res.TurnsBySession = make(map[string]int, len(sh.sessions))
	for id, state := range sh.sessions {
		res.TurnsBySession[id] = state.conversation
		if state.scanned {
			res.SessionsScanned++
		}
	}
	if out.need == 0 {
		return out
	}
	out.hits, out.carried = sh.hits, sh.carried
	if out.need < terms {
		out.below, out.belowCarried = sh.below, sh.belowCarried
	}
	return out
}

// merged is what the shards add up to: the hits at the best term count anything
// reached, the hits one level below it, and which query terms each set carries.
type merged struct {
	hits, below           []schema.Hit
	carried, belowCarried []bool
	need                  int
}

func or(dst, src []bool) {
	for i := range dst {
		if i < len(src) && src[i] {
			dst[i] = true
		}
	}
}
