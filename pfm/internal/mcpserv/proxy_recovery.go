package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// proxyDiscoverMethod is the probe Claude Code sends before initialize. The
	// daemon refuses it with HTTP 400, so the proxy answers it itself: a local
	// method-not-found sends the client straight on to initialize.
	proxyDiscoverMethod     = "server/discover"
	proxyMethodNotFound     = -32601
	proxyInternalError      = -32603
	proxyUninitializedError = "invalid during session initialization"
)

// proxySessionLostError is the daemon's 404 for a session it no longer holds
// (a restarted daemon, a closed session): the handshake replays and the
// request retries.
type proxySessionLostError struct {
	status int
	text   string
}

func (failure proxySessionLostError) Error() string {
	return fmt.Sprintf("daemon lost the session: HTTP %d: %s", failure.status, failure.text)
}

// proxyRejectedError is any other non-2xx answer: the daemon is up and refused
// this request. It is the request's answer — no retry, and the session stays.
type proxyRejectedError struct {
	address string
	status  int
	text    string
}

func (failure proxyRejectedError) Error() string {
	return fmt.Sprintf(
		"pfm MCP daemon %s rejected the request: HTTP %d: %s", failure.address, failure.status, failure.text,
	)
}

func (failure proxyRejectedError) code() int {
	if failure.status == http.StatusBadRequest && strings.Contains(failure.text, "not handled") {
		return proxyMethodNotFound
	}
	return proxyInternalError
}

func (proxy *stdioProxy) daemonStatusError(status int, body []byte) error {
	text := strings.TrimSpace(string(body))
	if status == http.StatusNotFound && strings.Contains(text, "session") {
		return proxySessionLostError{status: status, text: text}
	}
	return proxyRejectedError{address: proxy.address, status: status, text: text}
}

func isProxyHandshake(method string) bool {
	return method == proxyInitializeMethod || method == "notifications/initialized"
}

// uninitializedSessionResponse reports the daemon's 200 answer from a session
// that never saw initialize: the proxy's session is gone even though HTTP succeeded.
func uninitializedSessionResponse(response []byte) bool {
	var envelope struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Error == nil {
		return false
	}
	return strings.Contains(envelope.Error.Message, proxyUninitializedError)
}

// restoreSession replays the stored handshake whenever no session is held,
// whatever the generation, so no request is ever posted session-less. The
// reinit mutex makes concurrent callers replay it once.
func (proxy *stdioProxy) restoreSession(ctx context.Context) (uint64, error) {
	if session := proxy.session(); session.sessionID != "" || session.protocol != "" {
		return session.generation, nil
	}
	proxy.reinitMutex.Lock()
	defer proxy.reinitMutex.Unlock()
	if session := proxy.session(); session.sessionID != "" || session.protocol != "" {
		return session.generation, nil
	}
	return proxy.reinitializeLocked(ctx)
}

// retryExhausted names why the retry window closed: a daemon that kept losing
// the session is up, so only a connection failure reads as unreachable.
func (proxy *stdioProxy) retryExhausted(lastErr error) error {
	var lost proxySessionLostError
	if errors.As(lastErr, &lost) {
		return fmt.Errorf(
			"pfm MCP daemon %s kept losing the session for %s; the request was not delivered: %w",
			proxy.address, proxy.retryWindow, lastErr,
		)
	}
	return fmt.Errorf(
		"pfm MCP daemon %s stayed unreachable for %s; start it with `pfm mcp serve` and retry: %w",
		proxy.address, proxy.retryWindow, lastErr,
	)
}

func (proxy *stdioProxy) answerForwardFailure(output io.Writer, id json.RawMessage, cause error) {
	var rejected proxyRejectedError
	if errors.As(cause, &rejected) {
		proxy.answerError(output, id, rejected.code(), cause.Error())
		return
	}
	proxy.answerFailure(output, id, cause)
}
