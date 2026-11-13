//go:build jail

package doctor

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
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
	if warnings == 0 || !strings.Contains(stdout.String(), "zz=index,matcher") ||
		!strings.Contains(strings.ToLower(stdout.String()), "missing") {
		t.Fatalf("partial engine was silent: warnings=%d output=%q", warnings, stdout.String())
	}
}
