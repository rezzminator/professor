package mcpserv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/stale"
)

const (
	proxyInitializeMethod = "initialize"
	proxyRetryWindow      = 45 * time.Second
	proxyRetryDelay       = 100 * time.Millisecond
	proxyCloseTimeout     = 300 * time.Millisecond
	// A capture may return maxCaptureBytes in both the MCP text result and its
	// structured payload. Each input byte may expand to a six-byte JSON escape.
	proxyMaxResponse = maxCaptureBytes*18 + (1 << 20)
)

type stdioProxy struct {
	address                 string
	endpoint                string
	sidDir                  string
	warnings                io.Writer
	identity                *ProxyIdentity
	client                  *http.Client
	clock                   clock.Clock
	retryWindow             time.Duration
	retryDelay              time.Duration
	initialize, initialized []byte
	sessionID, protocol     string
	sessionGeneration       uint64
	handshakeMutex          sync.Mutex
	sessionMutex            sync.RWMutex
	reinitMutex             sync.Mutex
	requestMutex            sync.Mutex
	requests                map[string]*proxyRequestState
	requestTails            map[string]*proxyRequestState
	expectedRuntimeIdentity string
}

type proxyFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type proxySession struct {
	sessionID, protocol string
	generation          uint64
}

type proxyPostResult struct {
	response   []byte
	generation uint64
}

type uncertainProxyDeliveryError struct {
	cause error
}

func (failure uncertainProxyDeliveryError) Error() string { return failure.cause.Error() }

func (failure uncertainProxyDeliveryError) Unwrap() error { return failure.cause }

// proxyWhoami is the caller-identity resolver's dependencies; production
// leaves it empty (the process's own environment and tmux), a test that drives
// RunStdio end to end substitutes a deterministic caller.
var proxyWhoami resolve.WhoamiDependencies

func newStdioProxy(ctx context.Context, address string, warnings io.Writer) *stdioProxy {
	if warnings == nil {
		warnings = os.Stderr
	}
	proxy := &stdioProxy{
		address:      address,
		endpoint:     "http://" + address + pfmconfig.MCPPathProfessor,
		warnings:     warnings,
		client:       obs.WrapClient(&http.Client{Transport: http.DefaultTransport}),
		clock:        clock.Real,
		retryWindow:  proxyRetryWindow,
		retryDelay:   proxyRetryDelay,
		requests:     make(map[string]*proxyRequestState),
		requestTails: make(map[string]*proxyRequestState),
	}
	identifier, err := resolve.NewWhoami(proxyWhoami)
	if err != nil {
		proxy.warn("build caller identity resolver: %v; forwarding without _meta.pfmProxy", err)
		return proxy
	}
	identity, err := identifier.Identify(ctx)
	if err != nil {
		proxy.warn("resolve caller identity: %v; forwarding without _meta.pfmProxy", err)
		return proxy
	}
	resolved, resolveErr := paths.Resolve()
	if resolveErr == nil {
		proxy.sidDir = resolved.SIDDir
	}
	if identity.ID == "" {
		if resolveErr != nil {
			proxy.warn("resolve caller transcript paths: %v; forwarding seat without transcript id", resolveErr)
		} else {
			id, _, crumbErr := reload.SessionFromCrumb(
				resolved.SIDDir,
				identity.SocketName,
				identity.Pane,
			)
			if crumbErr != nil {
				proxy.warn("resolve caller transcript crumb: %v; forwarding seat without transcript id", crumbErr)
			} else {
				identity.ID = id
			}
		}
	}
	proxy.identity = &ProxyIdentity{
		Version: ProxyWireVersion, Session: identity.Session, SocketPath: identity.SocketPath,
		SocketName: identity.SocketName, Pane: identity.Pane, Engine: identity.Engine,
		ID: identity.ID, Source: identity.Source,
	}
	return proxy
}

func (proxy *stdioProxy) run(ctx context.Context, input io.Reader, output io.Writer) error {
	returned := make(chan error, 1)
	go func() { returned <- proxy.read(ctx, input, output) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-returned:
		return err
	}
}

