package mcpserv

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"sync"
)

var parseErrorFrame = []byte(
	`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}` + "\n",
)

// StdioOptions is what the stdio server takes from the command that started
// it: the daemon it forwards to, the home holding the compatible-proxy marker,
// the Claude breadcrumb directory, and where its fallback warnings go (nil:
// stderr).
type StdioOptions struct {
	DaemonAddress string
	Home          string
	SIDDir        string
	Warnings      io.Writer
}

// RunStdio serves the professor server over stdio: it forwards to the daemon's
// config.MCPPathProfessor when that daemon is compatible, and serves the
// combined server in process otherwise. It reads through the official SDK IO
// transport while keeping a syntactically malformed newline frame from
// terminating the whole server. MCP's stdio wire format is newline-delimited
// JSON, so invalid lines can be answered and discarded before the SDK's
// JSON-RPC connection sees them.
func (professor *Professor) RunStdio(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	options StdioOptions,
) error {
	reader, writer := io.Pipe()
	serialized := &lockedWriteCloser{writer: output}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if err := writer.Close(); err != nil {
				log.Printf("mcp stdio: close request pipe: %v", err)
			}
		}()
		buffered := bufio.NewReaderSize(input, 64<<10)
		for {
			frame, err := buffered.ReadBytes('\n')
			if len(frame) > 0 {
				if json.Valid(frame) {
					if _, writeErr := writer.Write(frame); writeErr != nil {
						return
					}
				} else {
					_, _ = serialized.Write(parseErrorFrame)
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = writer.CloseWithError(err)
				}
				return
			}
		}
	}()
	err := professor.runStdioTransport(ctx, reader, serialized, options)
	_ = reader.Close()
	// A client holds input open for the whole session, so the frame reader may
	// never return; an ended context must not wait for it, or the
	// replaced-executable guard cancels and the process still never exits.
	select {
	case <-done:
	case <-ctx.Done():
	}
	return err
}

type lockedWriteCloser struct {
	mutex  sync.Mutex
	writer io.Writer
}

func (writer *lockedWriteCloser) Write(content []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.writer.Write(content)
}

func (*lockedWriteCloser) Close() error {
	return nil
}
