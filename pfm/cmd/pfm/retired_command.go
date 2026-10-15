package main

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/update"
)

// runRetiredCommand answers a retired `pfm {command}` an adopter's agents may
// still run: one stderr line naming the successor and the port step, exit 2,
// nothing run. The name and successor come from the retired list alone; a
// command that list does not retire is the unknown-command answer, never a
// silent success.
func runRetiredCommand(command string, stderr io.Writer) int {
	name, successor, ok := update.RetiredPfmCommand(command)
	if !ok {
		fmt.Fprintf(stderr, "pfm: unknown command %q\n", command)
		return 2
	}
	fmt.Fprintf(
		stderr,
		"pfm: %q is retired — run \"%s ...\"; port this project's files with \"pfm doctor --project-updates\"\n",
		name, successor,
	)
	return 2
}