func (proxy *stdioProxy) read(ctx context.Context, input io.Reader, output io.Writer) error {
	requestCtx, cancelRequests := context.WithCancel(ctx)
	var pending sync.WaitGroup
	var pendingAdmission *proxyRequestState
	defer func() {
		cancelRequests()
		pending.Wait()
		proxy.closeSession()
	}()
	buffered := bufio.NewReader(input)
	for {
		frame, err := buffered.ReadBytes('\n')
		if len(frame) > 0 {
			frame = bytes.TrimSpace(frame)
			if len(frame) > 0 {
				var envelope proxyFrame
				decodeErr := json.Unmarshal(frame, &envelope)
				var cancellationState *proxyRequestState
				if decodeErr == nil {
					proxy.resolvePendingAdmission(pendingAdmission, envelope)
					pendingAdmission = nil
				}
				switch {
				case decodeErr != nil:
					proxy.warn("decode validated client frame: %v", decodeErr)
				case envelope.Method == proxyInitializeMethod || envelope.Method == "notifications/initialized":
					// Handshake stays ordered; later traffic may overlap.
					proxy.forward(requestCtx, frame, output)
				case len(envelope.ID) != 0:
					state, registered := proxy.registerRequest(requestCtx, envelope.ID)
					if !registered {
						proxy.answerFailure(output, envelope.ID, fmt.Errorf("invalid JSON-RPC request id"))
						continue
					}
					if state.admissionTimer != nil {
						pendingAdmission = state
					}
					pending.Add(1)
					go func(content []byte, request *proxyRequestState) {
						defer pending.Done()
						proxy.forwardRequest(content, output, request)
					}(append([]byte(nil), frame...), state)
				case envelope.Method == "notifications/cancelled":
					var completed bool
					completed, cancellationState = proxy.cancelRequest(envelope, output)
					if completed {
						break
					}
					fallthrough
				default:
					pending.Add(1)
					go func(content []byte, cancellation *proxyRequestState) {
						defer pending.Done()
						proxy.forward(requestCtx, content, output)
						if cancellation != nil {
							proxy.completeCancellation(cancellation)
						}
					}(append([]byte(nil), frame...), cancellationState)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read stdio proxy frame: %w", err)
		}
	}
}

// runStdioTransport forwards to the daemon's config.MCPPathProfessor when it
// mounts every locally enabled family and, when chat is enabled, runs the
// same chat runtime; otherwise it serves the combined server in process and
// says why on the warnings writer.
func (professor *Professor) runStdioTransport(
	ctx context.Context,
	reader io.ReadCloser,
	serialized io.WriteCloser,
	options StdioOptions,
) (returnErr error) {
	warnings := options.Warnings
	if warnings == nil {
		warnings = os.Stderr
	}
	const consequence = "if `pfm install` replaces this binary, this chat's MCP tools fail until the chat restarts"
	inProcess := func(reason string) error {
		fmt.Fprintf(warnings, "pfm mcp stdio: %s; using in-process MCP; %s\n", reason, consequence)
		return professor.combined.Run(ctx, &mcp.IOTransport{Reader: reader, Writer: serialized})
	}
	address := options.DaemonAddress
	if address == "" {
		return inProcess("daemon address missing")
	}
	status, probeErr := ProbeDaemon(address)
	if errors.Is(probeErr, ErrDaemonAbsent) {
		return inProcess(fmt.Sprintf("daemon absent at %s (%v)", address, probeErr))
	}
	if probeErr != nil {
		return inProcess(fmt.Sprintf("foreign service at %s (%v)", address, probeErr))
	}
	for _, family := range slices.Sorted(maps.Keys(professor.servers)) {
		if _, mounted := status.Servers[family]; !mounted {
			return inProcess(fmt.Sprintf("family %s not mounted by daemon at %s", family, address))
		}
	}
	if professor.chat != nil && (status.ChatRuntimeIdentity == "" ||
		status.ChatRuntimeIdentity != professor.chat.RuntimeIdentity()) {
		return inProcess("runtime mismatch with daemon at " + address)
	}
	if routeErr := probeProfessorRoute(ctx, address); routeErr != nil {
		return inProcess(fmt.Sprintf("daemon at %s serves no %s (%v)", address, pfmconfig.MCPPathProfessor, routeErr))
	}
	marker, markerErr := stale.HoldCompatibleProxy(options.Home)
	if markerErr != nil {
		return fmt.Errorf("protect selected daemon stdio proxy: %w", markerErr)
	}
	defer func() {
		if closeErr := marker.Close(); closeErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close compatible proxy marker %s: %w", marker.Name(), closeErr),
			)
		}
	}()
	proxy := newStdioProxy(ctx, address, warnings)
	if professor.chat != nil {
		proxy.expectedRuntimeIdentity = professor.chat.RuntimeIdentity()
		proxy.sidDir = options.SIDDir
	}
	return proxy.run(ctx, reader, serialized)
}

