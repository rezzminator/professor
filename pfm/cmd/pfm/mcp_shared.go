package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/mcpserv"
)

// mcpRuntime is the one bridge from the command package into MCP: the typed
// verb layer and the roster rung over the command's runtime, and the argv
// dispatcher for the stateful verbs not yet on the verb layer. ambient is true
// only for the stdio server, which one chat launches and whose caller it may
// read from its environment; the shared HTTP daemon passes false.
func mcpRuntime(runtime commandRuntime, ambient bool) mcpserv.Runtime {
	return mcpserv.Runtime{
		Paths:                runtime.Paths,
		Accounts:             runtime.Config.Accounts,
		ConfigPath:           runtime.Config.Path,
		ClaudeBinary:         runtime.Config.Claude.Binary,
		CodexBinary:          runtime.Config.Codex.Binary,
		OpencodeBinary:       runtime.Config.OpenCode.Binary,
		Chat:                 pfmchat.Verbs{Runtime: &runtime, Warnings: os.Stderr},
		Names:                pfmchat.NameResolver{Runtime: &runtime},
		AllowAmbientIdentity: ambient,
		Dispatch: func(_ context.Context, args []string, stdout, stderr io.Writer) int {
			if len(args) == 0 || args[0] != chatCommand {
				fmt.Fprintln(stderr, "pfm: MCP dispatch requires chat argv")
				return 2
			}
			return runChatWithRuntime(args[1:], strings.NewReader(""), stdout, stderr, runtime)
		},
	}
}
