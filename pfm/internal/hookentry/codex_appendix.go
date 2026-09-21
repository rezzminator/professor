package hookentry

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
	"github.com/rezzminator/professor/pfm/internal/config"
)

func CodexAppendix(input io.Reader, output, stderr io.Writer, runtime config.Runtime) int {
	if err := codexappendix.Run(input, output, runtime.Paths.Home); err != nil {
		fmt.Fprintf(stderr, "Professor appendix hook failed: %v\n", err)
		return 1
	}
	return 0
}
