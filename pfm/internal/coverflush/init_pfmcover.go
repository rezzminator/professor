//go:build pfmcover

package coverflush

import (
	"os"
	"runtime/coverage"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func init() {
	install(paths.OSEnv{}.Get("GOCOVERDIR"), coverage.WriteCountersDir, func(sig syscall.Signal) error {
		return syscall.Kill(os.Getpid(), sig)
	})
}
