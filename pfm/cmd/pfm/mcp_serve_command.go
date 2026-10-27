package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/binwatch"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/mcpserv"
)

func runMCPServe(stdout, stderr io.Writer, runtime commandRuntime, clk clock.Clock) (exitCode int) {
	clk = defaultClock(clk)
	port := runtime.Config.MCP.HTTP.Port
	if port < 1 || port > 65535 {
		fmt.Fprintf(stderr, "pfm mcp serve: configured port %d is outside 1..65535\n", port)
		return 2
	}
	chatEnabled := runtime.Config.MCPServers[config.MCPServerChat].Enabled
	if !chatEnabled {
		fmt.Fprintf(
			stderr,
			"pfm mcp serve: chat is disabled by config %s; enable it with: pfm mcp chat enable\n",
			runtime.Config.Path,
		)
		return 1
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	// A healthy daemon, an unresponsive listener, and a foreign service all
	// hold the port. Only an absent daemon lets this command try to bind it.
	existing, probeErr := mcpserv.ProbeDaemon(address)
	switch {
	case probeErr == nil:
		fmt.Fprintf(stderr, "pfm mcp serve: already running (pid %d, since %s)\n", existing.PID, existing.StartTime)
		return 1
	case errors.Is(probeErr, mcpserv.ErrDaemonUnresponsive):
		fmt.Fprintf(
			stderr,
			"pfm mcp serve: port %d is held by a listener that did not answer pfm's status probe: %v\n",
			port,
			probeErr,
		)
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
	families := mcpserv.FamilyServerOptions{Version: version}
	if chatEnabled {
		chat, err := mcpserv.NewConfigured(version, stderr, mcpRuntime(runtime, false))
		if err != nil {
			fmt.Fprintf(stderr, "pfm mcp serve: configure chat: %v\n", err)
			return 1
		}
		defer func() { cli.CloseResource(chat, "pfm mcp serve: close chat service", stderr, &exitCode) }()
		families.Chat = chat
		options.ChatRuntimeIdentity = chat.RuntimeIdentity()
	}
	professor, err := mcpserv.NewFamilyServers(families)
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp serve: %v\n", err)
		return 1
	}
	options.Chat = professor.FamilyHandler(config.MCPServerChat)
	options.Clock = clk
	options.StartedAt = clk.Now().UTC()
	server := newLoopbackMCPServer(mcpserv.NewDaemonHandler(options))
	fmt.Fprintf(
		stdout, "pfm mcp serve\thttp://%s\tchat=%s\n",
		address, enabledState(chatEnabled),
	)
	listenerOwned = false // http.Server.Serve closes its listener before returning.
	return binwatch.Serve(server, listener, stderr)
}

func newLoopbackMCPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, IdleTimeout: 2 * time.Minute,
	}
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

// runMCPStdio serves the selected family through its daemon route,
// with an in-process family server when the daemon is unavailable.
func runMCPStdio(_, stderr io.Writer, runtime commandRuntime, family string) (exitCode int) {
	if family != config.MCPServerChat {
		fmt.Fprintln(stderr, "pfm mcp serve --stdio: --family must be chat")
		return 2
	}
	if !runtime.Config.MCPServers[family].Enabled {
		fmt.Fprintf(stderr, "pfm mcp: %s is disabled; enable with: pfm mcp %s enable\n", family, family)
		return 1
	}
	chatRuntime := mcpRuntime(runtime, true)
	professor, closeFamilies, err := newStdioFamilies(stderr, runtime, chatRuntime)
	defer closeFamilies(&exitCode)
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp serve --stdio: %v\n", err)
		return 1
	}
	// This server answers from the build it started on until its chat ends:
	// Claude Code does not relaunch a stdio server that exits, so ending it on
	// an install would take the MCP tools away from every running chat.
	err = professor.RunStdio(context.Background(), os.Stdin, os.Stdout, mcpserv.StdioOptions{
		Family:        family,
		DaemonAddress: chatRuntime.DaemonAddress,
		Home:          runtime.Paths.Home,
		SIDDir:        runtime.Paths.SIDDir,
		Warnings:      stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp serve --stdio: %v\n", err)
		return 1
	}
	return 0
}

// newStdioFamilies configures every enabled family and builds the stdio
// server over them. A family that fails to configure does not take the other
// down: its tools stay listed and answer the error (mcpserv.FailedFamily), and
// one stderr line names it — the sole enabled family included, so the server
// still starts. Only when config enables no family does it fail.
// closeFamilies closes each configured service; it is always safe to call.
func newStdioFamilies(
	stderr io.Writer,
	runtime commandRuntime,
	chatRuntime mcpserv.Runtime,
) (professor *mcpserv.FamilyServers, closeFamilies func(*int), err error) {
	var closers []func(*int)
	closeFamilies = func(exitCode *int) {
		for _, closeFamily := range closers {
			closeFamily(exitCode)
		}
	}
	families := mcpserv.FamilyServerOptions{Version: version}
	fail := func(family string, tools []string, err error) {
		fmt.Fprintf(stderr, "pfm mcp serve --stdio: configure %s: %v\n", family, err)
		families.Failed = append(families.Failed, mcpserv.FailedFamily{
			Family: family, Tools: tools, Err: err, ConfigPath: runtime.Config.Path,
		})
	}
	if runtime.Config.MCPServers[config.MCPServerChat].Enabled {
		chat, err := mcpserv.NewConfigured(version, stderr, chatRuntime)
		if err != nil {
			fail(config.MCPServerChat, mcpserv.ToolNames(), err)
		} else {
			closers = append(closers, func(exitCode *int) {
				cli.CloseResource(chat, "pfm mcp serve --stdio: close chat service", stderr, exitCode)
			})
			families.Chat = chat
		}
	}
	if families.Chat == nil && len(families.Failed) == 0 {
		return nil, closeFamilies, errors.New("no family is enabled by config")
	}
	professor, err = mcpserv.NewFamilyServers(families)
	return professor, closeFamilies, err
}
