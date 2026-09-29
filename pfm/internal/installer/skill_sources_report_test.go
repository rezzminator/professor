package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
	for _, status := range InspectSkillSources(home, accounts, false) {
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
	for _, status := range InspectSkillSources(home, accounts, true) {
		if status.Name == "unfetched" && status.State != SkillSourceOffline {
			t.Fatalf("offline unfetched state=%s want OFFLINE", status.State)
		}
	}
	var report bytes.Buffer
	warnings, failures := ReportSkillSources(&report, home, accounts, false)
	if warnings != 3 || failures != 1 {
		t.Fatalf(
			"warnings=%d failures=%d, want 3 (conflict, unfetched, odd) and 1 (missing)\n%s",
			warnings,
			failures,
			report.String(),
		)
	}

	writeFixture(t, registry, "{broken")
	statuses := InspectSkillSources(home, accounts, false)
	if len(statuses) != 1 || statuses[0].State != SkillSourceCheckFailed ||
		!strings.Contains(statuses[0].Error, registry) {
		t.Fatalf("an unreadable registry did not report CHECK-FAILED naming %s: %+v", registry, statuses)
	}
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	if statuses := InspectSkillSources(home, accounts, false); len(statuses) != 1 ||
		statuses[0].State != SkillSourceNoRegistry {
		t.Fatalf("an absent registry state=%+v, want NO-REGISTRY", statuses)
	}
}

// skillSourceRow returns the one "doctor: skill-source" line naming name.
func skillSourceRow(t *testing.T, report, name string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "doctor: skill-source ") && strings.Contains(line, "name="+name+" ") {
			return line
		}
	}
	t.Fatalf("no skill-source row for %s:\n%s", name, report)
	return ""
}

// TestInspectSkillSourcesRecordedCloneGoneIsAFailure pins the F5 rebuttal: a
// recorded clone that is gone never reads as the benign NO-CLONE — reading
// the marker fails first, and that is a counted CHECK-FAILED naming the path.
func TestInspectSkillSourcesRecordedCloneGoneIsAFailure(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	gone := filepath.Join(t.TempDir(), "moved-away")
	writeFixture(t, filepath.Join(gone, "VERSION"), "0.0.0\n")
	if err := paths.WriteSourceRepoMarker(home, gone); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	warnings, failures := ReportSkillSources(&report, home, nil, false)
	if failures != 1 || warnings != 0 || !strings.Contains(report.String(), "state=CHECK-FAILED") ||
		!strings.Contains(report.String(), gone) {
		t.Fatalf("a recorded clone that is gone: warnings=%d failures=%d\n%s", warnings, failures, report.String())
	}
}

// TestInspectSkillSourcesResolvesTheOwnerLikeInstall pins F9: a clone without
// a manifest owner resolves {GH_USER} through the release manifest URL, as
// pfm install does, instead of reporting CHECK-FAILED.
func TestInspectSkillSourcesResolvesTheOwnerLikeInstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeSkillRegistry(
		t,
		home,
		skillRegistryJSON(map[string]string{"god-speed": "https://github.com/{GH_USER}/god-speed"}),
	)
	statuses := InspectSkillSources(home, nil, false)
	if len(statuses) != 1 || statuses[0].Name != "god-speed" || statuses[0].State != SkillSourceNotFetched {
		t.Fatalf("doctor did not resolve {GH_USER} the way install does: %+v", statuses)
	}
}

// TestReportGlobalRegistriesReadsOfflineFromTheInjectedEnv pins F10: doctor's
// OFFLINE comes from the environment it is handed, not from its own process.
func TestReportGlobalRegistriesReadsOfflineFromTheInjectedEnv(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file:///nowhere/god-speed"}))
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}
	for want, env := range map[SkillSourceState]*paths.MapEnv{
		SkillSourceNotFetched: {},
		SkillSourceOffline:    {Values: map[string]string{paths.EnvSkillSourcesOffline: "1"}},
	} {
		var report bytes.Buffer
		ReportGlobalRegistries(&report, home, accounts, false, env)
		if row := skillSourceRow(t, report.String(), "god-speed"); !strings.Contains(row, "state="+string(want)+" ") &&
			!strings.HasSuffix(row, "state="+string(want)) {
			t.Fatalf("env %v: row %q, want state=%s", env.Values, row, want)
		}
	}
}

// TestReportGlobalRegistriesChecksAccountLinksWithoutClaude pins F15: doctor
// checks every link install writes, the account links included, even when no
// Claude Code binary is installed.
func TestReportGlobalRegistriesChecksAccountLinksWithoutClaude(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	link := filepath.Join(home, ".claude", "skills", "god-speed")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}
	var report bytes.Buffer
	ReportGlobalRegistries(&report, home, accounts, true, &paths.MapEnv{})
	if row := skillSourceRow(t, report.String(), "god-speed"); !strings.Contains(row, "state=MISSING missing="+link) {
		t.Fatalf("a missing account link was not checked with Claude absent: %q", row)
	}
}

// TestInspectSkillSourcesNonDirectoryStoreNamesTheRemedy pins F3's doctor
// half: a stray file at the store path is a CONFLICT naming it, whose hint is
// to remove it — never a CHECK-FAILED that pfm install cannot clear.
func TestInspectSkillSourcesNonDirectoryStoreNamesTheRemedy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file:///nowhere/god-speed"}))
	store := filepath.Join(skillStoreRoot(home), "god-speed")
	writeFixture(t, store, "stray\n")
	statuses := InspectSkillSources(home, nil, false)
	if len(statuses) != 1 || statuses[0].State != SkillSourceConflict ||
		len(statuses[0].Conflicts) != 1 || statuses[0].Conflicts[0] != store {
		t.Fatalf("a non-directory store: %+v", statuses)
	}
}

// TestInspectSkillSourcesRefuseASymlinkedStoreRoot pins F26: a store root that
// is a link is a CONFLICT naming it on every row — install refuses it, so
// doctor never reads through it as linked.
func TestInspectSkillSourcesRefuseASymlinkedStoreRoot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	storeRoot := skillStoreRoot(home)
	moved := filepath.Join(filepath.Dir(storeRoot), "moved-skills")
	if err := os.Rename(storeRoot, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, storeRoot); err != nil {
		t.Fatal(err)
	}
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}

	statuses := InspectSkillSources(home, accounts, false)

	if len(statuses) != 1 || statuses[0].State != SkillSourceConflict ||
		len(statuses[0].Conflicts) != 1 || statuses[0].Conflicts[0] != storeRoot {
		t.Fatalf("a symlinked store root: %+v", statuses)
	}
}

// TestInspectSkillSourcesChecksTheDefaultAccountInstallWrites pins F29: with
// accounts that omit ~/.claude, doctor still checks the link install writes
// there.
func TestInspectSkillSourcesChecksTheDefaultAccountInstallWrites(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	second := filepath.Join(home, ".cc", "2")
	runSkillInstall(t, home, ModeApply, func(options *Options) { options.ConfigDirs = []string{second} })
	link := filepath.Join(home, ".claude", "skills", "god-speed")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	statuses := InspectSkillSources(home, []pfmconfig.Account{{ID: 2, ConfigDir: second}}, false)

	if len(statuses) != 1 || statuses[0].State != SkillSourceMissing ||
		len(statuses[0].Missing) != 1 || statuses[0].Missing[0] != link {
		t.Fatalf("the default account link install writes was not checked: %+v", statuses)
	}
}
