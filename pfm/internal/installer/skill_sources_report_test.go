package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestInspectSkillSourcesNamesEveryState pins the doctor rows: linked,
// MISSING naming the path, CONFLICT naming the path, NOT-FETCHED, OFFLINE,
// SKIPPED, and CHECK-FAILED for a registry it could not read — never "none".
func TestInspectSkillSourcesNamesEveryState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	base := t.TempDir()
	repos := map[string]string{}
	for _, name := range []string{"linked", "missing", "conflict"} {
		repos[name] = "file://" + skillFixtureRepo(
			t,
			filepath.Join(base, name),
			map[string]string{"SKILL.md": "# " + name + "\n"},
		)
	}
	repos["unfetched"] = "file://" + filepath.Join(base, "never-fetched")
	repos["odd"] = "https://example.invalid/{OTHER_TOKEN}/odd"
	registry := writeSkillRegistry(t, home, skillRegistryJSON(repos))
	runSkillInstall(t, home, ModeApply)
	missing := filepath.Join(home, ".agents", "skills", "missing")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	conflict := filepath.Join(home, ".claude", "skills", "conflict")
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(conflict, "SKILL.md"), "# own\n")
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}

	states := map[string]SkillSourceStatus{}
	for _, status := range InspectSkillSources(home, accounts, false, false) {
		states[status.Name] = status
	}
	for name, want := range map[string]SkillSourceState{
		"linked": SkillSourceLinked, "missing": SkillSourceMissing, "conflict": SkillSourceConflict,
		"unfetched": SkillSourceNotFetched, "odd": SkillSourceSkipped,
	} {
		if states[name].State != want {
			t.Fatalf("%s state=%s want %s (%+v)", name, states[name].State, want, states[name])
		}
	}
	if got := states["missing"].Missing; len(got) != 1 || got[0] != missing {
		t.Fatalf("MISSING row does not name %s: %v", missing, got)
	}
	if got := states["conflict"].Conflicts; len(got) != 1 || got[0] != conflict {
		t.Fatalf("CONFLICT row does not name %s: %v", conflict, got)
	}
	for _, status := range InspectSkillSources(home, accounts, false, true) {
		if status.Name == "unfetched" && status.State != SkillSourceOffline {
			t.Fatalf("offline unfetched state=%s want OFFLINE", status.State)
		}
	}
	var report bytes.Buffer
	warnings, failures := ReportSkillSources(&report, home, accounts, false, false)
	if warnings != 3 || failures != 1 {
		t.Fatalf(
			"warnings=%d failures=%d, want 3 (conflict, unfetched, odd) and 1 (missing)\n%s",
			warnings,
			failures,
			report.String(),
		)
	}

	writeFixture(t, registry, "{broken")
	statuses := InspectSkillSources(home, accounts, false, false)
	if len(statuses) != 1 || statuses[0].State != SkillSourceCheckFailed ||
		!strings.Contains(statuses[0].Error, registry) {
		t.Fatalf("an unreadable registry did not report CHECK-FAILED naming %s: %+v", registry, statuses)
	}
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	if statuses := InspectSkillSources(
		home,
		accounts,
		false,
		false,
	); len(statuses) != 1 ||
		statuses[0].State != SkillSourceNoRegistry {
		t.Fatalf("an absent registry state=%+v, want NO-REGISTRY", statuses)
	}
}
