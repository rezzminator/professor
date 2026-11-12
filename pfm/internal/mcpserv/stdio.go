package mcpserv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

var parseErrorFrame = []byte(
	`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}` + "\n",
)

// StdioOptions is what the stdio server takes from the command that started
// it: the daemon it forwards to, the home holding the compatible-proxy marker,
// the Claude breadcrumb directory, and where its fallback warnings go (nil:
// stderr).
type StdioOptions struct {
	Family        string
	DaemonAddress string
	Home          string
	SIDDir        string
	Warnings      io.Writer
}

// RunStdio serves the professor server over stdio: it forwards to the daemon's
// config.MCPFamilyPath(config.MCPServerChat) when that daemon is compatible, and serves the
// selected family in process otherwise. It reads through the official SDK IO
// transport while keeping a syntactically malformed newline frame from
// terminating the whole server. MCP's stdio wire format is newline-delimited
// JSON, so invalid lines can be answered and discarded before the SDK's
// JSON-RPC connection sees them.
func (professor *FamilyServers) RunStdio(
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
				obs.Logger(ctx).Error("mcp.stdio_pipe_close_failed", obs.FieldErr, err.Error())
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

// attachingCallers is the in-process route's input: input's frames unchanged, each tools/call through
// attachCaller first, so the in-process server learns its caller as the daemon does. A frame
// attachCaller refuses is answered on output and never reaches the server. Closing the returned
// reader closes input too.
func (proxy *stdioProxy) attachingCallers(ctx context.Context, input io.ReadCloser, output io.Writer) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		buffered := bufio.NewReader(input)
		for {
			line, err := buffered.ReadBytes('\n')
			if frame := bytes.TrimSpace(line); len(frame) > 0 {
				var envelope proxyFrame
				if json.Unmarshal(frame, &envelope) == nil && proxy.isChatToolCall(envelope) {
					withIdentity, attachErr := proxy.attachCaller(ctx, frame)
					if attachErr != nil {
						proxy.answerFailure(output, envelope.ID, attachErr)
						line = nil
					} else {
						withIdentity = append(withIdentity, '\n')
						line = withIdentity
					}
				}
				if _, writeErr := writer.Write(line); writeErr != nil {
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				_ = writer.CloseWithError(err)
				return
			}
		}
	}()
	return closingBoth{Reader: reader, first: reader, second: input}
}

// closingBoth is a reader whose Close closes both the pipe it reads and the input feeding that pipe.
type closingBoth struct {
	io.Reader
	first, second io.Closer
}

func (both closingBoth) Close() error {
	return errors.Join(both.first.Close(), both.second.Close())
}

// attachCaller is the one door adding the caller's identity to a chat-family tools/call frame, as
// _meta.pfmProxy, on the daemon route and in process alike: the server answering the call cannot see
// whose pane it came from, so the stdio process, a child of the calling engine, resolves it here.
func (proxy *stdioProxy) attachCaller(ctx context.Context, raw []byte) ([]byte, error) {
	resolved, identifyErr := proxy.identify(ctx)
	identity := ProxyIdentity{
		Version:        ProxyWireVersion,
		Session:        resolved.Session,
		Backend:        resolved.Backend,
		SeatID:         resolved.SeatID,
		HerdrWorkspace: resolved.HerdrWorkspace,
		SocketPath:     resolved.SocketPath,
		SocketName:     resolved.SocketName,
		Pane:           resolved.Pane,
		Engine:         resolved.Engine,
		ID:             resolved.ID,
	}
	if identifyErr != nil {
		kind := proxyErrorKind(identifyErr)
		if kind != statusNotRegistered {
			identity = ProxyIdentity{Version: ProxyWireVersion}
		}
		identity.Error = &ProxyIdentityError{Kind: kind, Message: identifyErr.Error()}
	}
	explicitThreadID, err := proxyCallHasThreadID(raw)
	if err != nil {
		return nil, fmt.Errorf("inspect caller thread metadata: %w", err)
	}
	// Claude may change transcripts while keeping its seat.
	engine, engineErr := pfmengine.Parse(identity.Engine)
	if identifyErr == nil && !explicitThreadID && engineErr == nil &&
		engine == pfmengine.Claude {
		// The launch-time id goes stale on /clear: an unreadable crumb sends none, as a missing one
		// does, so the daemon binds from its own live row and a split pane's self refuses.
		refreshErr := fmt.Errorf("breadcrumb directory %q is not absolute", proxy.sidDir)
		identity.ID = ""
		if filepath.IsAbs(proxy.sidDir) {
			identity.ID, _, refreshErr = reload.SessionFromCrumb(proxy.sidDir, identity.SocketName, identity.Pane)
		}
		if refreshErr != nil {
			proxy.warn("refresh Claude caller transcript: %v; forwarding without a transcript id", refreshErr)
		}
	}
	withIdentity, err := addProxyIdentity(raw, identity)
	if err != nil {
		return nil, fmt.Errorf("attach caller identity: %w", err)
	}
	return withIdentity, nil
}
