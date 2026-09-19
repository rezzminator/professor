package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"hostops/pfm/internal/binwatch"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/harvestmcp"
	"hostops/pfm/internal/mcpserv"
)

func runMCPServe(stdout, stderr io.Writer, runtime commandRuntime, clk clock.Clock) (exitCode int) {
	clk = defaultClock(clk)
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
	// Three outcomes, three answers: pfm's own daemon is already up; the port
	// is held by something that is not it (binding would either fail or, worse,
	// look like it worked while clients keep reaching the squatter); or nothing
	// is listening, which is the only case that goes on to bind.
	existing, probeErr := mcpserv.ProbeDaemon(address)
	switch {
	case probeErr == nil:
		fmt.Fprintf(stderr, "pfm mcp serve: already running (pid %d, since %s)\n", existing.PID, existing.StartTime)
		return 1
	case !errors.Is(probeErr, mcpserv.ErrDaemonAbsent):
		fmt.Fprintf(stderr, "pfm mcp serve: port %d is held by something that is not pfm: %v\n", port, probeErr)
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
	options := mcpserv.DaemonOptions{Version: version, Endpoint: "http://" + address, Warnings: stderr}
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
		options.HarvesterTools = harvestmcp.RegisteredToolNames(harvestRuntime(runtime))
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
	options.Clock = clk
	options.StartedAt = clk.Now().UTC()
	server := &http.Server{
		Handler:           mcpserv.NewDaemonHandler(options),
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
