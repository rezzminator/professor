package installer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// codexAppServer plays `codex app-server` over stdio for every start of the
// install's ProcessRunner: initialize, hooks/list from the listing file (one
// JSON hook object per line, "@STATUS@" in each standing for untrusted until a
// config/value/write named the hook's key, then trusted), and
// config/value/write. Each start is scripted on its own FakeRunner, so every
// exchange gets fresh pipes; every request line is appended to the log and
// every start to Starts.
type codexAppServer struct {
	*deps.FakeRunner
	listing, requestLog string
	mu                  sync.Mutex
	starts              []deps.StartCall
}

// Starts returns every start the install made, in order.
func (fake *codexAppServer) Starts() []deps.StartCall {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]deps.StartCall(nil), fake.starts...)
}

func (fake *codexAppServer) Start(ctx context.Context, argv []string, opts deps.StartOptions) (deps.Process, error) {
	fake.mu.Lock()
	fake.starts = append(fake.starts, deps.StartCall{Argv: append([]string(nil), argv...), Opts: opts})
	fake.mu.Unlock()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	go fake.serve(stdinReader, stdoutWriter)
	child := &deps.FakeRunner{}
	child.ScriptInteractive(
		[]string{argv[0], "app-server"},
		deps.InteractiveScript{Pid: 4242, Stdin: stdinWriter, Stdout: stdoutReader},
	)
	return child.Start(ctx, argv, opts)
}

func (fake *codexAppServer) serve(stdin *io.PipeReader, stdout *io.PipeWriter) {
	// Replies queue apart from the reads: rpc sends every request before it
	// reads a reply, as a real pipe's buffer lets it.
	replies := make(chan string, 8)
	var serveErr error
	go func() {
		for reply := range replies {
			_, _ = stdout.Write([]byte(reply + "\n"))
		}
		_ = stdout.CloseWithError(serveErr)
	}()
	defer close(replies)
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 65536), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if err := fake.logRequest(line); err != nil {
			serveErr = err
			return
		}
		switch {
		case strings.Contains(line, `"method":"initialize"`):
			replies <- `{"id":0,"result":{}}`
		case strings.Contains(line, `"method":"hooks/list"`):
			reply, err := fake.hooksList()
			if err != nil {
				serveErr = err
				return
			}
			replies <- reply
		case strings.Contains(line, `"method":"config/value/write"`):
			replies <- `{"id":1,"result":{}}`
		}
	}
	serveErr = scanner.Err()
}

func (fake *codexAppServer) logRequest(line string) error {
	log, err := os.OpenFile(fake.requestLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, err = log.WriteString(line + "\n")
		err = errors.Join(err, log.Close())
	}
	if err != nil {
		return fmt.Errorf("log the app-server request in %s: %w", fake.requestLog, err)
	}
	return nil
}

// hooksList answers hooks/list: every listing entry, trusted when a logged
// config/value/write carries its key.
func (fake *codexAppServer) hooksList() (string, error) {
	listing, err := os.ReadFile(fake.listing)
	if err != nil {
		return "", fmt.Errorf("read the hooks listing %s: %w", fake.listing, err)
	}
	logged, err := os.ReadFile(fake.requestLog)
	if err != nil {
		return "", fmt.Errorf("read the app-server request log %s: %w", fake.requestLog, err)
	}
	var writes []string
	for _, line := range strings.Split(string(logged), "\n") {
		if strings.Contains(line, "config/value/write") {
			writes = append(writes, line)
		}
	}
	var entries []string
	for _, entry := range strings.Split(strings.TrimSuffix(string(listing), "\n"), "\n") {
		if entry == "" {
			continue
		}
		_, afterKey, _ := strings.Cut(entry, `"key":"`)
		key, _, _ := strings.Cut(afterKey, `"`)
		status := "untrusted"
		for _, write := range writes {
			if strings.Contains(write, key+`\"`) {
				status = "trusted"
			}
		}
		entries = append(entries, strings.Replace(entry, "@STATUS@", status, 1))
	}
	return `{"id":1,"result":{"data":[{"hooks":[` + strings.Join(entries, ",") + `]}]}}`, nil
}
