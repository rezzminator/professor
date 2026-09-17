package hookentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/config"
)

// ReloadFront is the in-process reload command front injected by runInternal.
type ReloadFront func(args []string, stdout, stderr io.Writer, runtime config.Runtime) int

// ReloadIntercept converts a /reload prompt into the injected reload front.
func ReloadIntercept(
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
) int {
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal reload-intercept: decode hook payload: %v\n", err)
		return 0
	}
	trimmed := strings.TrimSpace(payload.Prompt)
	if trimmed != "/reload" && !strings.HasPrefix(trimmed, "/reload ") {
		return 0
	}
	words, err := action.SplitShellWords(strings.TrimPrefix(trimmed, "/reload"))
	if err != nil {
		fmt.Fprintf(stderr, "reload: %v\n", err)
		return 2
	}
	var captured bytes.Buffer
	if reload(words, &captured, &captured, runtime) == 0 {
		return blockPromptQuietly(stdout)
	}
	fmt.Fprintf(stderr, "reload: %s", captured.String())
	return 2
}
