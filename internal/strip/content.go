package strip

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mayberuk/recall/internal/jsonl"
)

// identKeys name the identifying argument of a tool call, most common first.
// A key absent from this list is never read directly, which is what keeps Edit
// and Write payloads — 65 MB of whole file contents — out of the invocation tier.
var identKeys = [...]string{
	"command", "file_path", "url", "query", "pattern",
	"path", "notebook_path", "skill", "subagent_type",
}

// identMax bounds a fallback argument. An identifying argument is short; a
// payload is not, so the cap is what makes the fallback safe for a tool this
// build has never seen.
const identMax = 256

// parts is what one record's message content yielded, one bucket per tier.
type parts struct {
	conv []string
	inv  []string
	res  []string

	// shaped and sawResult answer the content-shape question doctor asks, and
	// nothing else. Attribution uses promptSource; content shape is refuted for
	// that job and overcounts fivefold.
	shaped    bool
	sawResult bool
}

// gather walks message.content once, addressing each block's fields by index.
//
// Why not fetch the content array whole: jsonl copies whatever path it is given,
// so one Get of message.content copies 700 MB of tool payloads and thinking
// signatures over the corpus only to discard them, and the resulting garbage
// evicts the page cache the read depends on. Indexed access copies the words.
func (p *parts) gather(rec jsonl.Record) {
	n := rec.Get("message.content.#")
	if !n.Exists() {
		if t := rec.Get("message.content").String(); t != "" {
			p.conv = append(p.conv, t)
			p.shaped = true
		}
		return
	}
	for i := 0; i < int(n.Int()); i++ {
		base := "message.content." + strconv.Itoa(i) + "."
		switch rec.Get(base + "type").String() {
		case "text":
			p.shaped = true
			p.push(&p.conv, rec.Get(base+"text").String())
		case "thinking":
			p.push(&p.conv, rec.Get(base+"thinking").String())
		case "tool_use":
			p.inv = append(p.inv, invocation(rec, base))
		case "tool_result":
			p.sawResult = true
			p.result(rec, base)
		}
	}
}

func (p *parts) push(dst *[]string, text string) {
	if text != "" {
		*dst = append(*dst, text)
	}
}

// result reads a tool result from message.content and never from the top-level
// toolUseResult field, which is a second structured copy of the same bytes —
// 362 MB of pure duplication, and the largest single cut in the funnel.
func (p *parts) result(rec jsonl.Record, base string) {
	c := rec.Get(base + "content")
	if !c.IsArray() {
		p.push(&p.res, c.String())
		return
	}
	for _, b := range c.Array() {
		if b.Get("type").String() == "text" {
			p.push(&p.res, b.Get("text").String())
		}
	}
}

// invocation is the tool name plus its identifying argument.
func invocation(rec jsonl.Record, base string) string {
	name := rec.Get(base + "name").String()
	for _, k := range identKeys {
		if v := rec.Get(base + "input." + k); v.Exists() {
			if arg := v.String(); arg != "" {
				return name + " " + arg
			}
		}
	}
	return name + shortArgs(rec.Get(base+"input"))
}

// shortArgs is the fallback for a tool whose identifying argument this build
// does not know: every scalar argument short enough not to be a payload, keyed
// and ordered so the same call always strips to the same line.
func shortArgs(in jsonl.Value) string {
	if !in.IsObject() {
		return ""
	}
	keys, vals := in.Get("@keys").Array(), in.Get("@values").Array()
	if len(keys) != len(vals) {
		return ""
	}
	args := make([]string, 0, len(keys))
	for i, k := range keys {
		v := vals[i]
		if v.IsArray() || v.IsObject() {
			continue
		}
		s := v.String()
		if s == "" || len(s) > identMax {
			continue
		}
		args = append(args, k.String()+"="+s)
	}
	if len(args) == 0 {
		return ""
	}
	sort.Strings(args)
	return " " + strings.Join(args, " ")
}

const (
	nameOpen  = "<command-name>"
	nameClose = "</command-name>"
	argsOpen  = "<command-args>"
	argsClose = "</command-args>"
)

// commandLine rebuilds the line typed at a slash-command record: the command
// name and its arguments, as one line, with the XML scaffolding and the command
// message discarded. Dropping the name would make `/livecheck:run` unsearchable
// though it was typed, which is a silent false negative — the dealbreaker.
// Empty arguments mean nothing was typed beyond the wrapper, so there is no turn.
func commandLine(text string) (string, bool) {
	args, ok := tagBody(text, argsOpen, argsClose)
	if !ok || args == "" {
		return "", false
	}
	if name, ok := tagBody(text, nameOpen, nameClose); ok && name != "" {
		return name + " " + args, true
	}
	return args, true
}

func tagBody(text, openTag, closeTag string) (string, bool) {
	i := strings.Index(text, openTag)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(openTag):]
	j := strings.Index(rest, closeTag)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:j]), true
}
