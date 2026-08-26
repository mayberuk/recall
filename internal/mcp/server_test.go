package mcp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mayberuk/recall/internal/fperr"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewServerRefusesWithoutASearcher(t *testing.T) {
	s, err := NewServer(Options{Version: "0"})
	if err == nil {
		t.Fatalf("NewServer with no searcher returned a server: %v", s)
	}
	if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("error code = %q, want %q", got, fperr.ArgError)
	}
	if !strings.Contains(err.Error(), "searcher") {
		t.Errorf("error %q does not say what is missing", err)
	}
}

// stdout carries the protocol under stdio, so a Log pointed at it would
// corrupt the stream at the first diagnostic. Refusing at construction turns
// that into a startup failure instead of a client reporting malformed JSON
// from an unrelated call.
func TestNewServerRefusesToLogToStdout(t *testing.T) {
	if _, err := NewServer(Options{Searcher: &fakeSearcher{}, Log: os.Stdout}); err == nil {
		t.Fatal("NewServer accepted os.Stdout as its log")
	} else if !strings.Contains(err.Error(), "stdout") {
		t.Errorf("error %q does not name stdout", err)
	}

	// The negative control: any other writer is fine.
	if _, err := NewServer(Options{Searcher: &fakeSearcher{}, Log: &bytes.Buffer{}}); err != nil {
		t.Errorf("NewServer rejected an ordinary log writer: %v", err)
	}
	if _, err := NewServer(Options{Searcher: &fakeSearcher{}}); err != nil {
		t.Errorf("NewServer rejected a nil log: %v", err)
	}
}

func TestServeRefusesTheSameOptionsNewServerDoes(t *testing.T) {
	if err := Serve(context.Background(), Options{}); err == nil {
		t.Fatal("Serve with no searcher returned nil")
	} else if got := codeOf(t, err); got != fperr.ArgError {
		t.Errorf("error code = %q, want %q", got, fperr.ArgError)
	}
}

// codeOf is the slug a caller parses off recall's final stderr line.
func codeOf(t *testing.T, err error) fperr.Code {
	t.Helper()
	var coded *fperr.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error %v carries no failure code", err)
	}
	return coded.Code
}

// capture records the result of one method as it leaves the server, which is
// the only way to read a server/discover response: the client folds it into
// its session state and does not hand the raw result back.
type capture struct {
	mu     sync.Mutex
	method string
	result mcpsdk.Result
}

func (c *capture) middleware(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		res, err := next(ctx, method, req)
		if method == c.method && err == nil {
			c.mu.Lock()
			c.result = res
			c.mu.Unlock()
		}
		return res, err
	}
}

func (c *capture) get() mcpsdk.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

func TestDiscoverAdvertises20260728AndNotTheLoggingCapability(t *testing.T) {
	s, err := NewServer(Options{Version: "0.0.0-test", Searcher: &fakeSearcher{}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	seen := &capture{method: "server/discover"}
	s.AddReceivingMiddleware(seen.middleware)

	cs := connect(t, s)

	res, ok := seen.get().(*mcpsdk.DiscoverResult)
	if !ok {
		t.Fatalf("server/discover produced %T, want *mcpsdk.DiscoverResult", seen.get())
	}
	if !slices.Contains(res.SupportedVersions, "2026-07-28") {
		t.Errorf("supportedVersions = %v, want it to include 2026-07-28", res.SupportedVersions)
	}
	if res.Capabilities == nil {
		t.Fatal("server/discover advertised no capabilities at all")
	}
	// Naming a deprecated field is the assertion, not an oversight: the only way
	// to prove recall does not advertise logging is to look at the field that
	// would carry it. The SDK keeps it functional through the deprecation
	// window, so this stays until the field itself goes.
	//lint:ignore SA1019 asserting the deprecated capability is absent requires naming it
	if res.Capabilities.Logging != nil {
		t.Error("server/discover advertises the logging capability, which is deprecated in this revision")
	}
	// The positive control: dropping logging must not have dropped everything.
	if res.Capabilities.Tools == nil {
		t.Error("server/discover advertises no tools capability")
	}
	if !strings.Contains(res.Instructions, "recall_guide") {
		t.Errorf("instructions do not point at recall_guide: %q", res.Instructions)
	}

	// And the version the two of them actually settled on.
	if got := cs.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Errorf("negotiated protocol version = %q, want 2026-07-28", got)
	}
}

// The instructions paragraph and the first-call preamble (preamble.go) are
// two carriers of the same fact, kept apart because client uptake of either
// alone is inconsistent. This is the negative control that stops them
// drifting: instructions must say the first search already carries the
// guide, not just point at a still-mandatory round trip through recall_guide.
func TestInitializeInstructionsNameTheFirstCallMechanism(t *testing.T) {
	s, err := NewServer(Options{Version: "0.0.0-test", Searcher: &fakeSearcher{}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cs := connect(t, s)
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "first search") {
		t.Errorf("instructions do not mention the first-call preamble mechanism: %q", got)
	}
}

// Under stdio, stdout is the protocol stream. One stray write from this
// package corrupts it for the rest of the session, so exercise every path a
// client can reach and assert stdout stayed empty.
func TestNothingInThisPackageWritesToStdout(t *testing.T) {
	real := os.Stdout
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	os.Stdout = file
	defer func() {
		os.Stdout = real
		_ = file.Close()
	}()

	log := &bytes.Buffer{}
	fake := &fakeSearcher{guide: "the guide"}
	s, err := NewServer(Options{Version: "0.0.0-test", Searcher: fake, Log: log})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	cs := connect(t, s)

	listTools(t, cs)
	callTool(t, cs, "recall_guide", GuideArgs{})
	callTool(t, cs, "recall_find", FindArgs{SearchArgs: SearchArgs{Query: "agvtool"}})
	fake.err = errors.New("deliberate failure")
	callTool(t, cs, "recall_turns", TurnsArgs{SearchArgs: SearchArgs{Query: "agvtool"}})

	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		body, _ := os.ReadFile(file.Name())
		t.Errorf("%d bytes were written to stdout: %q", info.Size(), body)
	}
}
