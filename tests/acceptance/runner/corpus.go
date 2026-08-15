package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// The harness classifies tiers itself instead of importing internal/strip. A premise check that
// asked the code under test whether the corpus still supports the case would prove nothing.
// Method mirrors logs/acceptance-queries.md, which is where the pinned counts came from.

type queryFacts struct {
	Query string `json:"query"`

	ConvBySession map[string]int `json:"conv_by_session"`
	ConvByProject map[string]int `json:"conv_by_project"`
	ConvByCwd     map[string]int `json:"conv_by_cwd"`
	ConvTotal     int            `json:"conv_total"`

	ResultBySession map[string]int `json:"result_by_session"`
	ResultByProject map[string]int `json:"result_by_project"`
	ResultByCwd     map[string]int `json:"result_by_cwd"`
	ResultTotal     int            `json:"result_total"`

	// session -> cwd -> hits. Repo scope is a property of the cwd a record was written from, so
	// "does this session have hits inside repo X" is only answerable at this granularity.
	ConvSessionCwd   map[string]map[string]int `json:"-"`
	ResultSessionCwd map[string]map[string]int `json:"-"`

	// Files a session's conversation-tier matches live in; ≥2 is the cross-file duplication
	// a7 needs in order to discriminate at all.
	ConvFilesBySession map[string]map[string]struct{} `json:"-"`
	// Per session, each matching record uuid and the files it was found in. A uuid in two
	// files is one logical turn a naive count reports twice.
	ConvUUIDFiles map[string]map[string]map[string]struct{} `json:"-"`
	// Occurrences in the first copy seen of each uuid. Copies are byte-identical, so summing
	// this is the hit count a deduplicating implementation should report.
	ConvUUIDHits map[string]map[string]int `json:"-"`
	// Raw match count including copies of one uuid in several files.
	ConvCopiesBySession map[string]int `json:"-"`
}

func newQueryFacts(q string) *queryFacts {
	return &queryFacts{
		Query:               q,
		ConvBySession:       map[string]int{},
		ConvByProject:       map[string]int{},
		ConvByCwd:           map[string]int{},
		ResultBySession:     map[string]int{},
		ResultByProject:     map[string]int{},
		ResultByCwd:         map[string]int{},
		ConvFilesBySession:  map[string]map[string]struct{}{},
		ConvUUIDFiles:       map[string]map[string]map[string]struct{}{},
		ConvUUIDHits:        map[string]map[string]int{},
		ConvCopiesBySession: map[string]int{},
		ConvSessionCwd:      map[string]map[string]int{},
		ResultSessionCwd:    map[string]map[string]int{},
	}
}

// sessionsInRepo returns the sessions with hits in the named tier written from a cwd whose repo
// identity matches, with their hit counts.
func (q *queryFacts) sessionsInRepo(rr *repoResolver, want repoIdentity, conversation bool) map[string]int {
	src := q.ConvSessionCwd
	if !conversation {
		src = q.ResultSessionCwd
	}
	out := map[string]int{}
	for session, cwds := range src {
		for cwd, n := range cwds {
			if sameRepo(rr.resolve(cwd), want) {
				out[session] += n
			}
		}
	}
	return out
}

func (q *queryFacts) repoSpread(rr *repoResolver, conversation bool) map[string]int {
	src := q.ConvByCwd
	if !conversation {
		src = q.ResultByCwd
	}
	out := map[string]int{}
	for cwd, n := range src {
		out[repoKey(rr.resolve(cwd))] += n
	}
	return out
}

func sameRepo(a, b repoIdentity) bool { return a.Kind == b.Kind && a.Value == b.Value }

func repoKey(id repoIdentity) string {
	switch id.Kind {
	case "remote":
		return "remote:" + id.Name
	case "no-remote":
		return "no-remote:" + id.Value
	default:
		return "outside-any-repo"
	}
}

// dupUUIDs reports, for one session, how many matching record uuids were found in more than one
// file and how many redundant copies that is.
func (q *queryFacts) dupUUIDs(session string) (uuids, copies int) {
	for _, files := range q.ConvUUIDFiles[session] {
		if len(files) > 1 {
			uuids++
			copies += len(files) - 1
		}
	}
	return uuids, copies
}

func (q *queryFacts) distinctUUIDs(session string) int { return len(q.ConvUUIDFiles[session]) }

