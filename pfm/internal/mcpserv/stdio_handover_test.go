package mcpserv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const (
	handoverTestInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
	handoverTestInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	handoverTestCall        = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}` + "\n"
	handoverTestCallAgain   = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami"}}` + "\n"
)

var handoverTestArgs = []string{"mcp", "serve", "--stdio", "--family", "chat"}

// handoverExit is a child's non-zero exit, as exec.ExitError reports one.
type handoverExit int

func (exit handoverExit) Error() string { return fmt.Sprintf("exit status %d", int(exit)) }
func (exit handoverExit) ExitCode() int { return int(exit) }

// handoverChildScript plays one installed pfm: it answers initialize as
// answer says ("result", "error", "eof", "notice-first") and every tools/call
// with its own name.
type handoverChildScript struct {
	name, answer string
	waitErr      error
	stdin        *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdout       *io.PipeReader
	stdoutWriter *io.PipeWriter
	done         chan struct{}
	mutex        sync.Mutex
	received     []string
	killed       bool
}

func newHandoverChild(name, answer string, waitErr error) *handoverChildScript {
	child := &handoverChildScript{name: name, answer: answer, waitErr: waitErr, done: make(chan struct{})}
	child.stdin, child.stdinWriter = io.Pipe()
	child.stdout, child.stdoutWriter = io.Pipe()
	return child
}

func (child *handoverChildScript) play() {
	defer close(child.done)
	defer func() { _ = child.stdoutWriter.Close() }()
	lines := bufio.NewReader(child.stdin)
	for {
		line, err := lines.ReadString('\n')
		if err != nil {
			return
		}
		child.mutex.Lock()
		child.received = append(child.received, line)
		child.mutex.Unlock()
		var frame proxyFrame
		if json.Unmarshal([]byte(line), &frame) != nil {
			continue
		}
		reply := ""
		switch {
		case frame.Method == proxyInitializeMethod && child.answer == "eof":
			return
		case frame.Method == proxyInitializeMethod && child.answer == "error":
			reply = `{"jsonrpc":"2.0","id":` + string(frame.ID) + `,"error":{"code":-32603,"message":"schema v8"}}`
		case frame.Method == proxyInitializeMethod:
			if child.answer == "notice-first" {
				reply = `{"jsonrpc":"2.0","method":"notifications/message","params":{"data":"starting"}}` + "\n"
			}
			id := string(frame.ID)
			reply += `{"jsonrpc":"2.0","id":` + id + `,"result":{"serverInfo":{"name":"` + child.name + `"}}}`
		case frame.Method == "tools/call" && strings.Contains(line, `"name":"hold"`):
			// A long call: answered never, so only its cancellation settles it.
		case frame.Method == "tools/call":
			reply = `{"jsonrpc":"2.0","id":` + string(frame.ID) + `,"result":{"image":"` + child.name + `"}}`
		}
		if reply != "" {
			if _, err := child.stdoutWriter.Write([]byte(reply + "\n")); err != nil {
				return
			}
		}
	}
}

func (child *handoverChildScript) frames() []string {
	child.mutex.Lock()
	defer child.mutex.Unlock()
	return append([]string(nil), child.received...)
}

func (*handoverChildScript) Pid() int { return 4343 }

func (*handoverChildScript) Release() error { return nil }

func (child *handoverChildScript) StdinPipe() (io.WriteCloser, error) { return child.stdinWriter, nil }

func (child *handoverChildScript) StdoutPipe() (io.ReadCloser, error) { return child.stdout, nil }

func (*handoverChildScript) KillGroup() error { return errors.New("no process group") }

func (*handoverChildScript) SignalGroup(syscall.Signal) error { return errors.New("no process group") }

func (child *handoverChildScript) Wait() error {
	<-child.done
	return child.waitErr
}

func (child *handoverChildScript) Kill() error {
	child.mutex.Lock()
	child.killed = true
	child.mutex.Unlock()
	_ = child.stdin.Close()
	return nil
}

// handoverRunner starts the scripted children in order, recording each start.
type handoverRunner struct {
	deps.FakeRunner
	mutex    sync.Mutex
	children []*handoverChildScript
	startErr error
	starts   []deps.StartCall
}

