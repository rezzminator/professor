package doctor

import (
	"fmt"
	"io"
	"os"

	"hostops/pfm/internal/config"
)

// printActivityLogDoctor names the home's activity log and its size — the one
// row that tells an operator where `pfm log` reads from and how much this home
// has written. A file that has never been written is ABSENT (bytes=0); a file
// that could not be stat'd is StateUnavailable with the reason, because "we
// failed to look" is not "there is nothing there". Neither is a warning: an
// unwritten log is a fresh home, and an unreadable one is reported, not tallied.
func printActivityLogDoctor(stdout io.Writer, runtime config.Runtime) {
	path := runtime.Paths.LogFile
	info, err := os.Stat(path)
	switch {
	case err == nil:
		fmt.Fprintf(
			stdout,
			"doctor: log path=%s bytes=%d level=%s keep=%d max_mb=%d\n",
			path,
			info.Size(),
			logLevelLabel(runtime.Config.Log.Level),
			runtime.Config.Log.KeepFiles,
			runtime.Config.Log.MaxMB,
		)
	case os.IsNotExist(err):
		fmt.Fprintf(stdout, "doctor: log path=%s bytes=0 state=absent level=%s keep=%d max_mb=%d\n",
			path, logLevelLabel(runtime.Config.Log.Level), runtime.Config.Log.KeepFiles, runtime.Config.Log.MaxMB)
	default:
		fmt.Fprintf(stdout, "doctor: log path=%s state=%s error=%v\n", path, StateUnavailable, err)
	}
}

// logLevelLabel renders an unset log.level as what it means: the build decides.
func logLevelLabel(level string) string {
	if level == "" {
		return "build"
	}
	return level
}
