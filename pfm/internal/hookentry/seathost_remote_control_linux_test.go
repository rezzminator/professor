//go:build linux

package hookentry

import (
	"os"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func init() {
	if os.Getenv("PFM_REMOTE_HOST_CHILD") != "1" {
		return
	}
	runtime, err := config.LoadRuntime(os.Getenv(paths.EnvConfig))
	if err != nil {
		panic(err)
	}
	if os.Getenv("PFM_REMOTE_HOST_CORRUPT_CONFIG") == "1" {
		if err := os.WriteFile(runtime.Config.Path, []byte("{broken"), 0o600); err != nil {
			panic(err)
		}
	}
	// Retained supervisor preferences are stale; the file must win at exec.
	runtime.Config.Claude.RemoteControl = !runtime.Config.Claude.RemoteControl
	os.Exit(SeatHost([]string{"native-seat"}, os.Stdout, os.Stderr, runtime))
}
