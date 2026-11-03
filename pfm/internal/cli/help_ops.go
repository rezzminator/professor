package cli

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
)

//go:generate go run ../../scripts/helpops -root ../..

//go:embed help_ops.txt
var helpOperations string

// RunHelp renders the root usage or the derived public operation census.
func RunHelp(args []string, stdout, stderr io.Writer, usage func(io.Writer)) int {
	if len(args) == 0 || args[0] != "--ops" {
		usage(stdout)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: pfm help --ops")
		return 2
	}
	if err := WriteLines(stdout, strings.Split(strings.TrimSuffix(helpOperations, "\n"), "\n")); err != nil {
		fmt.Fprintf(stderr, "pfm help --ops: write census: %v\n", err)
		return 1
	}
	return 0
}
