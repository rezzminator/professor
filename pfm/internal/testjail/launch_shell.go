package testjail

import (
	"fmt"
	"os"
)

// launchShellEnv is the variable a Claude launch adds (claudelaunch.ShellEnv).
// Literal name: testjail cannot import internal/claudelaunch (import cycle).
const launchShellEnv = "CLAUDE_CODE_SHELL"

// jailedPfmStub is the installed pfm of a jailed home. It is also the cost launcher every rendered
// Claude launch runs in front of Claude: here it arms no sink and execs Claude, so a launched pane
// runs its fake engine.
const jailedPfmStub = "#!/bin/sh\n# jailed-pfm\n" +
	"if [ \"$1\" = internal ] && [ \"$2\" = cost-launch ] && [ \"$3\" = -- ]; then shift 3; exec \"$@\"; fi\n" +
	"echo \"jailed-pfm: $*: not a pfm\" >&2\nexit 127\n"

// pinLaunchShell pins CLAUDE_CODE_SHELL to the bash resolve finds, or clears
// it when there is none. A Claude launch adds CLAUDE_CODE_SHELL, bash resolved
// on the host's PATH, unless the launch environment already carries a usable
// one; pinning this host's bash keeps every emitted launch line host-blind and
// golden-testable, and without a bash the launch adds nothing either. A test
// of the resolution itself sets or clears it.
func pinLaunchShell(resolve func(string) (string, error)) error {
	bash, err := resolve("bash")
	if err != nil {
		if unsetErr := os.Unsetenv(launchShellEnv); unsetErr != nil {
			return fmt.Errorf("clear %s (no bash: %v): %w", launchShellEnv, err, unsetErr)
		}
		return nil
	}
	if err := os.Setenv(launchShellEnv, bash); err != nil {
		return fmt.Errorf("pin %s to %s: %w", launchShellEnv, bash, err)
	}
	return nil
}
