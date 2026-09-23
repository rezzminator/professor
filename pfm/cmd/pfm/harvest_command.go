package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

// retiredHarvesterServeFlags maps every flag the pre-config `pfm mcp harvester
// serve` accepted to where that setting lives now. A stale registration that
// still passes one fails loudly with the exact key, never silently.
var retiredHarvesterServeFlags = map[string]string{
	"user-agent":            "fetch.userAgent in harvester.config.json",
	"proxy-url":             "fetch.proxyURL in harvester.config.json",
	"host":                  "external.host in harvester.config.json (served by `pfm mcp serve`)",
	"port":                  "external.port in harvester.config.json (served by `pfm mcp serve`)",
	"public-url":            "external.publicURL in harvester.config.json (served by `pfm mcp serve`)",
	"internal-host":         "retired: the daemon's loopback port (mcp.http.port) is the internal gateway",
	"internal-port":         "retired: the daemon's loopback port (mcp.http.port) is the internal gateway",
	"allow-unauthenticated": "retired: the external gateway always authenticates (external.auth.*)",
	"ignore-robots-txt":     "retired: Harvester never consulted robots.txt",
}

// runHarvesterMCP serves the harvester over stdio for a client that launches
// it as a command. Both HTTP gateways — loopback and the authenticated
// external one — belong to the one daemon process, `pfm mcp serve`.
func runHarvesterMCP(args []string, _, stderr io.Writer, runtime commandRuntime) int {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if where, retired := retiredHarvesterServeFlags[name]; retired {
			fmt.Fprintf(stderr, "pfm mcp harvester serve: --%s is retired; %s\n", name, where)
			return 2
		}
	}
	flags := cli.NewFlagSet("mcp harvester serve", "usage: pfm mcp harvester serve [--transport stdio]", stderr)
	transport := flags.String(
		"transport",
		"stdio",
		"MCP transport (stdio only; HTTP gateways are served by `pfm mcp serve`)",
	)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if *transport != "stdio" {
		fmt.Fprintf(
			stderr,
			"pfm mcp harvester: --transport %q is retired; the loopback and authenticated external HTTP gateways are served by `pfm mcp serve` (harvester.config.json external.*)\n",
			*transport,
		)
		return 2
	}
	service, err := harvestmcp.NewConfiguredHarvester(version, harvestRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm mcp harvester: %v\n", err)
		return 1
	}
	defer func() {
		if closeErr := service.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "pfm mcp harvester: close converter: %v\n", closeErr)
		}
	}()
	if err := service.RunStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(stderr, "pfm mcp harvester: %v\n", err)
		return 1
	}
	return 0
}

// harvestRuntime resolves this machine's harvester config into the MCP
// adapter's runtime (harvestmcp.RuntimeFromConfig).
func harvestRuntime(runtime commandRuntime) harvestmcp.Runtime {
	return harvestmcp.RuntimeFromConfig(runtime.Paths.Home, runtime.Config.Harvester)
}
