package mockengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	openCodeSessionIDKey = "sessionID"
	openCodeMessageIDKey = "messageID"
	openCodeCostKey      = "cost"
	openCodeTokensKey    = "tokens"
	openCodeOutputKey    = "output"
	openCodeNameKey      = "name"
)

const openCodeSchemaRefusal = "mock-engine: opencode schema seeding is unpinned — the mock plays no StructuredOutput tool"

type openCodeServerState struct {
	mu sync.Mutex
	// scriptMu serializes the positional script's cursor: net/http runs each
	// message on its own goroutine, and the cursor lives in proc.script.
	scriptMu sync.Mutex
	next     int
	messages map[string][]byte
	sessions map[string]struct{}
	proc     *process
	engines  map[string]*openCodeSession
	plugins  *openCodePlugins
}

func openCodeServe(proc *process, call openCodeInvocation) int {
	host, port := call.hostname, call.port
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "0"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		warn(proc.stderr, "listen opencode server: %v", err)
		return ExitUsage
	}
	defer func() {
		if err := listener.Close(); err != nil {
			warn(proc.stderr, "close opencode listener: %v", err)
		}
	}()
	state := &openCodeServerState{proc: proc, messages: make(map[string][]byte)}
	defer state.close()
	server := &http.Server{Handler: state}
	if _, err := fmt.Fprintf(
		proc.stdout,
		"opencode server listening on http://%s\n",
		listener.Addr().String(),
	); err != nil {
		warn(proc.stderr, "announce opencode server: %v", err)
		return ExitUsage
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		warn(proc.stderr, "serve opencode HTTP: %v", err)
		return ExitUsage
	}
	return 0
}

func (state *openCodeServerState) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	proc := state.proc
	if password := proc.env("OPENCODE_SERVER_PASSWORD"); password != "" {
		username, supplied, ok := request.BasicAuth()
		if !ok || username != proc.env("OPENCODE_SERVER_USERNAME") || supplied != password {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	path := request.URL.Path
	switch {
	case request.Method == http.MethodGet && path == "/global/health":
		openCodeHTTPJSON(writer, map[string]bool{"healthy": true})
	case request.Method == http.MethodGet && path == "/experimental/tool/ids":
		if ready := proc.env("PFM_OPENCODE_PLUGIN_READY"); ready != "" {
			if err := os.WriteFile(ready, []byte("pfm-opencode-plugin-ready\n"), 0o600); err != nil {
				http.Error(writer, fmt.Sprintf("write plugin readiness: %v", err), http.StatusInternalServerError)
				return
			}
		}
		openCodeHTTPJSON(writer, []string{"bash", "read"})
	case request.Method == http.MethodPost && path == "/session":
		state.mu.Lock()
		state.next++
		id := fmt.Sprintf("ses_mock%08d", state.next)
		if state.sessions == nil {
			state.sessions = make(map[string]struct{})
		}
		state.sessions[id] = struct{}{}
		state.mu.Unlock()
		openCodeHTTPJSON(writer, map[string]string{"id": id})
	case strings.HasPrefix(path, "/session/"):
		parts := strings.Split(strings.TrimPrefix(path, "/session/"), "/")
		if request.Method == http.MethodDelete && len(parts) == 1 && parts[0] != "" {
			state.mu.Lock()
			_, exists := state.sessions[parts[0]]
			delete(state.sessions, parts[0])
			engineSession := state.engines[parts[0]]
			delete(state.engines, parts[0])
			state.mu.Unlock()
			if !exists {
				http.NotFound(writer, request)
				return
			}
			if engineSession != nil {
				if err := engineSession.notification(Step{NotificationType: "session.deleted"}); err != nil {
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}
				if err := engineSession.database.Close(); err != nil {
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			openCodeHTTPJSON(writer, true)
			return
		}
		if request.Method == http.MethodPost && len(parts) == 2 && parts[0] != "" && parts[1] == payloadMessage {
			state.message(writer, request, parts[0])
			return
		}
		if request.Method == http.MethodGet && len(parts) == 3 && parts[0] != "" && parts[1] == payloadMessage {
			state.mu.Lock()
			snapshot, ok := state.messages[parts[2]]
			state.mu.Unlock()
			if !ok {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			if _, err := writer.Write(snapshot); err != nil {
				warn(proc.stderr, "write opencode snapshot: %v", err)
			}
			return
		}
		http.NotFound(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func openCodeHTTPJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		http.Error(writer, fmt.Sprintf("encode opencode response: %v", err), http.StatusInternalServerError)
	}
}

func openCodeAttach(proc *process, call openCodeInvocation) int {
	prompt := call.prompt
	if prompt == "" {
		content, err := io.ReadAll(io.LimitReader(proc.stdin, 1<<20))
		if err != nil {
			warn(proc.stderr, "read opencode run prompt: %v", err)
			return ExitUsage
		}
		prompt = strings.TrimSpace(string(content))
	}
	if prompt == "" {
		warn(proc.stderr, "opencode run needs a prompt")
		return ExitUsage
	}
	endpoint, err := url.Parse(call.attach)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" {
		warn(proc.stderr, "invalid opencode attach address %q", call.attach)
		return ExitUsage
	}
	client := &http.Client{Timeout: 30 * time.Second}
	send := func(path string, payload any) ([]byte, int, error) {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("encode opencode attach body: %w", err)
		}
		target := *endpoint
		target.Path = path
		target.RawQuery = url.Values{openCodeDirectoryKey: []string{proc.cwd}}.Encode()
		request, err := http.NewRequestWithContext(proc.ctx, http.MethodPost, target.String(), bytes.NewReader(body))
		if err != nil {
			return nil, 0, fmt.Errorf("build opencode attach request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		if password := proc.env("OPENCODE_SERVER_PASSWORD"); password != "" {
			request.SetBasicAuth(proc.env("OPENCODE_SERVER_USERNAME"), password)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, 0, fmt.Errorf("send opencode attach request: %w", err)
		}
		defer func() {
			if err := response.Body.Close(); err != nil {
				warn(proc.stderr, "close opencode attach response: %v", err)
			}
		}()
		answer, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return nil, 0, fmt.Errorf("read opencode attach response: %w", err)
		}
		return answer, response.StatusCode, nil
	}
	data, status, err := send("/session", map[string]any{})
	if err != nil || status < 200 || status >= 300 {
		warn(proc.stderr, "create opencode attached session: status %d: %s: %v", status, data, err)
		return ExitUsage
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &session); err != nil || session.ID == "" {
		warn(proc.stderr, "decode opencode session: %v", err)
		return ExitUsage
	}
	data, status, err = send(
		"/session/"+url.PathEscape(session.ID)+"/message",
		map[string]any{openCodePartsKey: []map[string]string{{keyType: "text", blockText: prompt}}},
	)
	if err != nil {
		warn(proc.stderr, "submit opencode attached message: %v", err)
		return ExitUsage
	}
	if status == 500 {
		_, _ = fmt.Fprint(proc.stderr, string(data))
		_, code, found := strings.Cut(string(data), "crash step: exit ")
		if found {
			code, _, _ = strings.Cut(code, "\n")
			code, _, _ = strings.Cut(code, ":")
			if parsed, parseErr := strconv.Atoi(strings.TrimSpace(code)); parseErr == nil && parsed != 0 {
				return parsed
			}
		}
		return 1
	}
	if status < 200 || status >= 300 {
		warn(proc.stderr, "submit opencode attached message: status %d: %s", status, data)
		return ExitUsage
	}
	return 0
}
