package main

import (
	"fmt"
	"io"

	"hostops/pfm/internal/codexappendix"
)

func runCodexAppendix(input io.Reader, output, stderr io.Writer, runtime commandRuntime) int {
	if err := codexappendix.Run(input, output, runtime.Paths.Home); err != nil {
		fmt.Fprintf(stderr, "Professor appendix hook failed: %v\n", err)
		return 1
	}
	return 0
}