// probeProfessorRoute asks the daemon whether it mounts config.MCPPathProfessor.
// A daemon still running a build from before the professor server answers a
// healthy /status with the same families but 404 on this route; every other
// answer (the MCP handler refusing a bare GET included) means the route is
// there. A probe that could not complete is an error, never "mounted".
func probeProfessorRoute(ctx context.Context, address string) error {
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	endpoint := "http://" + address + pfmconfig.MCPPathProfessor
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("build route probe for %s: %w", endpoint, err)
	}
	response, err := obs.WrapClient(&http.Client{}).Do(request)
	if err != nil {
		return fmt.Errorf("probe %s: %w", endpoint, err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		return fmt.Errorf("close route probe response from %s: %w", endpoint, closeErr)
	}
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s answered HTTP 404", endpoint)
	}
	return nil
}

func (proxy *stdioProxy) forward(ctx context.Context, raw []byte, output io.Writer) {
	var frame proxyFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		proxy.warn("decode validated client frame: %v", err)
		return
	}
	if frame.Method == proxyInitializeMethod || frame.Method == "notifications/initialized" {
		proxy.storeHandshake(frame.Method, raw)
	}
	forwarded := raw
	if frame.Method == "tools/call" && proxy.identity != nil {
		identity := *proxy.identity
		explicitThreadID, err := proxyCallHasThreadID(raw)
		if err != nil {
			proxy.answerFailure(output, frame.ID, fmt.Errorf("inspect caller thread metadata: %w", err))
			return
		}
		engine, engineErr := pfmengine.Parse(identity.Engine)
		if !explicitThreadID && engineErr == nil && engine == pfmengine.Claude {
			if !filepath.IsAbs(proxy.sidDir) {
				proxy.answerFailure(
					output,
					frame.ID,
					fmt.Errorf("refresh Claude caller transcript: breadcrumb directory is not absolute"),
				)
				return
			}
			identity.ID, _, err = reload.SessionFromCrumb(proxy.sidDir, identity.SocketName, identity.Pane)
			if err != nil {
				proxy.answerFailure(output, frame.ID, fmt.Errorf("refresh Claude caller transcript: %w", err))
				return
			}
		}
		withIdentity, err := addProxyIdentity(raw, identity)
		if err != nil {
			proxy.answerFailure(output, frame.ID, fmt.Errorf("attach caller identity: %w", err))
			return
		}
		forwarded = withIdentity
	}
	response, err := proxy.sendWithRetry(ctx, forwarded, frame.Method == proxyInitializeMethod)
	if err != nil {
		if len(frame.ID) == 0 {
			proxy.warn(
				"dropped notification %q after pfm MCP daemon %s was unreachable for %s: %v",
				frame.Method,
				proxy.address,
				proxy.retryWindow,
				err,
			)
			return
		}
		proxy.answerFailure(output, frame.ID, err)
		return
	}
	if len(response) == 0 {
		return
	}
	if _, err := output.Write(append(response, '\n')); err != nil {
		proxy.warn("write daemon response to stdio: %v", err)
	}
}