func (runner *handoverRunner) Start(_ context.Context, argv []string, options deps.StartOptions) (deps.Process, error) {
	runner.mutex.Lock()
	defer runner.mutex.Unlock()
	runner.starts = append(runner.starts, deps.StartCall{Argv: argv, Opts: options})
	if runner.startErr != nil {
		return nil, runner.startErr
	}
	child := runner.children[0]
	runner.children = runner.children[1:]
	go child.play()
	return child, nil
}

// handoverOutput is the client's end of the server's output.
type handoverOutput struct {
	mutex  sync.Mutex
	buffer strings.Builder
}

func (output *handoverOutput) Write(content []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.Write(content)
}

func (output *handoverOutput) String() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.String()
}

// waitHandover polls a named condition until it holds or two seconds pass.
func waitHandover(t *testing.T, what string, holds func() bool) {
	t.Helper()
	deadline := clock.Real.Now().Add(2 * time.Second)
	for !holds() {
		if clock.Real.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		_ = clock.Real.Sleep(context.Background(), 5*time.Millisecond)
	}
}

// installHandoverBuild renames a new build over path, as make host-install does.
func installHandoverBuild(t *testing.T, path, body string) {
	t.Helper()
	if err := testjail.WriteExecutableNeverRun(path+".new", []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func newTestHandover(t *testing.T, runner *handoverRunner) (*stdioHandover, *handoverOutput, *strings.Builder, string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "pfm")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	image, err := binwatch.ImageAt(binary)
	if err != nil {
		t.Fatal(err)
	}
	output, warnings := &handoverOutput{}, &strings.Builder{}
	return &stdioHandover{
		options:  Handover{Image: image, Args: handoverTestArgs, Runner: runner},
		warnings: warnings,
		output:   output,
		end:      func() {},
	}, output, warnings, binary
}

func TestStdioHandoverMovesTheChatOntoTheInstalledPfm(t *testing.T) {
	newChild := newHandoverChild("new", "result", handoverExit(3))
	runner := &handoverRunner{children: []*handoverChildScript{newChild}}
	handover, output, warnings, binary := newTestHandover(t, runner)
	for _, frame := range []string{handoverTestInitialize, handoverTestInitialized} {
		if handover.carry([]byte(frame)) {
			t.Fatalf("untouched image handed %q over", frame)
		}
	}
	if len(runner.starts) != 0 {
		t.Fatalf("untouched image started %d installed pfm(s), want none", len(runner.starts))
	}
	installHandoverBuild(t, binary, "new build")
	if !handover.carry([]byte(handoverTestCall)) {
		t.Fatal("replaced image kept the tools/call")
	}
	wantReply := `{"jsonrpc":"2.0","id":2,"result":{"image":"new"}}` + "\n"
	waitHandover(t, "the installed pfm's tools/call answer", func() bool { return output.String() == wantReply })
	wantFrames := []string{handoverTestInitialize, handoverTestInitialized, handoverTestCall}
	if got := newChild.frames(); !slices.Equal(got, wantFrames) {
		t.Fatalf("installed pfm received %q, want %q", got, wantFrames)
	}
	start := runner.starts[0]
	wantArgv := append([]string{binary}, handoverTestArgs...)
	marker := paths.EnvMCPHandoverChild + "=1"
	if !slices.Equal(start.Argv, wantArgv) || !slices.Contains(start.Opts.Env, marker) ||
		!start.Opts.StdinPipe || !start.Opts.StdoutPipe || start.Opts.Stderr != warnings {
		t.Fatalf("start = %q %+v, want argv %q with %s, both pipes and stderr the warnings", start.Argv, start.Opts,
			wantArgv, marker)
	}
	handover.closeInput()
	var exit *HandoverExitError
	if err := handover.finish(); !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("finish = %v, want the installed pfm's exit status 3", err)
	}
	if output.String() != wantReply || warnings.String() != "" {
		t.Fatalf("output %q, warnings %q; want %q and none", output.String(), warnings.String(), wantReply)
	}
}

