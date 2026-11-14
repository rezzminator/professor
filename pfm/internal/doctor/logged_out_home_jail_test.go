//go:build jail

package doctor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// Kept real: the subject is what a real logged-out claude and codex child
// write into the HOME doctor hands them. The assertions read the files those
// children wrote and the throwaway homes they ran in, which a scripted runner
// would never produce.

// homeSnapshot maps each path under root to its mode and, for a file, its
// bytes or, for a symlink, its target, so two snapshots compare byte for byte.
func homeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entry := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry += " -> " + target
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry += " " + string(content)
		}
		snapshot[path] = entry
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snapshot
}

// TestDoctorLeavesHomeByteIdenticalWithALoggedOutClaude: on a migrated host one
// plain pfm doctor ran a logged-out claude with the ambient env, which
// recreated ~/.claude/backups in the shared store plus $HOME/.claude.json; the
// host check then reported store-identity and install refused. Doctor's claude
// runs each get a throwaway home, so $HOME, the store included, is unchanged.
func TestDoctorLeavesHomeByteIdenticalWithALoggedOutClaude(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	// The real dependency probe, so its claude runs reach the fake.
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	DependencyProbeOverride = nil
	// Production's SID dir is /tmp/cc-sid, outside HOME.
	sid := filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	scratch := t.TempDir()
	record := filepath.Join(scratch, "config-dirs.log")
	bin := filepath.Join(scratch, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteLoggedOutClaude(
		filepath.Join(bin, engine.MustLookup(engine.Claude).Binary), record, "",
	); err != nil {
		t.Fatal(err)
	}
	codexRecord := filepath.Join(scratch, "codex-homes.log")
	if err := testjail.WriteLoggedOutCodex(
		filepath.Join(bin, engine.MustLookup(engine.Codex).Binary), codexRecord, "",
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := homeSnapshot(t, home)
	var stdout, stderr bytes.Buffer
	runDoctorRealChildren(&stdout, &stderr, runtime)
	after := homeSnapshot(t, home)
	for path, entry := range after {
		if before[path] != entry {
			t.Errorf("doctor changed %s", path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("doctor removed %s", path)
		}
	}
	if runs := testjail.AssertClaudeRanInThrowawayHomes(t, home, sid, record); runs == 0 {
		t.Errorf("doctor never ran the fake claude; stdout:\n%s", stdout.String())
	}
	if runs := testjail.AssertCodexRanInThrowawayHomes(t, home, sid, codexRecord); runs == 0 {
		t.Errorf("doctor never ran the fake codex; stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "account rows left to pfm: auth") {
		t.Errorf("the logged-out throwaway home's auth row was judged as the binary's:\n%s", stdout.String())
	}
}

// runDoctorRealChildren is runDoctor over the doctor's own runner: its probes
// start the fake claude and codex found on PATH.
func runDoctorRealChildren(stdout, stderr io.Writer, runtime config.Runtime) int {
	return Run(nil, stdout, stderr, runtime, testDependencies())
}
