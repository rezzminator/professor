package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// EnvInstallMark carries InstallMark into every process pfm launches: pfm sets
// it in its own environment at start, so its children (tmux servers, engines,
// helpers) inherit it, and the service units it writes name it. A stop of
// pfm-launched processes acts only on its own install's mark, so a test jail
// or another checkout's build never reaches the operator's live fleet.
const EnvInstallMark = "PFM_INSTALL_MARK"

// InstallMark is the install's mark: a digest of its resolved home, so two
// homes (the operator's, a jail's) never share one.
func InstallMark(home string) string {
	sum := sha256.Sum256([]byte(PhysicalPath(home)))
	return hex.EncodeToString(sum[:8])
}

// MarkProcess sets the mark of the install at home in pfm's own environment,
// so every process it launches from here on inherits it. The kernel keeps a
// process's environment as it started, so the mark reaches children only,
// never pfm itself: a stop that finds no mark falls back to the installed
// binary's path.
func MarkProcess(home string) error {
	if home == "" {
		return fmt.Errorf("mark this install's processes: no resolved home")
	}
	if err := os.Setenv(EnvInstallMark, InstallMark(home)); err != nil {
		return fmt.Errorf("mark this install's processes: set %s: %w", EnvInstallMark, err)
	}
	return nil
}