func TestStdioHandoverBeforeTheHandshakeCarriesTheClientsOwn(t *testing.T) {
	newChild := newHandoverChild("new", "result", nil)
	runner := &handoverRunner{children: []*handoverChildScript{newChild}}
	handover, output, warnings, binary := newTestHandover(t, runner)
	installHandoverBuild(t, binary, "new build")
	if !handover.carry([]byte(handoverTestInitialize)) {
		t.Fatal("replaced image kept the initialize")
	}
	wantReply := `{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"new"}}}` + "\n"
	waitHandover(t, "the installed pfm's initialize answer", func() bool { return output.String() == wantReply })
	handover.closeInput()
	if err := handover.finish(); err != nil || warnings.String() != "" {
		t.Fatalf("finish = %v, warnings %q; want nil and none", err, warnings.String())
	}
}

func TestStdioHandoverMovesAnInstalledPfmAnInstallReplacedAgain(t *testing.T) {
	first, second := newHandoverChild("first", "result", nil), newHandoverChild("second", "result", nil)
	runner := &handoverRunner{children: []*handoverChildScript{first, second}}
	handover, output, warnings, binary := newTestHandover(t, runner)
	handover.carry([]byte(handoverTestInitialize))
	handover.carry([]byte(handoverTestInitialized))
	installHandoverBuild(t, binary, "first build")
	handover.carry([]byte(handoverTestCall))
	firstReply := `{"jsonrpc":"2.0","id":2,"result":{"image":"first"}}` + "\n"
	waitHandover(t, "the first installed pfm's answer", func() bool { return output.String() == firstReply })
	installHandoverBuild(t, binary, "second build, longer")
	if !handover.carry([]byte(handoverTestCallAgain)) {
		t.Fatal("handed-over server kept the tools/call")
	}
	secondReply := `{"jsonrpc":"2.0","id":3,"result":{"image":"second"}}` + "\n"
	waitHandover(t, "the second installed pfm's answer", func() bool {
		return output.String() == firstReply+secondReply
	})
	// The first one's input ended: it answered every request it held.
	waitHandover(t, "the first installed pfm's input to end", func() bool {
		select {
		case <-first.done:
			return true
		default:
			return false
		}
	})
	wantSecond := []string{handoverTestInitialize, handoverTestInitialized, handoverTestCallAgain}
	if got := second.frames(); !slices.Equal(got, wantSecond) {
		t.Fatalf("second installed pfm received %q, want %q", got, wantSecond)
	}
	handover.closeInput()
	if err := handover.finish(); err != nil || warnings.String() != "" || len(runner.starts) != 2 {
		t.Fatalf("finish = %v, warnings %q, %d starts; want nil, none, 2", err, warnings.String(), len(runner.starts))
	}
}

func TestRunStdioEndsWithTheInstalledPfmsExitStatus(t *testing.T) {
	child := newHandoverChild("new", "result", handoverExit(4))
	runner := &handoverRunner{children: []*handoverChildScript{child}}
	handover, _, _, binary := newTestHandover(t, runner)
	local := stdioTestService("local", io.Discard)
	input, client := io.Pipe()
	output := &handoverOutput{}
	options := StdioOptions{Warnings: io.Discard, Handover: &handover.options}
	returned := make(chan error, 1)
	go func() {
		returned <- stdioTestProfessor(t, local).RunStdio(context.Background(), input, output, options)
	}()
	if _, err := client.Write([]byte(handoverTestInitialize + handoverTestInitialized)); err != nil {
		t.Fatal(err)
	}
	answered := func() bool { return strings.Contains(output.String(), `"id":1`) }
	waitHandover(t, "the running image's initialize answer", answered)
	installHandoverBuild(t, binary, "new build")
	if _, err := client.Write([]byte(handoverTestCall)); err != nil {
		t.Fatal(err)
	}
	wantTail := `{"jsonrpc":"2.0","id":2,"result":{"image":"new"}}` + "\n"
	waitHandover(t, "the installed pfm's answer", func() bool { return strings.HasSuffix(output.String(), wantTail) })
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	var exit *HandoverExitError
	if err := <-returned; !errors.As(err, &exit) || exit.Code != 4 {
		t.Fatalf("RunStdio = %v, want the installed pfm's exit status 4", err)
	}
}
