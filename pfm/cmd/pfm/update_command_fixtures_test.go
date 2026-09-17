// Shared scripted-executable fixtures for update_command_test.go.

package main

import (
	"context"
	"os"
	"testing"
)

// useScriptedUpdateCandidate swaps updateBuildCandidate for a fixture that writes a
// tiny shell script instead of building. The sibling selected-tag test exercises
// the real cmd/go build, so this test only grades which executable runs the
// post-build actions and a scripted fixture avoids paying for that subprocess
// build a second time.
func useScriptedUpdateCandidate(t *testing.T) {
	t.Helper()
	previousBuild := updateBuildCandidate
	t.Cleanup(func() { updateBuildCandidate = previousBuild })
	updateBuildCandidate = func(_ context.Context, _, _, output string) error {
		fixture := "#!/bin/sh\n" +
			"printf '%s\\n' \"$*\" >> \"$PFM_UPDATE_CANDIDATE_MARKER\"\n" +
			"printf 'pfm v0.10.0\\n'\n"
		return os.WriteFile(output, []byte(fixture), 0o755)
	}
}
