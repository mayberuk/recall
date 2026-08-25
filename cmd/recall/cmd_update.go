package main

import (
	"context"
	"flag"
	"io"
	"os"
	"time"

	"github.com/mayberuk/recall/internal/archive"
	"github.com/mayberuk/recall/internal/fperr"
	"github.com/mayberuk/recall/internal/update"
)

type updateCmd struct {
	fs    *flag.FlagSet
	check bool
}

func newUpdateCmd() *updateCmd {
	c := &updateCmd{fs: newFlags("update")}
	c.fs.BoolVar(&c.check, "check", false, "report what the latest release is and stop, installing nothing")
	return c
}

func init() {
	Register("update", func(args []string) error { return runUpdate(args, os.Stdout, os.Stderr) })
	Describe("update", "", "replace this binary with the latest published release",
		newUpdateCmd().fs,
		"recall update",
		"recall update --check")
}

// runUpdate is one of the two verbs allowed to reach the network, and the only
// one that writes to the binary. Both facts are the point: a tool that
// promises no background process has to make every request something the user
// asked for by name.
func runUpdate(args []string, out, errOut io.Writer) error {
	c := newUpdateCmd()
	if _, err := parseArgs(c.fs, args); err != nil {
		return err
	}
	if update.Silenced() {
		return fperr.New(fperr.ArgError,
			"%s is set, so recall will not reach the network; unset it to update", update.SilenceEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := update.Client()

	rel, err := update.Latest(ctx, client)
	if err != nil {
		return fperr.New(fperr.ToolMissing, "cannot reach the releases API: %v", err)
	}

	// Recording the answer is the whole reason the other verbs can mention an
	// update without asking anyone: they read this file and never the network.
	if dir, dErr := archive.Dir(); dErr == nil {
		s := update.Load(dir)
		s.Latest, s.CheckedAt = rel.Tag, time.Now()
		_ = update.Save(dir, s)
	}

	switch {
	// A source build has no release to be behind. Say which one is current and
	// stop, rather than overwriting a binary its owner compiled on purpose.
	case !update.IsRelease(version):
		writeLine(out, "the latest release is "+rel.Tag+". This binary reports "+version+
			", so it was built from source; install a release to use `recall update`.")
		return nil
	case !update.Newer(rel.Tag, version):
		writeLine(out, "recall "+version+" is the latest release.")
		return nil
	case c.check:
		writeLine(out, "recall "+rel.Tag+" is available. You have "+version+". Run `recall update`.")
		return nil
	}

	self, err := os.Executable()
	if err != nil {
		return fperr.New(fperr.Internal, "cannot locate the running binary: %v", err)
	}
	update.SweepOld(self)

	writeLine(out, "recall "+version+" -> "+rel.Tag)
	if err := update.Install(ctx, client, rel.Tag, self, out); err != nil {
		return fperr.New(fperr.AtomicWriteFailed, "%v", err)
	}
	writeLine(out, "  replaced "+self)
	return nil
}

func writeLine(w io.Writer, s string) { _, _ = io.WriteString(w, s+"\n") }
