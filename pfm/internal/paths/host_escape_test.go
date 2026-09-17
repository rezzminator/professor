package paths

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A test that never set up its jail must not resolve to the operator's own
// home directory. The escape is silent by construction: Resolve() computes
// pathnames and touches nothing, so an unjailed test and a real run produce
// byte-identical values right up to the moment something writes a fixture
// transcript into a live account or opens the fleet.db a real chat is indexed
// in. Both have happened on a dev host, from one `go test ./...` run outside
// the fence. Absence of a jail therefore has to be an ERROR here — this is the
// last point at which it is distinguishable from an ordinary run.
func TestResolveRefusesTheOperatorsRealHomeInsideATest(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv(EnvRealHome, "")

	values, err := Resolve()
	if err == nil {
		realHome, homeErr := os.UserHomeDir()
		if homeErr != nil {
			realHome = "<unknown>"
		}
		t.Fatalf(
			"Resolve() without %s returned Home=%q (the operator's real home is %q) instead of refusing;\n"+
				"an unjailed test can now open their live database at %q",
			EnvHome, values.Home, realHome, values.DB,
		)
	}
	for _, mustName := range []string{EnvHome, EnvRealHome} {
		if !strings.Contains(err.Error(), mustName) {
			t.Errorf(
				"Resolve() error %q does not name %s; the message is the only place the fix appears",
				err,
				mustName,
			)
		}
	}
}

// The rare test that MUST see the host — building a binary against the real
// module cache, probing a live config — says so by name and still works.
func TestResolveHonorsTheNamedRealHomeOptOut(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv(EnvRealHome, "1")

	values, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() with %s=1: %v", EnvRealHome, err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory on this platform: %v", err)
	}
	if values.Home != realHome {
		t.Fatalf("Resolve() with %s=1 gave Home=%q, want the real %q", EnvRealHome, values.Home, realHome)
	}
}

// PFM_HOME and HOME name one concept. A jail that pins only the first leaves
// the second pointing at the operator's real account, and a test reaching for
// the plain name writes there — which is exactly how fixture transcripts
// landed in a live ~/.claude/projects. Resolve() cannot see that escape, so it
// is gated here instead: reach for the plain variable and name your reason.
func TestNoTestReachesForThePlainHOMEVariable(t *testing.T) {
	// Built rather than written literally, so this gate does not match itself.
	needles := []string{
		"os.Getenv(" + strconv.Quote("HOME") + ")",
		"os.LookupEnv(" + strconv.Quote("HOME") + ")",
	}
	// Each entry states why the real host is the correct target there.
	allowed := map[string]string{
		"e2e/claude_capture_fixture_test.go": "asserts HOME equals the explicitly supplied private E2E home before reading fixtures or contacting the loopback sink",
		"e2e/install_e2e_test.go":            "hands `go build` the real module cache",
		"internal/mcpserv/server_test.go":    "hands `go build` the real module cache",
		"cmd/pfm/jail_home_test.go":          "asserts the jail pins the plain variable",
	}

	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	walkErr := filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot read is a hole in the sweep, never a pass.
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(moduleRoot, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if _, ok := allowed[relative]; ok {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, needle := range needles {
			if strings.Contains(string(source), needle) {
				offenders = append(offenders, relative+": "+needle)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("sweep of %s failed — this is a failure to look, not a clean result: %v", moduleRoot, walkErr)
	}
	if len(offenders) > 0 {
		t.Fatalf(
			"test files read the plain HOME variable, which a jail does not pin:\n  %s\n"+
				"use the jail's own home (paths.Resolve().Home), or add the file to the allow-list above with a reason",
			strings.Join(offenders, "\n  "),
		)
	}
}
