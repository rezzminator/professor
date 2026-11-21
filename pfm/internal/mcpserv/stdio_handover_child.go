package mcpserv

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// handoverChild is one installed pfm a stdio server carries frames to. pending
// holds the ids of the client requests it has not answered yet, so a child an
// install replaced again keeps its input until the last one is answered.
type handoverChild struct {
	handover  *stdioHandover
	image     binwatch.Image
	process   deps.Process
	stdin     io.WriteCloser
	lines     chan []byte
	exited    chan struct{}
	code      int
	closeOnce sync.Once
	mutex     sync.Mutex
	pending   map[string]bool
	retiring  bool
}

// handoverReply is the part of a child's output frame that says what it answers.
type handoverReply struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Error  json.RawMessage `json:"error"`
}

func (child *handoverChild) open() error {
	stdin, err := child.process.StdinPipe()
	if err != nil {
		return fmt.Errorf("open the installed pfm's input: %w", err)
	}
	child.stdin = stdin
	stdout, err := child.process.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open the installed pfm's output: %w", err)
	}
	child.lines = make(chan []byte, 16)
	child.exited = make(chan struct{})
	go handoverLines(stdout, child.lines)
	return nil
}

// replay sends the client's initialize and swallows the child's answer to it:
// the client already holds the answer the image it started on gave. The child's
// output reaches the client through pump, started once the child serves.
func (child *handoverChild) replay(initialize []byte) error {
	var request proxyFrame
	if err := json.Unmarshal(initialize, &request); err != nil {
		return fmt.Errorf("decode the stored initialize: %w", err)
	}
	want, ok := proxyRequestKey(request.ID)
	if !ok {
		return fmt.Errorf("decode the stored initialize: id %s is no request id", request.ID)
	}
	if err := child.write(initialize); err != nil {
		return fmt.Errorf("replay initialize: %w", err)
	}
	timer := handoverClock.NewTimer(handoverReplayBound)
	defer timer.Stop()
	for {
		select {
		case line, open := <-child.lines:
			if !open {
				return fmt.Errorf("replay initialize: %w", errHandoverChildEnded)
			}
			var reply handoverReply
			if json.Unmarshal(line, &reply) != nil || reply.Method != "" {
				child.forward(line)
				continue
			}
			if got, ok := proxyRequestKey(reply.ID); !ok || got != want {
				child.forward(line)
				continue
			}
			if len(reply.Error) != 0 && string(reply.Error) != "null" {
				return fmt.Errorf("replay initialize: the installed pfm answered %s", bytes.TrimSpace(line))
			}
			return nil
		case <-timer.C():
			return fmt.Errorf("replay initialize: no answer within %v", handoverReplayBound)
		}
	}
}

// send carries one client frame to the child; a request it could not take is
// answered with an error, so the client never waits on it.
func (child *handoverChild) send(frame []byte, envelope proxyFrame) {
	key, request := "", false
	if envelope.Method != "" {
		key, request = proxyRequestKey(envelope.ID)
	}
	if request {
		child.mutex.Lock()
		child.pending[key] = true
		child.mutex.Unlock()
	}
	err := child.write(frame)
	if err == nil {
		return
	}
	child.handover.warn("carry a frame to the installed pfm %s: %v", child.image.Path, err)
	if !request {
		return
	}
	child.settle(key)
	cause := fmt.Sprintf("the installed pfm %s did not take the request: %v", child.image.Path, err)
	if err := writeErrorResponse(child.handover.output, envelope.ID, proxyInternalError, cause); err != nil {
		child.handover.warn("%v", err)
	}
}

func (child *handoverChild) write(frame []byte) error {
	if !bytes.HasSuffix(frame, []byte("\n")) {
		frame = append(append([]byte(nil), frame...), '\n')
	}
	_, err := child.stdin.Write(frame)
	return err
}

// pump carries the child's output to the client until it ends, then records
// how the child exited.
func (child *handoverChild) pump() {
	for line := range child.lines {
		child.forward(line)
		var reply handoverReply
		if json.Unmarshal(line, &reply) == nil && reply.Method == "" {
			if key, ok := proxyRequestKey(reply.ID); ok {
				child.settle(key)
			}
		}
	}
	child.code = 0
	if err := child.process.Wait(); err != nil {
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			child.code = coded.ExitCode()
		} else {
			child.handover.warn("wait for the installed pfm %s: %v", child.image.Path, err)
			child.code = 1
		}
	}
	close(child.exited)
	child.handover.exited(child)
}

func (child *handoverChild) forward(line []byte) {
	if !bytes.HasSuffix(line, []byte("\n")) {
		line = append(line, '\n')
	}
	if _, err := child.handover.output.Write(line); err != nil {
		child.handover.warn("carry the installed pfm's answer to the client: %v", err)
	}
}

// retire ends the child's input once it has answered every request it holds.
func (child *handoverChild) retire() {
	child.mutex.Lock()
	child.retiring = true
	drained := len(child.pending) == 0
	child.mutex.Unlock()
	if drained {
		child.closeStdin()
	}
}

func (child *handoverChild) closeStdin() {
	child.closeOnce.Do(func() {
		if err := child.stdin.Close(); err != nil {
			child.handover.warn("close the installed pfm's input: %v", err)
		}
	})
}

// stop ends a child whose hand-over failed and reaps it.
func (child *handoverChild) stop() {
	if child.stdin != nil {
		child.closeStdin()
	}
	if err := child.process.Kill(); err != nil {
		child.handover.warn("stop the installed pfm %s: %v", child.image.Path, err)
	}
	go func() {
		if child.lines != nil {
			for range child.lines {
			}
		}
		_ = child.process.Wait() // killed above: its status says nothing more
	}()
}

// handoverLines feeds reader's lines to lines until it ends, then closes lines.
func handoverLines(reader io.Reader, lines chan<- []byte) {
	defer close(lines)
	buffered := bufio.NewReaderSize(reader, 64<<10)
	for {
		line, err := buffered.ReadBytes('\n')
		if len(line) > 0 {
			lines <- line
		}
		if err != nil {
			return
		}
	}
}

var errHandoverChildEnded = errors.New("the installed pfm ended before it answered")

var handoverClock clock.Clock = clock.Real
