package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/hostcheck"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const staleStamp = ".pre-professor-20300101-000000"

// buildStaleCleanHome is the clean doctor home minus the account dirs it
// stages for no configured account (testjail.CleanHome makes .cc/1 and .cc/2),
// which --stale rightly lists as dead.
func buildStaleCleanHome(t *testing.T) config.Runtime {
	t.Helper()
	runtime := buildCleanDoctorHome(t)
	for _, id := range []int{1, 2} {
		dir := config.DefaultAccountDir(runtime.Paths.Home, id)
		configured := false
		for _, account := range runtime.Config.Accounts {
			configured = configured || account.ConfigDir == dir
		}
		if !configured {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatalf("remove unconfigured fixture account dir %s: %v", dir, err)
			}
		}
	}
	return runtime
}

func seedStaleFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("retired"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func ensureStaleSuccessor(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		seedStaleFile(t, path)
	} else if err != nil {
		t.Fatalf("inspect %s: %v", path, err)
	}
}

// seedStaleHome writes one path per retired-path kind pfm once wrote, each
// successor present so none is held, and returns path → kind.
func seedStaleHome(t *testing.T, runtime config.Runtime) map[string]string {
	t.Helper()
	home := runtime.Paths.Home
	store := installer.ClaudeStore(home)
	legacyConfigDir := config.LegacyConfigDir(paths.OSEnv{}, home)
	ensureStaleSuccessor(t, runtime.Config.Path)
	ensureStaleSuccessor(t, runtime.Paths.StateDB)
	ensureStaleSuccessor(t, runtime.Paths.CacheDB)
	if err := os.MkdirAll(paths.HarvesterCacheDir(home), 0o700); err != nil {
		t.Fatal(err)
	}
	seeded := map[string]string{}
	seeded[filepath.Join(legacyConfigDir, config.FileName)] = "legacy-config-dir"
	seeded[paths.LegacyStateDB(home)] = "legacy-state-db"
	seeded[paths.LegacyCacheDB(home)] = "legacy-cache-db"
	seeded[filepath.Join(paths.LegacyHarvesterCacheDir(home), "page.json")] = "legacy-harvester-cache"
	seeded[filepath.Join(installer.ManagedRoot(home), "harness-prompts", "a.md")] = "staged-prompts"
	seeded[filepath.Join(home, ".local", "state", "pfm", "shared.db")] = "shared-db"
	seeded[filepath.Join(store, "settings.json"+staleStamp)] = "install-backup"
	seeded[filepath.Join(home, ".zshrc"+staleStamp)] = "install-backup"
	seeded[filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.mcp.plist"+staleStamp)] = "install-backup"
	seeded[filepath.Join(home, ".config", "systemd", "user", "pfm-mcp.service"+staleStamp)] = "install-backup"
	seeded[runtime.Paths.StateDB+".bak-before-v7"] = "migration-backup"
	seeded[filepath.Join(installer.RetiredStoreArchive(home), ".last-update-result.json"+staleStamp)] = "retired-archive"
	seeded[filepath.Join(home, ".local", "state", "pfm", "retired-commands", "cc-ls"+staleStamp)] = "retired-archive"
	seeded[filepath.Join(config.DefaultAccountDir(home, 9), "settings.json")] = "dead-account-dir"
	seeded[filepath.Join(home, ".local", "state", "pfm", "fleet.db.bak-before-v8")] = "retired-fleet-db"
	seeded[filepath.Join(home, ".local", "state", "pfm", "retired-chat-skills.X8Z7oiYZ", "SKILL.md")] = "retired-chat-skills"
	seeded[filepath.Join(installer.RetiredMigrationJournal(home), "20260928T100205Z", "journal.json")] = "retired-migration-journal"
	for path := range seeded {
		seedStaleFile(t, path)
	}
	link := filepath.Join(store, "skills", "deep-rr")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".professor", "engines", "deep-rr"), link); err != nil {
		t.Fatalf("link %s: %v", link, err)
	}
	seeded[link] = "dead-registry-link"
	return seeded
}

// listedPath is the row path that carries seeded: the seeded file itself, or
// the retired directory holding it.
func listedPath(seeded, kind string) string {
	switch kind {
	case "legacy-config-dir", "legacy-harvester-cache", "staged-prompts", "dead-account-dir", "retired-chat-skills":
		return filepath.Dir(seeded)
	case "retired-migration-journal":
		return filepath.Dir(filepath.Dir(seeded))
	}
	return seeded
}