func proxyCallHasThreadID(raw []byte) (bool, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(raw, &request); err != nil {
		return false, err
	}
	var params map[string]json.RawMessage
	if value := request["params"]; len(value) != 0 {
		if err := json.Unmarshal(value, &params); err != nil {
			return false, fmt.Errorf("decode params: %w", err)
		}
	}
	if params == nil {
		return false, nil
	}
	var meta map[string]json.RawMessage
	if value := params["_meta"]; len(value) != 0 {
		if err := json.Unmarshal(value, &meta); err != nil {
			return false, fmt.Errorf("decode params _meta: %w", err)
		}
	}
	_, present := meta["threadId"]
	return present, nil
}

func addProxyIdentity(raw []byte, identity ProxyIdentity) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var params map[string]json.RawMessage
	if value := request["params"]; len(value) != 0 {
		if err := json.Unmarshal(value, &params); err != nil {
			return nil, fmt.Errorf("decode params: %w", err)
		}
	}
	if params == nil {
		params = make(map[string]json.RawMessage)
	}
	var meta map[string]json.RawMessage
	if value := params["_meta"]; len(value) != 0 {
		if err := json.Unmarshal(value, &meta); err != nil {
			return nil, fmt.Errorf("decode params _meta: %w", err)
		}
	}
	if meta == nil {
		meta = make(map[string]json.RawMessage)
	}
	payload, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode proxy identity: %w", err)
	}
	meta["pfmProxy"] = payload
	params["_meta"], err = json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("encode params _meta: %w", err)
	}
	request["params"], err = json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode params: %w", err)
	}
	return json.Marshal(request)
}

func (proxy *stdioProxy) sendWithRetry(ctx context.Context, frame []byte, isInitialize bool) ([]byte, error) {
	retryCtx, cancel := context.WithTimeout(ctx, proxy.retryWindow)
	defer cancel()
	var lastErr error
	needsReinitialize := false
	var recoveryGeneration uint64
	for attempt := 0; ; attempt++ {
		var result proxyPostResult
		var err error
		if proxy.expectedRuntimeIdentity != "" {
			status, probeErr := ProbeDaemon(proxy.address)
			if probeErr != nil {
				err = fmt.Errorf("verify daemon runtime before replay: %w", probeErr)
			} else if status.ChatRuntimeIdentity == "" || status.ChatRuntimeIdentity != proxy.expectedRuntimeIdentity {
				return nil, fmt.Errorf(
					"pfm MCP daemon %s runtime mismatch during recovery; request was not replayed",
					proxy.address,
				)
			}
		}
		if attempt > 0 && !isInitialize && needsReinitialize {
			result.generation, err = proxy.reinitializeCurrent(retryCtx, recoveryGeneration)
		}
		if err == nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			result, err = proxy.post(ctx, frame)
			if err == nil {
				return result.response, nil
			}
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		var uncertain uncertainProxyDeliveryError
		if errors.As(err, &uncertain) {
			return nil, proxy.uncertainDelivery(err)
		}
		lastErr = err
		needsReinitialize = proxy.clearSession(result.generation)
		if needsReinitialize {
			recoveryGeneration = proxy.session().generation
		}
		timer := proxy.clock.NewTimer(proxy.retryDelay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			return nil, fmt.Errorf(
				"pfm MCP daemon %s stayed unreachable for %s; start it with `pfm mcp serve` and retry: %w",
				proxy.address,
				proxy.retryWindow,
				lastErr,
			)
		case <-timer.C():
		}
	}
}

func (proxy *stdioProxy) uncertainDelivery(cause error) error {
	return fmt.Errorf(
		"lost the response from pfm MCP daemon %s; the request may have arrived, so it was not replayed; check the daemon, then retry deliberately: %w",
		proxy.address,
		cause,
	)
}

