package main

import (
	"io"

	"hostops/pfm/internal/opencodegen"
)

func runOpenCode(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return opencodegen.RunCommand(args, codexRepoRoot, runtime.Paths.Home, stdout, stderr)
}
