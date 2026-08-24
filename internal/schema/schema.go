// Package schema is the record and type vocabulary shared by strip, repo,
// archive, scan, and rank.
//
// It holds no logic, only types, so any package that needs the shape of a
// turn or a hit can depend on schema without importing whichever package
// produces one — the reason scan and rank never import strip or archive
// directly.
package schema

// Tier is what kind of text a turn carries. Conversation is searched by default;
// the other two are opt-in and every search declares which it covered.
type Tier string

const (
	TierConversation Tier = "conversation"
	TierInvocation   Tier = "invocation"
	TierResult       Tier = "result"
)

// Author attributes a turn. Human means typed by the operator, which is
// promptSource == "typed" plus the <command-args> payload of slash-command
// records — content-shape detection was refuted by spike and overcounts 5x.
// System covers user-role records that are not typed: slash-command wrappers,
// local-command-stdout, compact continuation summaries, sdk prompts sent to
// subagents. They stay searchable but are never attributed to the operator.
type Author string

const (
	AuthorHuman     Author = "human"
	AuthorAssistant Author = "assistant"
	AuthorAgent     Author = "agent"
	AuthorSystem    Author = "system"
)

// Turn is one stripped turn, the archive record. Nothing downstream of strip
// parses raw JSONL.
type Turn struct {
	Session string
	UUID    string // dedup key; 17,659 records exist in more than one file
	TS      string // RFC3339
	Tier    Tier
	Author  Author
	Agent   string // agent name when known; agentId is 43-46% present
	Origin  Agent  // whose store it was read from; Agent above is a subagent's own name
	Repo    string // resolved by internal/repo, not by internal/strip
	Branch  string
	CWD     string // kept raw: repo resolution may improve later
	Text    string
}

// MatchKind is how a match sits in the surrounding words. Substring matching is
// kept — a term in the middle of an identifier is a real hit, and dropping it
// would be the false negative the tool exists to prevent — so the distinction
// is carried here and spent on ranking instead of on filtering.
type MatchKind string

const (
	MatchWord   MatchKind = "word"   // whole word: "build" in "the build"
	MatchPrefix MatchKind = "prefix" // starts a word: "wallet" in "wallets"
	MatchInside MatchKind = "inside" // interior: "no" in "know"
)

// Hit is one match inside a turn. Offset and Length locate the match within
// Text so a renderer can highlight without re-searching. Terms is how many of
// the query's terms the turn carries, which is what lets a query degrade to its
// best partial match instead of to nothing.
type Hit struct {
	Session string
	UUID    string
	TS      string
	Tier    Tier
	Author  Author
	Agent   string
	Repo    string
	Branch  string
	Offset  int
	Length  int
	Match   MatchKind
	Terms   int
	Text    string
}
