package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"hostops/pfm/internal/binwatch"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/harvestmcp"
	"hostops/pfm/internal/mcpserv"
)

const (
	mcpProtocolVersion = "2025-06-18"
)

var chatMCPTools = mcpserv.ToolNames()

var harvesterMCPTools = []string{
	archiveCommand, "fetch", "fetchImage", "findWorks", "search", "searchCache",
}

// mcpDaemonStatus is the stable local health document consumed by doctor and
// by the single-instance probe.
type mcpDaemonStatus struct {
	PFMVersion      string              `json:"pfmVersion"`
	ProtocolVersion string              `json:"protocolVersion"`
	Servers         map[string][]string `json:"servers"`
	PID             int                 `json:"pid"`
	StartTime       string              `json:"startTime"`
	Endpoint        string              `json:"endpoint"`
	// HarvesterExternal is the authenticated external gateway's live state:
	// "disabled", "listening on HOST:PORT as URL", or "failed: <error>". A
	// failed external bind never takes the loopback port down with it, so it
	// must be visible HERE — doctor reads it — not only in the service log.
	HarvesterExternal string `json:"harvesterExternal,omitempty"`
}

type mcpDaemonOptions struct {
	Version   string
	StartedAt time.Time
	Endpoint  string
	Chat      http.Handler
	Harvester http.Handler
	// External reports the external gateway state at request time.
	External *atomic.Pointer[string]
}

func newMCPDaemonHandler(options mcpDaemonOptions) http.Handler {
	if options.StartedAt.IsZero() {
		options.StartedAt = time.Now().UTC()
	}
	// Servers reports only what is actually mounted below, never the full
	// registered set: mcp.servers.<name>.enabled=false means the handler was
	// never constructed, and /status must not claim a tool surface the
	// daemon cannot serve.
	servers := map[string][]string{}
	if options.Chat != nil {
		servers[config.MCPServerChat] = append([]string(nil), chatMCPTools...)
	}
	if options.Harvester != nil {
		servers[config.MCPServerHarvester] = append([]string(nil), harvesterMCPTools...)
	}
	status := mcpDaemonStatus{
		PFMVersion:      options.Version,
		ProtocolVersion: mcpProtocolVersion,
		Servers:         servers,
		PID:             os.Getpid(),
		StartTime:       options.StartedAt.UTC().Format(time.RFC3339Nano),
		Endpoint:        options.Endpoint,
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Browsers attach Origin even when script code targets loopback. Local
		// MCP clients do not. Refuse browser-capable cross-origin requests before
		// they can reach chat_inject or any other mounted tool.
		if request.Header.Get("Origin") != "" {
			http.Error(writer, "browser-origin requests are forbidden", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/status":
			if request.Method != http.MethodGet {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			current := status
			if options.External != nil {
				if state := options.External.Load(); state != nil {
					current.HarvesterExternal = *state
				}
			}
			writeMCPJSON(writer, current)
		case "/mcp/chat":
			serveMCPDaemonRoute(writer, request, options.Chat, config.MCPServerChat)
		case "/mcp/harvester":
			serveMCPDaemonRoute(writer, request, options.Harvester, config.MCPServerHarvester)
		default:
			http.NotFound(writer, request)
		}
	})
}

// serveMCPDaemonRoute dispatches to a mounted server's handler. handler is
// nil exactly when mcp.servers.<name>.enabled is false, in which case a
// plain 404 would be indistinguishable from the daemon not running at all;
// answer with an explicit refusal instead so a disabled route reads as
// disabled, not broken.
func serveMCPDaemonRoute(writer http.ResponseWriter, request *http.Request, handler http.Handler, name string) {
	if handler == nil {
		http.Error(
			writer,
			fmt.Sprintf("pfm mcp: %s is disabled by config; enable it with: pfm mcp %s enable", name, name),
			http.StatusServiceUnavailable,
		)
		return
	}
	handler.ServeHTTP(writer, request)
}

func writeMCPJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		// The response may already be committed. There is no useful second
		// response to write, but retain the error in the server's normal log.
		fmt.Fprintf(os.Stderr, "pfm mcp: encode status: %v\n", err)
	}
}