func (proxy *stdioProxy) storeHandshake(method string, frame []byte) {
	proxy.handshakeMutex.Lock()
	defer proxy.handshakeMutex.Unlock()
	if method == proxyInitializeMethod {
		proxy.initialize = append(proxy.initialize[:0], frame...)
		return
	}
	proxy.initialized = append(proxy.initialized[:0], frame...)
}

func (proxy *stdioProxy) handshakeSnapshot() ([]byte, []byte) {
	proxy.handshakeMutex.Lock()
	defer proxy.handshakeMutex.Unlock()
	return append([]byte(nil), proxy.initialize...), append([]byte(nil), proxy.initialized...)
}

func (proxy *stdioProxy) reinitialize(ctx context.Context) error {
	proxy.reinitMutex.Lock()
	defer proxy.reinitMutex.Unlock()
	_, err := proxy.reinitializeLocked(ctx)
	return err
}

func (proxy *stdioProxy) reinitializeCurrent(ctx context.Context, generation uint64) (uint64, error) {
	proxy.reinitMutex.Lock()
	defer proxy.reinitMutex.Unlock()
	session := proxy.session()
	if session.generation != generation || session.sessionID != "" || session.protocol != "" {
		return session.generation, nil
	}
	return proxy.reinitializeLocked(ctx)
}

func (proxy *stdioProxy) reinitializeLocked(ctx context.Context) (uint64, error) {
	initialize, initialized := proxy.handshakeSnapshot()
	if len(initialize) == 0 {
		return proxy.session().generation, nil
	}
	result, err := proxy.post(ctx, initialize)
	if err != nil {
		return result.generation, fmt.Errorf("re-initialize daemon session: %w", err)
	}
	if len(initialized) != 0 {
		result, err = proxy.post(ctx, initialized)
		if err != nil {
			return result.generation, fmt.Errorf("restore initialized notification: %w", err)
		}
	}
	return result.generation, nil
}

func (proxy *stdioProxy) post(ctx context.Context, frame []byte) (proxyPostResult, error) {
	session := proxy.session()
	result := proxyPostResult{generation: session.generation}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy.endpoint, bytes.NewReader(frame))
	if err != nil {
		return result, fmt.Errorf("build daemon request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if session.sessionID != "" {
		request.Header.Set("Mcp-Session-Id", session.sessionID)
	}
	if session.protocol != "" {
		request.Header.Set("Mcp-Protocol-Version", session.protocol)
	}
	response, err := proxy.client.Do(request)
	if err != nil {
		failure := fmt.Errorf("POST %s: %w", proxy.endpoint, err)
		if ctx.Err() != nil || retryableConnectionFailure(err) {
			return result, failure
		}
		return result, uncertainProxyDeliveryError{cause: failure}
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			proxy.warn("close daemon response: %v", closeErr)
		}
	}()
	body, err := io.ReadAll(io.LimitReader(response.Body, proxyMaxResponse+1))
	if err != nil {
		return result, uncertainProxyDeliveryError{cause: fmt.Errorf("read daemon response: %w", err)}
	}
	if len(body) > proxyMaxResponse {
		return result, uncertainProxyDeliveryError{
			cause: fmt.Errorf("daemon response exceeds %d bytes", proxyMaxResponse),
		}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("daemon answered HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	responseSessionID := response.Header.Get("Mcp-Session-Id")
	if len(bytes.TrimSpace(body)) == 0 {
		proxy.updateSession(result.generation, responseSessionID, "")
		var outgoing proxyFrame
		if err := json.Unmarshal(frame, &outgoing); err != nil {
			return result, uncertainProxyDeliveryError{cause: fmt.Errorf("decode delivered request: %w", err)}
		}
		if len(outgoing.ID) != 0 {
			return result, uncertainProxyDeliveryError{cause: fmt.Errorf("daemon answered an empty response")}
		}
		return result, nil
	}
	contentType := strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0])
	if contentType != "application/json" {
		proxy.updateSession(result.generation, responseSessionID, "")
		return result, uncertainProxyDeliveryError{
			cause: fmt.Errorf("daemon answered unsupported content type %q", contentType),
		}
	}
	body = bytes.TrimSpace(body)
	if !json.Valid(body) {
		proxy.updateSession(result.generation, responseSessionID, "")
		return result, uncertainProxyDeliveryError{cause: fmt.Errorf("daemon answered malformed JSON")}
	}
	proxy.updateSession(result.generation, responseSessionID, protocolFromResponse(body))
	result.response = body
	return result, nil
}

