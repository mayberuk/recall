// Package mcp serves recall's four searching verbs and its guide over the
// Model Context Protocol, stdio only, so an agent reaches the same answers the
// CLI prints without spawning a process per question.
//
// It is a second envelope around the answer the CLI already computes, never a
// second implementation of it: nothing here scans, ranks, refreshes or reads a
// repo. The search arrives through [Searcher], and the coverage footer every
// view carries is passed through untouched.
package mcp

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/mayberuk/recall/internal/fperr"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// instructions is what a client shows the model before its first call. It
// points at the guide rather than restating it, because the guide is a page
// and this is a paragraph, and because a caller that reads the guide once has
// it for the rest of the session.
const instructions = "recall searches the transcripts of past coding-agent sessions on this machine. " +
	"Call recall_guide once before the first query: it states how a query is read and what a search " +
	"leaves out by default, which is the difference between a useful answer and a confident wrong one. " +
	"Every answer carries a coverage block naming what was not searched and what was capped — read it, " +
	"because finding nothing within that scope is not the same as there being nothing."

// toolListTTL is how long a client may serve a cached tools/list, in
// milliseconds. The list is compiled into the binary and cannot vary per
// connection, so the only thing this bounds is how long a client keeps serving
// the old list after recall is upgraded mid-session. An hour is short against
// that and long against a session.
const toolListTTL = 3_600_000

// toolListScope is private rather than public because a stdio server has no
// intermediary to share a cache with: public buys nothing stdio can exercise,
// and private can never be wrong.
const toolListScope = "private"

// Options configures a server.
type Options struct {
	// Version is what the server reports as its own implementation version.
	Version string

	// Searcher answers every tool call. A nil one is refused rather than
	// panicked on at the first call.
	Searcher Searcher

	// Log is where this package's diagnostics go. It must never be os.Stdout:
	// under stdio, stdout carries the protocol, and one stray write corrupts
	// the stream for the rest of the session. A nil Log discards.
	Log io.Writer
}

// NewServer builds the server without connecting it to anything.
func NewServer(opt Options) (*mcpsdk.Server, error) {
	if opt.Searcher == nil {
		return nil, fperr.New(fperr.ArgError, "an mcp server needs a searcher to answer with")
	}
	// Caught here rather than left to corrupt the protocol stream, where the
	// symptom is a client reporting malformed JSON from an unrelated call.
	if opt.Log == os.Stdout {
		return nil, fperr.New(fperr.ArgError, "an mcp server cannot log to stdout; stdout carries the protocol")
	}

	s := mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:        "recall",
			Version:     opt.Version,
			Title:       "recall",
			Description: "search past agent sessions on this machine",
		},
		&mcpsdk.ServerOptions{
			// Non-nil, because the SDK's nil default is {"logging":{}} for
			// historical reasons and logging is deprecated in this revision.
			Capabilities: &mcpsdk.ServerCapabilities{Tools: &mcpsdk.ToolCapabilities{}},
			Instructions: instructions,
			Logger:       logger(opt.Log),
		})

	registerTools(s, &calls{searcher: opt.Searcher})
	s.AddReceivingMiddleware(cacheToolList)
	return s, nil
}

// Serve runs the server over stdio to completion.
func Serve(ctx context.Context, opt Options) error {
	s, err := NewServer(opt)
	if err != nil {
		return err
	}
	return s.Run(ctx, &mcpsdk.StdioTransport{})
}

// cacheToolList stamps the cache hint onto tools/list, which the SDK leaves
// unset.
func cacheToolList(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		res, err := next(ctx, method, req)
		if list, ok := res.(*mcpsdk.ListToolsResult); ok {
			list.Cacheable = mcpsdk.Cacheable{TTLMs: toolListTTL, CacheScope: toolListScope}
		}
		return res, err
	}
}

// logger is always non-nil, so no path through the SDK has to test for one.
func logger(w io.Writer) *slog.Logger {
	if w == nil {
		w = io.Discard
	}
	return slog.New(slog.NewTextHandler(w, nil))
}