func runMCPServe(stdout, stderr io.Writer, runtime commandRuntime) (exitCode int) {
	port := runtime.Config.MCP.HTTP.Port
	if port < 1 || port > 65535 {
		fmt.Fprintf(stderr, "pfm mcp serve: configured port %d is outside 1..65535\n", port)
		return 2
	}
	chatEnabled := runtime.Config.MCPServers[config.MCPServerChat].Enabled
	harvesterEnabled := runtime.Config.MCPServers[config.MCPServerHarvester].Enabled
	if !chatEnabled && !harvesterEnabled {
		fmt.Fprintf(
			stderr,
			"pfm mcp serve: every registered server is disabled by config %s; enable at least one with: pfm mcp <server> enable\n",
			runtime.Config.Path,
		)
		return 1
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	if existing, ok := probeMCPDaemon(address); ok {
		fmt.Fprintf(stderr, "pfm mcp serve: already running (pid %d, since %s)\n", existing.PID, existing.StartTime)
		return 1
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp serve: listen loopback %s: %v\n", address, err)
		return 1
	}
	listenerOwned := true
	defer func() {
		if listenerOwned {
			cli.CloseResource(listener, "pfm mcp serve: close listener", stderr, &exitCode)
		}
	}()

	// Gate at construction: a server whose config is off is never built, let
	// alone mounted, so there is no live handler for a disabled route to
	// accidentally reach.
	options := mcpDaemonOptions{Version: version, Endpoint: "http://" + address}
	if chatEnabled {
		chat, err := mcpserv.NewConfigured(version, stderr, mcpRuntime(runtime, false))
		if err != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: configure chat: %v\n", err)
			return 1
		}
		defer func() { cli.CloseResource(chat, "pfm mcp serve: close chat service", stderr, &exitCode) }()
		options.Chat = chat.NewHTTPHandler()
	}
	if harvesterEnabled {
		harvester, err := harvestmcp.NewConfiguredHarvester(version, harvestRuntime(runtime))
		if err != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: configure harvester: %v\n", err)
			return 1
		}
		defer func() {
			cli.CloseResource(harvester, "pfm mcp serve: close harvester service", stderr, &exitCode)
		}()
		options.Harvester = harvester.NewHTTPHandler()
	}
	external := &atomic.Pointer[string]{}
	setExternal := func(state string) { external.Store(&state) }
	switch {
	case !runtime.Config.Harvester.External.Enabled:
		setExternal("disabled")
	case !harvesterEnabled:
		setExternal("off: external.enabled is true but harvester.enabled is false")
		fmt.Fprintln(
			stderr,
			"pfm mcp serve: harvester external gateway NOT serving: external.enabled is true but harvester.enabled is false",
		)
	default:
		stopExternal, err := startHarvesterExternal(runtime, stderr, setExternal)
		if err != nil {
			// The loopback port keeps serving chat + harvester; the failure is
			// reported on /status (doctor) and here, never swallowed.
			setExternal("failed: " + err.Error())
			fmt.Fprintf(stderr, "pfm mcp serve: harvester external gateway NOT serving: %v\n", err)
		} else {
			defer stopExternal()
		}
	}
	options.External = external
	options.StartedAt = time.Now().UTC()
	handler := newMCPDaemonHandler(options)
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Fprintf(
		stdout, "pfm mcp serve\thttp://%s\tchat=%s\tharvester=%s\tharvester_external=%s\n",
		address, enabledState(chatEnabled), enabledState(harvesterEnabled), *external.Load(),
	)
	listenerOwned = false // http.Server.Serve closes its listener before returning.
	return binwatch.Serve(server, listener, stderr)
}

// startHarvesterExternal opens the authenticated external harvester gateway on
// its own port, inside the daemon process. It serves the harvester ONLY —
// never the chat MCP, which drives live terminals — and harvestmcp.NewRemote
// refuses to build it without a credential. The returned stop closes the
// listener and the gateway's converter.
func startHarvesterExternal(runtime commandRuntime, stderr io.Writer, setState func(string)) (func(), error) {
	settings := runtime.Config.Harvester.External
	statePath := ""
	if settings.StateDir != "" {
		statePath = filepath.Join(settings.StateDir, "auth.json")
	}
	remote, err := harvestmcp.NewRemote(harvestmcp.RemoteOptions{
		Runtime: harvestRuntime(runtime), Version: version, PublicURL: settings.PublicURL,
		Passphrase: settings.Passphrase, StaticToken: settings.StaticToken, StatePath: statePath,
	})
	if err != nil {
		return nil, fmt.Errorf("configure: %w", err)
	}
	address := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		if closeErr := remote.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: close external harvester after failed listen: %v\n", closeErr)
		}
		return nil, fmt.Errorf("listen %s: %w", address, err)
	}
	server := &http.Server{
		Handler:           remote.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	setState(fmt.Sprintf("listening on %s as %s", address, settings.PublicURL))
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			setState("failed: " + err.Error())
			fmt.Fprintf(stderr, "pfm mcp serve: harvester external gateway stopped: %v\n", err)
		}
	}()
	return func() {
		if err := server.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: close external gateway: %v\n", err)
		}
		if err := remote.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: close external harvester: %v\n", err)
		}
	}, nil
}

// enabledState renders a config gate for the startup log line so the
// operator can see which servers the daemon actually mounted, not just that
// it started.
func enabledState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func probeMCPDaemon(address string) (mcpDaemonStatus, bool) {
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/status", http.NoBody)
	if err != nil {
		return mcpDaemonStatus{}, false
	}
	client := &http.Client{Timeout: 300 * time.Millisecond}
	response, err := client.Do(request)
	if err != nil {
		return mcpDaemonStatus{}, false
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "pfm mcp serve: close daemon probe response: %v\n", err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return mcpDaemonStatus{}, false
	}
	var status mcpDaemonStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || status.PID < 1 {
		return mcpDaemonStatus{}, false
	}
	return status, true
}

// mcpDaemonReachability is kept separate from config printing so doctor can
// report a failed probe as a named state rather than silently omitting it.
func mcpDaemonReachability(runtime commandRuntime) (mcpDaemonStatus, error) {
	address := "127.0.0.1:" + strconv.Itoa(runtime.Config.MCP.HTTP.Port)
	status, ok := probeMCPDaemon(address)
	if !ok {
		return mcpDaemonStatus{}, fmt.Errorf("unreachable at http://%s/status", address)
	}
	return status, nil
}
