package hookentry

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const EnvHookCaptureDir = "PFM_HOOK_CAPTURE_DIR"

// CaptureHookInput persists the original bytes before any hook decoder runs.
// Filenames identify the receiver rather than trusting an undecoded payload.
func CaptureHookInput(args []string, input io.Reader, env paths.Env) (io.Reader, error) {
	directory := env.Get(EnvHookCaptureDir)
	if directory == "" || len(args) == 0 || !capturesPayload(args) {
		return input, nil
	}
	raw, err := io.ReadAll(input)
	if err != nil {
		return nil, fmt.Errorf("capture %s hook input: read: %w", args[0], err)
	}
	path := filepath.Join(directory, args[0]+"-"+rand.Text()+".json")
	if err := atomicfile.Create(path, raw, 0o600); err != nil {
		return nil, fmt.Errorf("capture %s hook input: %w", args[0], err)
	}
	return bytes.NewReader(raw), nil
}

func capturesPayload(args []string) bool {
	name := args[0]
	if name == "herdr-state" {
		options, err := parseHerdrOptions(args[1:])
		return err == nil && !options.event.FiredByTmux()
	}
	// These guards are registered outside Claude's launch settings.
	if name == "engine-guard" || name == "orchestrator-wait" || name == "usage-hook" {
		return true
	}
	for _, hook := range claudelaunch.HookTemplates("") {
		_, command, found := strings.Cut(hook.Command, " internal ")
		if found && strings.Fields(command)[0] == name {
			return true
		}
	}
	return false
}

// CapturedHook stops dispatch on a capture failure and otherwise replays stdin.
func CapturedHook(args []string, input io.Reader, stdout, stderr io.Writer, runtime config.Runtime,
	dispatch func([]string, io.Reader, io.Writer, io.Writer, config.Runtime) int,
) int {
	reader, err := CaptureHookInput(args, input, paths.OSEnv{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return dispatch(args, reader, stdout, stderr, runtime)
}
