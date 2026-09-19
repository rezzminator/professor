package mcpserv

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var parseErrorFrame = []byte(
	`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}` + "\n",
)

// RunStdio serves through the official SDK IO transport while keeping a
// syntactically malformed newline frame from terminating the whole server.
// MCP's stdio wire format is newline-delimited JSON, so invalid lines can be
// answered and discarded before the SDK's JSON-RPC connection sees them.
func (service *Service) RunStdio(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
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
	err := service.Run(ctx, &mcp.IOTransport{
		Reader: reader,
		Writer: serialized,
	})
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
