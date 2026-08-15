package main

import (
	"flag"
	"sort"
)

// RunFunc is a verb's entry point. args are what followed the verb on the
// command line, with the verb itself removed.
type RunFunc func(args []string) error

type entry struct {
	run      RunFunc
	args     string
	summary  string
	flags    *flag.FlagSet
	examples []string
}

var registry = map[string]entry{}

// Register adds a verb. Each cmd_<verb>.go calls it from its own init(), so
// adding a verb touches exactly one file and no two verbs ever edit the same
// one. Registering a verb twice panics at startup rather than letting one
// silently shadow the other.
func Register(verb string, fn RunFunc) {
	if verb == "" {
		panic("recall: refusing to register a verb with no name")
	}
	if fn == nil {
		panic("recall: refusing to register verb " + verb + " with no run function")
	}
	if _, dup := registry[verb]; dup {
		panic("recall: duplicate verb registration: " + verb)
	}
	registry[verb] = entry{run: fn}
}

// Describe attaches usage to an already-registered verb, including the flag set
// the verb parses. The flags are held rather than re-declared, because the one
// place an agent looks before its first invocation is `recall <verb> --help`,
// and a second hand-written list of flags is a list that goes stale.
func Describe(verb, args, summary string, fs *flag.FlagSet, examples ...string) {
	e, ok := registry[verb]
	if !ok {
		panic("recall: cannot describe unregistered verb: " + verb)
	}
	e.args, e.summary, e.flags, e.examples = args, summary, fs, examples
	registry[verb] = e
}

func verbNames() []string {
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// flagNames is every flag a verb accepts, in the order flag itself sorts them.
// A usage error prints this: "flag provided but not defined" with no list of
// what is defined leaves a caller with no next move.
func flagNames(verb string) []string {
	e, ok := registry[verb]
	if !ok || e.flags == nil {
		return nil
	}
	var out []string
	e.flags.VisitAll(func(f *flag.Flag) { out = append(out, "--"+f.Name) })
	return out
}