// dedupedHits is what the session's hit count should be once cross-file copies collapse;
// ConvBySession is the same figure with every copy counted.
func (q *queryFacts) dedupedHits(session string) int {
	total := 0
	for _, n := range q.ConvUUIDHits[session] {
		total += n
	}
	return total
}

type sessionFacts struct {
	ID                string              `json:"id"`
	ConversationBytes int64               `json:"conversation_bytes"`
	Files             map[string]struct{} `json:"-"`
	Cwds              map[string]struct{} `json:"-"`
	Projects          map[string]struct{} `json:"-"`
}

type corpusFacts struct {
	Root      string
	FileCount int
	LineCount int64
	// Corpus-wide cross-file duplication, independent of any query: how many (session, uuid)
	// pairs have their record written into more than one file, and how many copies that is.
	RecordPairs     int
	DuplicatedPairs int
	RedundantCopies int
	pairFirstFile   map[string]string
	pairAllFiles    map[string]map[string]struct{}
	Sessions        map[string]*sessionFacts
	CwdRecords      map[string]int
	CwdSessions     map[string]map[string]struct{}
	RelocatedCwds   map[string]string
	Queries         map[string]*queryFacts
}

func (c *corpusFacts) session(id string) *sessionFacts {
	s := c.Sessions[id]
	if s == nil {
		s = &sessionFacts{
			ID:       id,
			Files:    map[string]struct{}{},
			Cwds:     map[string]struct{}{},
			Projects: map[string]struct{}{},
		}
		c.Sessions[id] = s
	}
	return s
}

// largestSession returns the session id with the most conversation-tier bytes.
func (c *corpusFacts) largestSession() (string, int64) {
	ids := make([]string, 0, len(c.Sessions))
	for id := range c.Sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	best, bestBytes := "", int64(-1)
	for _, id := range ids {
		if b := c.Sessions[id].ConversationBytes; b > bestBytes {
			best, bestBytes = id, b
		}
	}
	return best, bestBytes
}

func scanCorpus(root string, queries []string) (*corpusFacts, error) {
	c := &corpusFacts{
		Root:          root,
		Sessions:      map[string]*sessionFacts{},
		CwdRecords:    map[string]int{},
		CwdSessions:   map[string]map[string]struct{}{},
		RelocatedCwds: map[string]string{},
		Queries:       map[string]*queryFacts{},

		pairFirstFile: map[string]string{},
		pairAllFiles:  map[string]map[string]struct{}{},
	}
	lowered := make([]string, len(queries))
	for i, q := range queries {
		c.Queries[q] = newQueryFacts(q)
		lowered[i] = strings.ToLower(q)
	}

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	c.FileCount = len(files)

	for _, path := range files {
		project := projectDirOf(root, path)
		if err := c.scanFile(path, project, queries, lowered); err != nil {
			return nil, fmt.Errorf("scan %s: %w", path, err)
		}
	}
	return c, nil
}

// notePair tracks cross-file duplication of one logical record. The key is (session, uuid), not
// uuid alone: a fork carries a record into a new session keeping its uuid, and collapsing on uuid
// alone would delete the turn from the other session entirely.
func (c *corpusFacts) notePair(session, uuid, path string) {
	if session == "" || uuid == "" {
		return
	}
	key := session + "\x00" + uuid
	first, seen := c.pairFirstFile[key]
	if !seen {
		c.pairFirstFile[key] = path
		c.RecordPairs++
		return
	}
	if first == path {
		return
	}
	// Count distinct files, not repeat sightings: an append-only file can carry the same record
	// twice, and that is not a second copy of it.
	files := c.pairAllFiles[key]
	if files == nil {
		files = map[string]struct{}{first: {}}
		c.pairAllFiles[key] = files
		c.DuplicatedPairs++
	}
	if _, dup := files[path]; dup {
		return
	}
	files[path] = struct{}{}
	c.RedundantCopies++
}

func projectDirOf(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "?"
	}
	return strings.SplitN(rel, string(filepath.Separator), 2)[0]
}

