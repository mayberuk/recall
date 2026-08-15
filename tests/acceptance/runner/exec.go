package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type invocation struct {
	Label string
	Argv  []string
	Dir   string
	Env   map[string]string
	// Shell runs Argv[0] as a shell program instead of executing the binary directly; a11 needs
	// the fzf pipeline to be a real pipeline.
	Shell   string
	Timeout time.Duration
}

type invResult struct {
	Label       string            `json:"label"`
	Argv        []string          `json:"argv"`
	Dir         string            `json:"dir"`
	EnvDelta    map[string]string `json:"env_delta"`
	ExitCode    int               `json:"exit_code"`
	DurationMS  int64             `json:"duration_ms"`
	StdoutBytes int               `json:"stdout_bytes"`
	StderrBytes int               `json:"stderr_bytes"`
	StdoutFile  string            `json:"stdout_file"`
	StderrFile  string            `json:"stderr_file"`
	StartError  string            `json:"start_error,omitempty"`
	TimedOut    bool              `json:"timed_out,omitempty"`

	stdout []byte
	stderr []byte
}

const defaultTimeout = 180 * time.Second

func runInvocation(in invocation) invResult {
	to := in.Timeout
	if to == 0 {
		to = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), to)
	defer cancel()

	var cmd *exec.Cmd
	if in.Shell != "" {
		cmd = exec.CommandContext(ctx, in.Shell, "-c", in.Argv[0])
	} else {
		cmd = exec.CommandContext(ctx, in.Argv[0], in.Argv[1:]...)
	}
	cmd.Dir = in.Dir
	cmd.Env = mergeEnv(in.Env)
	cmd.Stdin = strings.NewReader("")

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)

	res := invResult{
		Label:       in.Label,
		Argv:        in.Argv,
		Dir:         in.Dir,
		EnvDelta:    in.Env,
		DurationMS:  dur.Milliseconds(),
		StdoutBytes: out.Len(),
		StderrBytes: errb.Len(),
		stdout:      out.Bytes(),
		stderr:      errb.Bytes(),
		ExitCode:    -1,
	}
	if ctx.Err() != nil {
		res.TimedOut = true
	}
	if err == nil {
		res.ExitCode = 0
		return res
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res
	}
	res.StartError = err.Error()
	return res
}

func mergeEnv(delta map[string]string) []string {
	base := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			base[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range delta {
		base[k] = v
	}
	keys := make([]string, 0, len(base))
	for k := range base {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+base[k])
	}
	return env
}

func (r invResult) stdoutString() string { return string(r.stdout) }
