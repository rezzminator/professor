package obs

import (
	"context"
	"io"
	"strings"
)

// WarnLines returns a writer that records every non-empty line written to it
// as one WARN record named event, the line in FieldErr: a surface whose stderr
// nobody reads (a detached worker) still leaves its diagnostics in the
// activity log. Every write succeeds; the record is the destination.
func WarnLines(ctx context.Context, event string) io.Writer {
	return warnLines{ctx: ctx, event: event}
}

type warnLines struct {
	ctx   context.Context
	event string
}

func (writer warnLines) Write(data []byte) (int, error) {
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			Logger(writer.ctx).Warn(writer.event, FieldErr, line)
		}
	}
	return len(data), nil
}
