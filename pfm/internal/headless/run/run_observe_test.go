package run

import (
	"bytes"
	"context"
	"sync"
	"time"
)

// armedDeadlineContext is a context.Context whose Err() reports
// context.DeadlineExceeded only once arm() is called. It lets a test decide
// the exact instant a "timeout" fires instead of racing a fixed wall-clock
// duration against a child process's fork + pid-file write, while still
// exercising the same ctx.Done()-driven process-group kill path a real
// request.Timeout deadline would.
type armedDeadlineContext struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newArmedDeadlineContext() *armedDeadlineContext {
	return &armedDeadlineContext{done: make(chan struct{})}
}

func (c *armedDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *armedDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *armedDeadlineContext) Value(any) any { return nil }

func (c *armedDeadlineContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *armedDeadlineContext) arm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = context.DeadlineExceeded
		close(c.done)
	}
}

type observedWriter struct {
	mu    sync.Mutex
	data  bytes.Buffer
	first chan struct{}
	once  sync.Once
}

func (writer *observedWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	_, _ = writer.data.Write(value)
	writer.once.Do(func() { close(writer.first) })
	return len(value), nil
}

func (writer *observedWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.data.String()
}
