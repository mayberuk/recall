package jsonl

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Header is every top-level field of a record, read in one pass. HasPromptSource
// is separate because presence is itself the signal: promptSource absent means
// the turn was machine-generated, not that the value is empty.
type Header struct {
	Type            string
	UUID            string
	SessionID       string
	Timestamp       string
	CWD             string
	RelocatedCWD    string
	GitBranch       string
	AgentID         string
	PromptSource    string
	HasPromptSource bool
	IsSidechain     bool
}

// Header reads every field the accessors expose in a single scan.
//
// Why not call the accessors: gjson answers one path per scan and proves a field
// absent only by scanning the whole record, which is the common case here —
// promptSource, agentId and relocatedCwd are absent far more often than present,
// and every field sits after the message body in key order. Nine accessor calls
// measured 4.5 s over the 1.29 GB corpus against a 4 s gate; this pass, 0.85 s.
func (r Record) Header() Header { return parseHeader(r.raw) }

// header field indices, one bit each in headerParse.seen.
const (
	fType = iota
	fUUID
	fSessionID
	fSessionAlt
	fTimestamp
	fCWD
	fRelocatedCWD
	fGitBranch
	fAgentID
	fPromptSource
	fIsSidechain
)

// headerParse carries the scan's state. seen makes every field first-wins, which
// is how gjson reads a repeated top-level key; without it the last value would
// win for a field whose first value was empty or false.
type headerParse struct {
	h         Header
	sessionAt string
	seen      uint32
}

// parseHeader takes the bytes as a parameter rather than reading r.raw inline:
// a slice loaded from a value receiver spills to memory, so every bounds check
// reloads its length. Measured over the corpus, inlining this costs 27%.
func parseHeader(b []byte) Header {
	var p headerParse
	i := skipSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		return p.h
	}
	i++
	for {
		i = skipSpace(b, i)
		if i >= len(b) || b[i] != '"' {
			break
		}
		key, next := scanString(b, i)
		i = skipSpace(b, next)
		if i < len(b) && b[i] == ':' {
			i++
		}
		i = skipSpace(b, i)
		start := i
		i = skipValue(b, i)
		p.field(key, b[start:i])
		i = skipSpace(b, i)
		if i >= len(b) || b[i] != ',' {
			break
		}
		i++
	}
	if p.h.SessionID == "" {
		p.h.SessionID = p.sessionAt
	}
	return p.h
}

func (p *headerParse) field(key, raw []byte) {
	switch string(key) {
	case "type":
		p.setString(fType, &p.h.Type, raw)
	case "uuid":
		p.setString(fUUID, &p.h.UUID, raw)
	case "sessionId":
		p.setString(fSessionID, &p.h.SessionID, raw)
	case "session_id":
		p.setString(fSessionAlt, &p.sessionAt, raw)
	case "timestamp":
		p.setString(fTimestamp, &p.h.Timestamp, raw)
	case "cwd":
		p.setString(fCWD, &p.h.CWD, raw)
	case "relocatedCwd":
		p.setString(fRelocatedCWD, &p.h.RelocatedCWD, raw)
	case "gitBranch":
		p.setString(fGitBranch, &p.h.GitBranch, raw)
	case "agentId":
		p.setString(fAgentID, &p.h.AgentID, raw)
	case "promptSource":
		if p.first(fPromptSource) {
			p.h.HasPromptSource = true
			p.h.PromptSource = valueString(raw)
		}
	case "isSidechain":
		if p.first(fIsSidechain) {
			p.h.IsSidechain = truthy(raw)
		}
	}
}

func (p *headerParse) first(field uint) bool {
	bit := uint32(1) << field
	if p.seen&bit != 0 {
		return false
	}
	p.seen |= bit
	return true
}

func (p *headerParse) setString(field uint, dst *string, raw []byte) {
	if p.first(field) {
		*dst = valueString(raw)
	}
}

var jsonTrue = []byte("true")

// numberStart is gjson's own set: a value opening with one of these is a number,
// including the Infinity and NaN spellings it tolerates.
func numberStart(c byte) bool {
	switch c {
	case '+', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'i', 'I', 'N':
		return true
	}
	return false
}

// truthy reads a boolean exactly as gjson's Result.Bool does. Matching it matters
// because a future version writing isSidechain numerically would otherwise read
// false here and true through the accessor, turning every subagent turn into an
// assistant and emptying --mine of the label the author rule leads with.
func truthy(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	switch {
	case raw[0] == '"':
		v, _ := strconv.ParseBool(strings.ToLower(unquote(raw)))
		return v
	case raw[0] == 't':
		return bytes.Equal(raw, jsonTrue)
	case numberStart(raw[0]):
		f, _ := strconv.ParseFloat(string(raw), 64)
		return f != 0
	}
	return false
}

// valueString reads a value exactly as gjson's Result.String does, type by type.
// The header is certified field-for-field against the accessors, so a value whose
// type a later release changes has to read the same through both; a rule of its
// own here would be a divergence the differential cannot see.
func valueString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	switch {
	case raw[0] == '"':
		return unquote(raw)
	case raw[0] == '{' || raw[0] == '[':
		return string(raw)
	case bytes.Equal(raw, jsonTrue):
		return "true"
	case bytes.Equal(raw, []byte("false")):
		return "false"
	case numberStart(raw[0]):
		return numberString(raw)
	}
	return ""
}

// numberString mirrors Result.String's number branch: digits pass through
// verbatim, anything else round-trips through the float gjson parsed.
func numberString(raw []byte) string {
	i := 0
	if raw[0] == '-' {
		i++
	}
	for ; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			f, _ := strconv.ParseFloat(string(raw), 64)
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return string(raw)
}

func unquote(raw []byte) string {
	if len(raw) < 2 {
		return ""
	}
	body := raw[1 : len(raw)-1]
	if bytes.IndexByte(body, '\\') < 0 {
		return string(body)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(body)
	}
	return s
}

func skipSpace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// scanString returns the contents of the string starting at b[i] and the index
// just past its closing quote.
func scanString(b []byte, i int) (val []byte, next int) {
	i++
	start := i
	for i < len(b) {
		switch b[i] {
		case '\\':
			i += 2
		case '"':
			return b[start:i], i + 1
		default:
			i++
		}
	}
	return b[start:], i
}

// skipValue returns the index just past the value starting at b[i].
func skipValue(b []byte, i int) int {
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '"':
		_, next := scanString(b, i)
		return next
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				_, i = scanString(b, i)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return i
	default:
		// gjson's parseNumber ends a bare value at any byte <= ' ', so the raw
		// span it reports matches this one and the two read the same text.
		for i < len(b) {
			if b[i] <= ' ' || b[i] == ',' || b[i] == '}' || b[i] == ']' {
				return i
			}
			i++
		}
		return i
	}
}
