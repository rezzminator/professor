package hookentry

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// CodexLaunch applies the current primary account's Codex policy at launch time.
func CodexLaunch(args []string, stderr io.Writer, runtime config.Runtime) int {
	primary, err := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "resolve primary account: %v\n", err)
		return 1
	}
	policy := runtime.Config.EffectiveCodex(primary)
	binaryName := policy.Binary
	if binaryName == "" {
		binaryName = pfmengine.MustLookup(pfmengine.Codex).Binary
	}
	binary, err := obs.Runner(deps.RealRunner{}).LookPath(binaryName)
	if err != nil {
		fmt.Fprintf(stderr, "resolve Codex binary: %v\n", err)
		return 1
	}
	argv := []string{binary}
	if policy.Yolo {
		argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
	}
	argv = append(argv, args...)
	unset := make(map[string]bool)
	for _, name := range claudelaunch.Hygiene() {
		unset[name] = true
	}
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !unset[name] {
			env = append(env, entry)
		}
	}
	if err := LaunchExec(binary, argv, env); err != nil {
		fmt.Fprintf(stderr, "launch Codex: %v\n", err)
		return 1
	}
	return 0
}
