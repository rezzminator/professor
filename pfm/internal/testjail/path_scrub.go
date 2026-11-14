package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// pfmInstallBinSuffix is the tail of pfm's managed install bin dir, whichever
// home owns it.
var pfmInstallBinSuffix = filepath.Join(".local", "share", "pfm", "install", "bin")

// scrubPATH strips the operator's ~/.local/bin and pfm's managed install bin, which hold pfm's own
// `claude` launcher shim, from PATH, so a jailed test resolving a binary through PATH never reaches
// it. An armed unit binary holds no PATH to scrub: the unit net points it at an empty directory.
func scrubPATH(armed bool) error {
	if armed {
		return nil
	}
	hostEnv := paths.OSEnv{}
	operatorHome, homeErr := hostEnv.Home()
	if homeErr != nil {
		warnSetup("user home unknown, stripping only pfm install bin dirs from PATH: %v", homeErr)
		operatorHome = ""
	}
	if err := os.Setenv("PATH", scrubOperatorPATH(hostEnv.Get("PATH"), operatorHome)); err != nil {
		return fmt.Errorf("scrub PATH: %w", err)
	}
	return nil
}

// scrubOperatorPATH drops every PATH entry that is <home>/.local/bin or a pfm
// install bin dir (a path ending .local/share/pfm/install/bin, any home),
// comparing after filepath.Clean and, where it resolves, EvalSymlinks. Order
// and every other entry — empty ones included — are kept. An empty home matches
// no ~/.local/bin, so only install bin dirs go.
func scrubOperatorPATH(pathEnv, home string) string {
	var homeBins []string
	if home != "" {
		bin := filepath.Join(home, ".local", "bin")
		homeBins = append(homeBins, bin)
		if resolved, err := filepath.EvalSymlinks(bin); err == nil {
			homeBins = append(homeBins, filepath.Clean(resolved))
		}
	}
	isOperatorBin := func(candidate string) bool {
		candidate = filepath.Clean(candidate)
		for _, bin := range homeBins {
			if candidate == bin {
				return true
			}
		}
		return candidate == pfmInstallBinSuffix ||
			strings.HasSuffix(candidate, string(filepath.Separator)+pfmInstallBinSuffix)
	}
	var kept []string
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry != "" {
			if isOperatorBin(entry) {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(entry); err == nil && isOperatorBin(resolved) {
				continue
			}
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}