// TestDoctorStaleListsAndPurgesEveryRetiredPath pins --stale (one row per
// retired path with its kind) and --purge (every path moved into one
// timestamped backup dir, the originals gone).
func TestDoctorStaleListsAndPurgesEveryRetiredPath(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	home := runtime.Paths.Home
	seeded := seedStaleHome(t, runtime)

	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--stale"}, &stdout, &stderr, runtime)
	if code != 1 {
		t.Fatalf("doctor --stale code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "doctor:") {
		t.Fatalf("doctor --stale ran the health pass:\n%s", stdout.String())
	}
	for path, kind := range seeded {
		want := "stale: WARN " + kind + " " + listedPath(path, kind) + " — "
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor --stale misses %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = runDoctor([]string{"--stale", "--purge"}, &stdout, &stderr, runtime)
	if code != 0 {
		t.Fatalf("doctor --stale --purge code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	backup := ""
	for _, line := range strings.Split(stdout.String(), "\n") {
		if rest, ok := strings.CutPrefix(line, "stale: backup "); ok {
			backup = strings.Fields(rest)[0]
		}
	}
	if !strings.HasPrefix(
		backup,
		filepath.Join(home, ".local", "state", "pfm", "stale-backup")+string(filepath.Separator),
	) {
		t.Fatalf("doctor --stale --purge printed no backup dir under the home:\n%s", stdout.String())
	}
	for path := range seeded {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("purged %s still present: err=%v\n%s", path, err, stdout.String())
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(backup, relative)); err != nil {
			t.Fatalf("purged %s missing from backup %s: %v", path, backup, err)
		}
	}

	stdout.Reset()
	if code := runDoctor([]string{"--stale"}, &stdout, &stderr, runtime); code != 0 ||
		!strings.Contains(stdout.String(), "stale: ok (") {
		t.Fatalf("doctor --stale after purge code=%d stdout=%s", code, stdout.String())
	}
}

// TestDoctorStalePurgeReportsAFailedMove pins the failure door: a move that
// fails is named with its error, the rest still move, and the run exits 3.
func TestDoctorStalePurgeReportsAFailedMove(t *testing.T) {
	runtime := buildStaleCleanHome(t)
	home := runtime.Paths.Home
	failing := filepath.Join(home, ".local", "state", "pfm", "shared.db")
	moving := filepath.Join(home, ".zshrc"+staleStamp)
	seedStaleFile(t, failing)
	seedStaleFile(t, moving)
	saved := staleRename
	t.Cleanup(func() { staleRename = saved })
	staleRename = func(from, to string) error {
		if from == failing {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("injected failure")}
		}
		return saved(from, to)
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--stale", "--purge"}, &stdout, &stderr, runtime)
	if code != 3 {
		t.Fatalf("doctor --stale --purge code=%d stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "stale: FAILED "+failing+" — ") ||
		!strings.Contains(stdout.String(), "injected failure") ||
		!strings.Contains(stdout.String(), "moved 1, failed 1") {
		t.Fatalf("doctor --stale --purge did not report the failed move:\n%s", stdout.String())
	}
	if _, err := os.Lstat(failing); err != nil {
		t.Fatalf("failed move lost %s: %v", failing, err)
	}
	if _, err := os.Lstat(moving); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s did not move beside the failure: %v", moving, err)
	}
}

// TestDoctorStaleCleanHomeNamesTheWalk pins the clean answer: "ok" with the
// count of kinds walked, exit 0, so an empty list reads as a walk that ran.
func TestDoctorStaleCleanHomeNamesTheWalk(t *testing.T) {
	runtime := buildStaleCleanHome(t)
	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--stale"}, &stdout, &stderr, runtime)
	want := fmt.Sprintf("stale: ok (%d kinds walked)\n", len(hostcheck.RetiredPathDetectors()))
	if code != 0 || len(hostcheck.RetiredPathDetectors()) == 0 || stdout.String() != want {
		t.Fatalf("doctor --stale clean code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestDoctorStaleUsageRejectsMixedFlags pins the usage door.
func TestDoctorStaleUsageRejectsMixedFlags(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	for _, args := range [][]string{
		{"--purge"},
		{"--stale", "--verbose"},
		{"--stale", "--skip-harvest"},
		{"--stale", "--project-updates"},
		{"--stale", "--json"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runDoctor(args, &stdout, &stderr, runtime); code != 2 {
			t.Fatalf("doctor %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}