func (c *corpusFacts) scanFile(path, project string, queries, lowered []string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		c.LineCount++
		rec := gjson.ParseBytes(line)

		recType := rec.Get("type").String()
		sessionID := rec.Get("sessionId").String()
		if cwd := rec.Get("cwd").String(); cwd != "" {
			c.CwdRecords[cwd]++
			if c.CwdSessions[cwd] == nil {
				c.CwdSessions[cwd] = map[string]struct{}{}
			}
			c.CwdSessions[cwd][sessionID] = struct{}{}
		}
		if recType == "relocated" {
			if rc := rec.Get("relocatedCwd").String(); rc != "" && sessionID != "" {
				c.RelocatedCwds[sessionID] = rc
			}
		}
		if recType != "user" && recType != "assistant" {
			continue
		}
		if sessionID == "" {
			continue
		}
		s := c.session(sessionID)
		s.Files[path] = struct{}{}
		s.Projects[project] = struct{}{}
		if cwd := rec.Get("cwd").String(); cwd != "" {
			s.Cwds[cwd] = struct{}{}
		}
		uuid := rec.Get("uuid").String()
		cwd := rec.Get("cwd").String()
		c.notePair(sessionID, uuid, path)

		conv, result := splitTiers(rec.Get("message.content"))
		s.ConversationBytes += int64(len(conv))

		c.tally(queries, lowered, conv, project, cwd, sessionID, path, uuid, true)
		c.tally(queries, lowered, result, project, cwd, sessionID, path, uuid, false)
	}
	return sc.Err()
}

// splitTiers returns concatenated conversation-tier text and tool-result-tier text for one
// record. tool_use input is invocation tier and is deliberately dropped: neither a1 nor a6
// reasons about it, and including it would blur the two tiers the cases separate.
func splitTiers(content gjson.Result) (conv, result string) {
	if !content.Exists() {
		return "", ""
	}
	if content.Type == gjson.String {
		return content.String(), ""
	}
	var cb, rb strings.Builder
	content.ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").String() {
		case "text":
			cb.WriteString(block.Get("text").String())
			cb.WriteByte('\n')
		case "thinking":
			cb.WriteString(block.Get("thinking").String())
			cb.WriteByte('\n')
		case "tool_result":
			rc := block.Get("content")
			if rc.Type == gjson.String {
				rb.WriteString(rc.String())
				rb.WriteByte('\n')
				return true
			}
			rc.ForEach(func(_, inner gjson.Result) bool {
				if inner.Get("type").String() == "text" {
					rb.WriteString(inner.Get("text").String())
					rb.WriteByte('\n')
				}
				return true
			})
		}
		return true
	})
	return cb.String(), rb.String()
}

func (c *corpusFacts) tally(queries, lowered []string, text, project, cwd, session, path, uuid string, conversation bool) {
	if text == "" {
		return
	}
	low := strings.ToLower(text)
	for i, q := range queries {
		n := strings.Count(low, lowered[i])
		if n == 0 {
			continue
		}
		qf := c.Queries[q]
		if !conversation {
			qf.ResultBySession[session] += n
			qf.ResultByProject[project] += n
			qf.ResultByCwd[cwd] += n
			qf.ResultTotal += n
			if qf.ResultSessionCwd[session] == nil {
				qf.ResultSessionCwd[session] = map[string]int{}
			}
			qf.ResultSessionCwd[session][cwd] += n
			continue
		}
		qf.ConvBySession[session] += n
		qf.ConvByProject[project] += n
		qf.ConvByCwd[cwd] += n
		qf.ConvTotal += n
		if qf.ConvSessionCwd[session] == nil {
			qf.ConvSessionCwd[session] = map[string]int{}
		}
		qf.ConvSessionCwd[session][cwd] += n
		qf.ConvCopiesBySession[session] += n
		if qf.ConvFilesBySession[session] == nil {
			qf.ConvFilesBySession[session] = map[string]struct{}{}
			qf.ConvUUIDFiles[session] = map[string]map[string]struct{}{}
			qf.ConvUUIDHits[session] = map[string]int{}
		}
		qf.ConvFilesBySession[session][path] = struct{}{}
		if uuid != "" {
			if qf.ConvUUIDFiles[session][uuid] == nil {
				qf.ConvUUIDFiles[session][uuid] = map[string]struct{}{}
				qf.ConvUUIDHits[session][uuid] = n
			}
			qf.ConvUUIDFiles[session][uuid][path] = struct{}{}
		}
	}
}
