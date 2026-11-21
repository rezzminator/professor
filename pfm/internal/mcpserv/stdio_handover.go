package mcpserv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// handoverReplayBound is how long a newly installed pfm may take to answer the
// replayed initialize before the hand-over gives up and the running image
// keeps serving.
const handoverReplayBound = 15 * time.Second

// Handover moves a stdio server onto the newly installed pfm (StdioOptions.Handover;
// nil never moves). A chat starts its stdio server once and never relaunches it, so
// a server left on a replaced image fails every call the new build's state
// database refuses it. Before each client frame the server asks Image whether an
// install replaced it; once one has, it starts the installed binary with Args (its
// own arguments) and the environment plus EnvMCPHandoverChild=1, replays the
// client's handshake, and from then on carries every frame both ways unchanged.
// A later install replaces the child the same way, so the chain is never deeper
// than one extra pfm.
type Handover struct {
	Image  binwatch.Image
	Args   []string
	Runner deps.Runner
}

// HandoverExitError ends a server whose installed pfm ended with a non-zero Code.
type HandoverExitError struct{ Code int }

func (exit *HandoverExitError) Error() string {
	return fmt.Sprintf("the installed pfm serving this chat exited with status %d", exit.Code)
}

// stdioHandover is one server's hand-over state. carry runs on the frame reader
// alone; the children's output pumps share the rest under mutex.
type stdioHandover struct {
	options   Handover
	warnings  io.Writer
	output    io.Writer
	end       func()
	handshake stdioHandshake
	// declined is the image a failed hand-over tried: it is retried only once
	// an install replaces it again.
	declined *binwatch.Image
	// front is the client requests the image this server started on took,
	// so a later cancellation of one still reaches it; a key the size of a
	// request id per request, kept until a cancellation names it.
	front    map[string]bool
	mutex    sync.Mutex
	current  *handoverChild
	children []*handoverChild
	ended    bool
}

// carry hands frame to the installed pfm, starting it first when an install has
// replaced the image serving now; false leaves frame to this image.
func (handover *stdioHandover) carry(frame []byte) bool {
	var envelope proxyFrame
	_ = json.Unmarshal(frame, &envelope)
	handover.mutex.Lock()
	current := handover.current
	handover.mutex.Unlock()
	serving := handover.options.Image
	if current != nil {
		serving = current.image
	}
	if handover.replaced(serving) {
		if child, err := handover.start(); err != nil {
			handover.warn("move this chat's MCP server onto the installed pfm %s, keeping the running one: %v",
				handover.options.Image.Path, err)
		} else {
			handover.mutex.Lock()
			handover.current = child
			handover.mutex.Unlock()
			// Serving now: its end ends this server (exited).
			go child.pump()
			if current != nil {
				current.retire()
			}
			current = child
		}
	}
	if isProxyHandshake(envelope.Method) {
		handover.handshake.store(envelope.Method, frame)
	}
	if carried, front := handover.cancellation(frame, envelope, current); carried || front {
		return carried
	}
	if current == nil {
		handover.recordFront(envelope)
		return false
	}
	current.send(frame, envelope)
	return true
}

// replaced asks the door whether serving is stale, once per installed build.
func (handover *stdioHandover) replaced(serving binwatch.Image) bool {
	if handover.declined != nil {
		serving = *handover.declined
	}
	replaced, err := serving.Replaced()
	if err != nil {
		return false // mid-install: the path is back by the next frame
	}
	return replaced
}

// start runs the installed pfm and replays the stored handshake to it.
func (handover *stdioHandover) start() (*handoverChild, error) {
	image, err := binwatch.ImageAt(handover.options.Image.Path)
	if err != nil {
		return nil, err
	}
	handover.declined = &image
	argv := append([]string{image.Path}, handover.options.Args...)
	process, err := handover.options.Runner.Start(context.Background(), argv, deps.StartOptions{
		Env:        deps.EnvironmentWith(paths.EnvMCPHandoverChild, "1"),
		Stderr:     handover.warnings,
		StdinPipe:  true,
		StdoutPipe: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start %q: %w", argv, err)
	}
	child := &handoverChild{handover: handover, image: image, process: process, pending: map[string]bool{}}
	if err := child.open(); err != nil {
		child.stop()
		return nil, err
	}
	initialize, initialized := handover.handshake.snapshot()
	if len(initialize) != 0 {
		if err := child.replay(initialize); err != nil {
			child.stop()
			return nil, err
		}
	}
	if len(initialized) != 0 {
		if err := child.write(initialized); err != nil {
			child.stop()
			return nil, fmt.Errorf("replay notifications/initialized: %w", err)
		}
	}
	handover.declined = nil
	handover.mutex.Lock()
	handover.children = append(handover.children, child)
	handover.mutex.Unlock()
	return child, nil
}

// closeInput passes the client's end of input on to every child.
func (handover *stdioHandover) closeInput() {
	handover.mutex.Lock()
	children := append([]*handoverChild(nil), handover.children...)
	handover.mutex.Unlock()
	for _, child := range children {
		child.closeStdin()
	}
}

// handed reports whether an installed pfm serves this chat now.
func (handover *stdioHandover) handed() bool {
	handover.mutex.Lock()
	defer handover.mutex.Unlock()
	return handover.current != nil
}

// finish waits for the child serving now and returns how it ended; nil when
// no hand-over happened or it exited 0.
func (handover *stdioHandover) finish() error {
	handover.mutex.Lock()
	current := handover.current
	handover.ended = true
	handover.mutex.Unlock()
	if current == nil {
		return nil
	}
	<-current.exited
	if current.code != 0 {
		return &HandoverExitError{Code: current.code}
	}
	return nil
}

// exited ends the server when the child serving it ends on its own.
func (handover *stdioHandover) exited(child *handoverChild) {
	handover.mutex.Lock()
	serving := handover.current == child
	handover.mutex.Unlock()
	if serving {
		handover.end()
	}
}

func (handover *stdioHandover) warn(format string, args ...any) {
	fmt.Fprintf(handover.warnings, "pfm mcp stdio: "+format+"\n", args...)
}

// OwnHandover is the hand-over a stdio server started from this executable
// arms: nil for the installed pfm a hand-over started (EnvMCPHandoverChild),
// which serves and never moves itself, or when this executable cannot be read,
// said on stderr.
func OwnHandover(args []string, runner deps.Runner, stderr io.Writer) *Handover {
	if (paths.OSEnv{}).Get(paths.EnvMCPHandoverChild) == "1" {
		return nil
	}
	image, err := binwatch.OwnImage()
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp serve --stdio: cannot watch own executable (%v); "+
			"a new install will NOT reach this chat's MCP tools until the chat restarts\n", err)
		return nil
	}
	return &Handover{Image: image, Args: args, Runner: runner}
}
