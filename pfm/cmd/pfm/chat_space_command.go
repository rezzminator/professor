package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
)

// runChatSpace is `pfm chat space [name]`: the calling chat moves into the
// named space, or with no name back to its folder's place (chat.Space).
func runChatSpace(ctx context.Context, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := cli.NewFlagSet("chat space", "usage: pfm chat space [name]", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() > 1 || flags.NArg() == 1 && strings.TrimSpace(flags.Arg(0)) == "" {
		flags.Usage()
		return 2
	}
	line, err := pfmchat.Space(ctx, &runtime, flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat space: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, line)
	return 0
}
