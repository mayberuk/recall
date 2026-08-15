package corpusgen

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// fillerWords are the corpus's noise. They are ordinary words on purpose: a
// scan over text with no word boundaries measures nothing a real transcript
// does, and the tokenizer's behaviour is part of what a benchmark is timing.
var fillerWords = []string{
	"adapter", "after", "already", "another", "answer", "around", "because",
	"before", "branch", "build", "cache", "called", "change", "check", "client",
	"commit", "config", "context", "cursor", "default", "depends", "detail",
	"during", "every", "failure", "fetch", "field", "handler", "header",
	"import", "inside", "instead", "layer", "listed", "little", "module",
	"needed", "network", "object", "offset", "option", "output", "package",
	"parser", "path", "pending", "process", "queue", "reader", "record",
	"release", "request", "result", "retry", "return", "runner", "schema",
	"scope", "second", "server", "session", "should", "signal", "source",
	"state", "still", "stream", "target", "thread", "timeout", "token",
	"update", "value", "verify", "walk", "window", "worker", "wrapper",
}

func (g *generator) sentence(words int) string {
	var b strings.Builder
	for i := 0; i < words; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(fillerWords[g.rnd.Intn(len(fillerWords))])
	}
	b.WriteByte('.')
	return b.String()
}

// filler is a sentence of about n bytes. Sizing text by bytes rather than by a
// word count is what makes the byte target and the tier ratio one problem
// instead of two: the ratio fixes how many turns of each tier are written, and
// the measured mean size of a turn in that tier fixes how big each one is.
func (g *generator) filler(n int) string { return g.sentence(wordsFor(n)) }

// meanWordBytes is what one word of a sentence costs, counting the space after
// it or the full stop that ends the sentence.
var meanWordBytes = func() float64 {
	total := 0
	for _, w := range fillerWords {
		total += len(w) + 1
	}
	return float64(total) / float64(len(fillerWords))
}()

func wordsFor(n int) int {
	return max(int(math.Round(float64(n)/meanWordBytes)), 1)
}

func (g *generator) uuid() string {
	return fmt.Sprintf("%08x-%04x-4%03x-%04x-%012x",
		g.rnd.Uint32(), g.rnd.Intn(1<<16), g.rnd.Intn(1<<12),
		0x8000|g.rnd.Intn(1<<14), g.rnd.Uint64()&0xffffffffffff)
}

func (g *generator) token() string {
	return fmt.Sprintf("%016x", g.rnd.Uint64())
}

// signature is the opaque blob a thinking block carries. It is 94.5% of
// thinking bytes in the real corpus and none of its words, so a corpus without
// it would let strip look far cheaper than it is.
func (g *generator) signature() string {
	var b strings.Builder
	b.WriteString("Cg")
	for i := 0; i < 8; i++ {
		b.WriteString(strconv.FormatUint(g.rnd.Uint64(), 16))
	}
	return b.String()
}

func joinWords(words []string) string { return strings.Join(words, " ") }
