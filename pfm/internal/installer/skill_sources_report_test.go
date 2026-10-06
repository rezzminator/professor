package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

	states := map[string]SkillSourceStatus{}
	for _, status := range InspectSkillSources(home, false) {
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
	for _, status := range InspectSkillSources(home, true) {
		if status.Name == "unfetched" && status.State != SkillSourceOffline {
			t.Fatalf("offline unfetched state=%s want OFFLINE", status.State)
		}
	}
	var report bytes.Buffer
	warnings, failures := ReportSkillSources(&report, home, false)
	if warnings != 3 || failures != 1 {
		t.Fatalf(
			"warnings=%d failures=%d, want 3 (conflict, unfetched, odd) and 1 (missing)\n%s",
			warnings,
			failures,
			report.String(),
		)
	}

	writeFixture(t, registry, "{broken")
	statuses := InspectSkillSources(home, false)
	if len(statuses) != 1 || statuses[0].State != SkillSourceCheckFailed ||
		!strings.Contains(statuses[0].Error, registry) {
		t.Fatalf("an unreadable registry did not report CHECK-FAILED naming %s: %+v", registry, statuses)
	}
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	if statuses := InspectSkillSources(home, false); len(statuses) != 1 ||
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
	warnings, failures := ReportSkillSources(&report, home, false)
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
	statuses := InspectSkillSources(home, false)
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

	for want, env := range map[SkillSourceState]*paths.MapEnv{
		SkillSourceNotFetched: {},
		SkillSourceOffline:    {Values: map[string]string{paths.EnvSkillSourcesOffline: "1"}},
	} {
		var report bytes.Buffer
		ReportGlobalRegistries(&report, home, false, env)
		if row := skillSourceRow(t, report.String(), "god-speed"); !strings.Contains(row, "state="+string(want)+" ") &&
			!strings.HasSuffix(row, "state="+string(want)) {
			t.Fatalf("env %v: row %q, want state=%s", env.Values, row, want)
		}
	}
}

// TestReportGlobalRegistriesChecksStoreLinksWithoutClaude checks store skill links even when Claude is absent.
func TestReportGlobalRegistriesChecksStoreLinksWithoutClaude(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	link := filepath.Join(home, ".claude", "skills", "god-speed")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	var report bytes.Buffer
	ReportGlobalRegistries(&report, home, true, &paths.MapEnv{})
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
	statuses := InspectSkillSources(home, false)
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

	statuses := InspectSkillSources(home, false)

	if len(statuses) != 1 || statuses[0].State != SkillSourceConflict ||
		len(statuses[0].Conflicts) != 1 || statuses[0].Conflicts[0] != storeRoot {
		t.Fatalf("a symlinked store root: %+v", statuses)
	}
}

// TestSkillFileStatesReadApartOnBothSurfaces pins F35: a store whose root
// SKILL.md exists but is unusable (a directory here) is SKILL-SOURCE-INVALID
// on install, never SKILL-SOURCE-MISSING, and SKIPPED in doctor, a warning
// with no install hint, never CHECK-FAILED.
func TestSkillFileStatesReadApartOnBothSurfaces(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"gs": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	skill := filepath.Join(skillStoreRoot(home), "gs", "SKILL.md")
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(skill, "nested.md"), "# nested\n")

	output := runSkillInstall(t, home, ModeApply, func(options *Options) { options.SkillSourcesOffline = true })

	if strings.Contains(output, "SKILL-SOURCE-MISSING gs") || !strings.Contains(output, "SKILL-SOURCE-INVALID gs: ") {
		t.Fatalf("an unusable SKILL.md did not read as SKILL-SOURCE-INVALID on install:\n%s", output)
	}

	var report bytes.Buffer
	warnings, failures := ReportSkillSources(&report, home, false)
	if row := skillSourceRow(t, report.String(), "gs"); !strings.Contains(row, "state=SKIPPED ") ||
		strings.Contains(row, "hint=") || warnings != 1 || failures != 0 {
		t.Fatalf("doctor: warnings=%d failures=%d row %q, want one SKIPPED warning", warnings, failures, row)
	}
}

// TestInspectSkillSourcesChecksTheStoreInstallWrites checks the store link regardless of account roster.
func TestInspectSkillSourcesChecksTheStoreInstallWrites(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	second := filepath.Join(home, ".cc", "2")
	runSkillInstall(
		t,
		home,
		ModeApply,
		func(options *Options) { options.ClaudeAccounts = []pfmconfig.Account{{ID: 2, ConfigDir: second}} },
	)
	link := filepath.Join(home, ".claude", "skills", "god-speed")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	statuses := InspectSkillSources(home, false)

	if len(statuses) != 1 || statuses[0].State != SkillSourceMissing ||
		len(statuses[0].Missing) != 1 || statuses[0].Missing[0] != link {
		t.Fatalf("the default account link install writes was not checked: %+v", statuses)
	}
}

func TestSkillFileThroughARegularFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	store := filepath.Join(skillStoreRoot(home), "god-speed")
	skill := filepath.Join(store, "SKILL.md")
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(store, "notadir"), "file\n")
	symlinkFixture(t, "notadir/SKILL.md", skill)
	err := checkSkillFile(store)
	if !errors.Is(err, errSkillFileUnusable) {
		t.Errorf("a link through a file is not classified as unusable: %v", err)
	}
	output := runSkillInstall(t, home, ModeApply, func(options *Options) { options.SkillSourcesOffline = true })
	want := "  skip    SKILL-SOURCE-INVALID god-speed: " + err.Error() + "\n"
	if !strings.Contains(output, want) {
		t.Errorf("offline install missing %q:\n%s", want, output)
	}
	var report bytes.Buffer
	warnings, failures := ReportSkillSources(&report, home, false)
	row := skillSourceRow(t, report.String(), "god-speed")
	wantRow := "doctor: skill-source name=god-speed store=" + store + " state=SKIPPED error=" + err.Error()
	if row != wantRow || strings.Contains(row, "hint=") || warnings != 1 || failures != 0 {
		t.Fatalf("doctor warnings=%d failures=%d row=%q, want %q", warnings, failures, row, wantRow)
	}
}

func TestInspectSkillSourcesConfiguredLinkDirs(t *testing.T) {
	t.Parallel()
	for _, removed := range []bool{false, true} {
		name := "linked"
		if removed {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
			configDir := filepath.Join(home, ".cc", "x")
			configure := func(options *Options) { options.ConfigDir = configDir }
			runSkillInstall(t, home, ModeApply, configure)
			ledger := skillLinkLedgerPath(home)
			content, err := os.ReadFile(ledger)
			if err != nil {
				t.Errorf("install did not write its link ledger: %v", err)
			} else {
				var got skillLinkLedger
				want := skillLinkLedger{Version: 1, LinkDirs: []string{
					filepath.Join(configDir, "skills"), filepath.Join(home, ".agents", "skills"),
				}}
				if err := json.Unmarshal(content, &got); err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("ledger=%s, decoded=%+v err=%v; want %+v", content, got, err, want)
				}
				wantBytes, err := json.MarshalIndent(want, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(content, append(wantBytes, '\n')) {
					t.Errorf("ledger bytes = %q, want indented JSON with a newline", content)
				}
			}
			if output := runSkillInstall(t, home, ModeApply, configure); strings.Contains(output, ledger) {
				t.Errorf("second apply named an unchanged ledger:\n%s", output)
			}
			link := filepath.Join(configDir, "skills", "god-speed")
			wantState := SkillSourceLinked
			if removed {
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				wantState = SkillSourceMissing
			}
			statuses := InspectSkillSources(home, false)
			if len(statuses) != 1 || statuses[0].State != wantState {
				t.Fatalf("configured link state = %+v, want %s", statuses, wantState)
			}
			if removed && !reflect.DeepEqual(statuses[0].Missing, []string{link}) {
				t.Fatalf("missing configured link = %v, want %s", statuses[0].Missing, link)
			}
			var report bytes.Buffer
			warnings, failures := ReportSkillSources(&report, home, false)
			wantFailures := 0
			if removed {
				wantFailures = 1
			}
			if warnings != 0 || failures != wantFailures {
				t.Fatalf(
					"configured link warnings=%d failures=%d, want 0/%d\n%s",
					warnings,
					failures,
					wantFailures,
					report.String(),
				)
			}
		})
	}
}

func TestInspectSkillSourcesLegacyLinkDirs(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	if err := os.Remove(skillLinkLedgerPath(home)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "skills", "god-speed")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	statuses := InspectSkillSources(home, false)
	if len(statuses) != 1 || statuses[0].State != SkillSourceMissing ||
		!reflect.DeepEqual(statuses[0].Missing, []string{link}) {
		t.Fatalf("legacy missing link = %+v, want %s", statuses, link)
	}
	var report bytes.Buffer
	if warnings, failures := ReportSkillSources(&report, home, false); warnings != 0 || failures != 1 {
		t.Fatalf("legacy missing link warnings=%d failures=%d\n%s", warnings, failures, report.String())
	}
}

func TestInspectSkillSourcesLinkLedgerFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, content, cause string
	}{
		{"decode", "{broken", "invalid character"},
		{"read", "", "is a directory"},
		{"version", `{"version":2,"link_dirs":["/fixture/skills"]}`, "version 2, want 1"},
		{"empty", `{"version":1,"link_dirs":[]}`, "link_dirs is empty"},
		{"relative", `{"version":1,"link_dirs":["relative/skills"]}`, "link dir relative/skills is not absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file:///nowhere"}))
			ledger := skillLinkLedgerPath(home)
			if tc.name == "read" {
				writeFixture(t, filepath.Join(ledger, "blocker"), "file\n")
			} else {
				writeFixture(t, ledger, tc.content)
			}
			statuses := InspectSkillSources(home, false)
			if len(statuses) != 1 || statuses[0].Path != ledger || statuses[0].State != SkillSourceCheckFailed ||
				!strings.Contains(statuses[0].Error, "skill link ledger "+ledger+": ") ||
				!strings.Contains(statuses[0].Error, tc.cause) {
				t.Fatalf("unreadable ledger = %+v, want CHECK-FAILED naming %s and %q", statuses, ledger, tc.cause)
			}
			var report bytes.Buffer
			if warnings, failures := ReportSkillSources(&report, home, false); warnings != 0 || failures != 1 {
				t.Fatalf("ledger failure warnings=%d failures=%d\n%s", warnings, failures, report.String())
			}
		})
	}
}
