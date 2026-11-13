//go:build jail

package picker

import (
	"bytes"
	"context"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// A scripted listing (--tsv, --plain, --killed, <id>) is parsed by a script;
// the update notice on stderr belongs only to the interactive picker.
func TestScriptedListingPrintsNoUpdateNotice(t *testing.T) {
	previousStart := startProfessorUpdateCheck
	t.Cleanup(func() { startProfessorUpdateCheck = previousStart })
	startProfessorUpdateCheck = func(context.Context, []string, deps.StartOptions) error { return nil }

	for _, args := range [][]string{{"--killed"}, {"--killed", "--tsv"}, {"--tsv"}, {"--plain"}, {"no-such-chat"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			jailTest(t)
			runtime := pfmconfig.Runtime{Version: "v1.0.0", Paths: jailPaths(t)}
			writeFailingUpdateCache(t, runtime)
			if professorUpdateCheckNotice(runtime) == "" {
				t.Fatal("fixture: the failing check produced no notice to suppress")
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, &stdout, &stderr, runtime)
			if strings.Contains(stderr.String(), "update check") {
				t.Fatalf("pfm ls %v (code %d) printed the update notice on stderr: %q", args, code, stderr.String())
			}
		})
	}
}
