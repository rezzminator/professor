package run

import (
	"bytes"
	"fmt"
)

// boundedBuffer keeps a limited stream's last maxCapturedOutput bytes: an engine's answer, its
// terminal event and its error line come last, so an overflow costs the oldest output only. The
// bytes.Buffer is a field, not an embed: a promoted ReadFrom let exec's io.Copy bypass Write.
type boundedBuffer struct {
	data      bytes.Buffer
	truncated bool
	limited   bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if !buffer.limited {
		return buffer.data.Write(value)
	}
	written := len(value)
	if len(value) >= maxCapturedOutput {
		buffer.data.Reset()
		value = value[len(value)-maxCapturedOutput:]
		buffer.truncated = true
	} else if over := buffer.data.Len() + len(value) - maxCapturedOutput; over > 0 {
		buffer.data.Next(over)
		buffer.truncated = true
	}
	_, _ = buffer.data.Write(value)
	return written, nil
}

func (buffer *boundedBuffer) String() string { return buffer.data.String() }

// noteOverflow names a stream that outgrew the capture limit among the run's diagnostics.
func (buffer *boundedBuffer) noteOverflow(name string, result *Result) {
	if buffer.truncated {
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf(
			"%s exceeded the %d-byte capture limit; only its last %d bytes were kept",
			name, maxCapturedOutput, maxCapturedOutput,
		))
	}
}