func retryableConnectionFailure(err error) bool {
	var network *net.OpError
	return errors.As(err, &network) && errors.Is(network.Err, syscall.ECONNREFUSED)
}

func (proxy *stdioProxy) closeSession() {
	session := proxy.session()
	if session.sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyCloseTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, proxy.endpoint, http.NoBody)
	if err != nil {
		proxy.warn("build daemon session close: %v", err)
		return
	}
	request.Header.Set("Mcp-Session-Id", session.sessionID)
	if session.protocol != "" {
		request.Header.Set("Mcp-Protocol-Version", session.protocol)
	}
	response, err := proxy.client.Do(request)
	if err != nil {
		proxy.warn("close daemon session %q: %v", session.sessionID, err)
		return
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			proxy.warn("close daemon session response: %v", closeErr)
		}
	}()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, proxyMaxResponse+1))
		if readErr != nil {
			proxy.warn("read daemon session close response: %v", readErr)
			return
		}
		proxy.warn(
			"close daemon session %q: HTTP %d: %s",
			session.sessionID,
			response.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}
	proxy.clearSession(session.generation)
}

func protocolFromResponse(response []byte) string {
	var envelope struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return ""
	}
	return envelope.Result.ProtocolVersion
}

func (proxy *stdioProxy) session() proxySession {
	proxy.sessionMutex.RLock()
	defer proxy.sessionMutex.RUnlock()
	return proxySession{sessionID: proxy.sessionID, protocol: proxy.protocol, generation: proxy.sessionGeneration}
}

func (proxy *stdioProxy) updateSession(generation uint64, sessionID, protocol string) {
	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()
	if generation != proxy.sessionGeneration {
		return
	}
	nextSessionID := proxy.sessionID
	if sessionID != "" {
		nextSessionID = sessionID
	}
	nextProtocol := proxy.protocol
	if protocol != "" {
		nextProtocol = protocol
	}
	if nextSessionID == proxy.sessionID && nextProtocol == proxy.protocol {
		return
	}
	proxy.sessionID = nextSessionID
	proxy.protocol = nextProtocol
	proxy.sessionGeneration++
}

func (proxy *stdioProxy) clearSession(generation uint64) bool {
	proxy.sessionMutex.Lock()
	defer proxy.sessionMutex.Unlock()
	if generation != proxy.sessionGeneration {
		return proxy.sessionID == "" && proxy.protocol == ""
	}
	proxy.sessionID = ""
	proxy.protocol = ""
	proxy.sessionGeneration++
	return true
}

func (proxy *stdioProxy) answerFailure(output io.Writer, id json.RawMessage, cause error) {
	proxy.answerError(output, id, -32000, cause.Error())
}

func (proxy *stdioProxy) answerError(output io.Writer, id json.RawMessage, code int, message string) {
	response := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id}
	response.Error.Code = code
	response.Error.Message = message
	encoded, err := json.Marshal(response)
	if err != nil {
		proxy.warn("encode daemon failure response: %v", err)
		return
	}
	if _, err := output.Write(append(encoded, '\n')); err != nil {
		proxy.warn("write daemon failure response: %v", err)
	}
}

func (proxy *stdioProxy) warn(format string, arguments ...any) {
	fmt.Fprintf(proxy.warnings, "pfm mcp stdio proxy: "+format+"\n", arguments...)
}
