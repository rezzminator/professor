package hookentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// KillFront is the in-process kill command front injected by runInternal.
type KillFront func(args []string, stdout, stderr io.Writer, runtimes ...config.Runtime) int

var exitWords = map[string]bool{"e": true, "/e": true}

// ExitIntercept converts an exact e or /e prompt into the injected kill front.
func ExitIntercept(
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	kill KillFront,
) int {
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-intercept: decode hook payload: %v\n", err)
		return 0
	}
	if !exitWords[strings.TrimSpace(payload.Prompt)] {
		return 0
	}
	var captured bytes.Buffer
	if kill([]string{"--self", "--exit"}, &captured, &captured, runtime) == 0 {
		return blockPromptQuietly(stdout)
	}
	fmt.Fprintf(stderr, "exit: %s", captured.String())
	return 2
}
