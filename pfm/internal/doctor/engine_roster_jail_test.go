//go:build jail

package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/index"
)

// Kept real: the subject is a new engine registering in a process of its own,
// so the test re-executes the test binary as a child; the registration is
// process-global and must never reach the rest of the suite.

func TestDoctorWarnsWhenANewEngineRegistersOnlySomeCapabilities(t *testing.T) {
	const helper = "PFM_DOCTOR_PARTIAL_ENGINE_HELPER"
	if os.Getenv(helper) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestDoctorWarnsWhenANewEngineRegistersOnlySomeCapabilities$")
		command.Env = append(os.Environ(), helper+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("partial-engine doctor proof failed: %v\n%s", err, output)
		}
		return
	}
	id := pfmengine.ID("zz")
	pfmengine.Register(pfmengine.Descriptor{
		ID: id, Name: "Zed", Short: "Zed", LongName: "zed", Binary: "zed",
		SocketPrefix: "zz-", RootEnv: "PFM_ZZ_ROOT",
		DefaultRoots: func(home string) []string { return []string{home} },
	})
	index.RegisterSource(id, fourthSource{})
	gather.RegisterMatcher(id, fourthMatcher{})
	var stdout bytes.Buffer
	warnings := PrintEngineCapabilities(&stdout, testDependencies())
	if got, want := fmt.Sprintf(
		"%d %s",
		warnings,
		stdout.String(),
	), "1 doctor: engines cc=index,launcher,matcher,usage,headless,ask cx=index,launcher,matcher,usage,headless,ask ox=index,matcher zz=index,matcher MISSING(launcher,usage,headless,ask)\n"; got != want {
		t.Fatalf("partial engine report = %q, want %q", got, want)
	}
}
