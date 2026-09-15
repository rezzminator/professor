package installer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

// stageGlobalFanoutSource writes the one source repository every global
// wiring step reads from: one global command, one global skill directory,
// and one global agent. It returns the repository root.
func stageGlobalFanoutSource(t *testing.T, home string) string {
	t.Helper()
	repo := filepath.Join(home, "blueprint")
	writeFixture(t, filepath.Join(repo, "templates", "global", "commands", "alpha.md"), "a global command\n")
	writeFixture(
		t,
		filepath.Join(repo, "templates", "global", "skills", "beta", "SKILL.md"),
		"---\nname: beta\n---\n\nbody\n",
	)
	writeFixture(t, filepath.Join(repo, "templates", "global", "agents", "gamma.md"),
		"---\nname: gamma\ndescription: Gamma role for testing.\n---\n\nbody\n")
	return repo
}

// globalFanoutEngine builds an installer pinned to two configured Claude
// accounts — the host shape `pfm install` has whenever config.Accounts holds
// more than one entry.
func globalFanoutEngine(t *testing.T, home string, apply bool, stdout io.Writer) (*engine, string, string) {
	t.Helper()
	repo := stageGlobalFanoutSource(t, home)
	first := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	return &engine{
		options: Options{
			Home: home, ConfigDir: first, ConfigDirs: []string{first, second},
			SourceRepo: repo, Stdout: stdout,
		},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       apply,
		stamp:       "fixture",
	}, first, second
}

// TestGlobalWiringReachesEveryConfiguredAccount is the defect this fanout
// exists to close: the retire paths already walk every configured Claude
// config dir, while the wiring linked global commands, skills, the handoff
// skill, and the global agents into the primary account only — so account 2
// silently had none of them. Every registry the installer would retire from
// is a registry it must install into.
func TestGlobalWiringReachesEveryConfiguredAccount(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	installer, first, second := globalFanoutEngine(t, home, true, &output)
	assets, err := assetFiles()
	if err != nil {
		t.Fatalf("assetFiles: %v", err)
	}

	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"wireGlobalCommands", installer.wireGlobalCommands},
		{"wireGlobalSkills", installer.wireGlobalSkills},
		{"wireSkills", func() error { return installer.wireSkills(assets) }},
		{"wireCodexAgents", installer.wireCodexAgents},
	} {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v\n%s", step.name, err, output.String())
		}
	}

	repo := filepath.Join(home, "blueprint")
	for _, config := range []string{first, second} {
		assertFanoutLink(t, filepath.Join(config, "commands", "alpha.md"),
			filepath.Join(repo, "templates", "global", "commands", "alpha.md"))
		assertFanoutLink(t, filepath.Join(config, "skills", "beta"),
			filepath.Join(repo, "templates", "global", "skills", "beta"))
		assertFanoutLink(t, filepath.Join(config, "agents", "gamma.md"),
			filepath.Join(repo, "templates", "global", "agents", "gamma.md"))
		assertFanoutLink(t, filepath.Join(config, "skills", "handoff", "SKILL.md"),
			filepath.Join(installer.managedRoot, "handoff.skill.md"))
	}
}

// TestGlobalWiringDryRunPlansEveryConfiguredAccount pins the rule the whole
// installer holds to: the preview IS the apply's plan. A dry run that named
// only the primary account would hide exactly the defect above from the
// operator reading `pfm install` before running it.
func TestGlobalWiringDryRunPlansEveryConfiguredAccount(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	installer, first, second := globalFanoutEngine(t, home, false, &output)
	assets, err := assetFiles()
	if err != nil {
		t.Fatalf("assetFiles: %v", err)
	}
	for _, step := range []func() error{
		installer.wireGlobalCommands,
		installer.wireGlobalSkills,
		func() error { return installer.wireSkills(assets) },
		installer.wireCodexAgents,
	} {
		if err := step(); err != nil {
			t.Fatalf("dry run: %v\n%s", err, output.String())
		}
	}

	repo := filepath.Join(home, "blueprint")
	for _, config := range []string{first, second} {
		for _, want := range []string{
			"link " + filepath.Join(config, "commands", "alpha.md") + " -> " + filepath.Join(repo, "templates", "global", "commands", "alpha.md"),
			"link " + filepath.Join(config, "skills", "beta") + " -> " + filepath.Join(repo, "templates", "global", "skills", "beta"),
			"link " + filepath.Join(config, "agents", "gamma.md") + " -> " + filepath.Join(repo, "templates", "global", "agents", "gamma.md"),
			"link " + filepath.Join(config, "skills", "handoff", "SKILL.md") + " -> " + filepath.Join(installer.managedRoot, "handoff.skill.md"),
		} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("dry run never planned %q:\n%s", want, output.String())
			}
		}
		if _, err := os.Lstat(filepath.Join(config, "commands", "alpha.md")); !os.IsNotExist(err) {
			t.Fatalf("dry run wrote into %s: %v", config, err)
		}
	}
}

func assertFanoutLink(t *testing.T, target, source string) {
	t.Helper()
	resolved, linked := resolvedLink(target)
	if !linked {
		info, err := os.Lstat(target)
		t.Fatalf("%s is not a symlink (lstat=%v err=%v), want -> %s", target, info, err, source)
	}
	if resolved != filepath.Clean(source) {
		t.Fatalf("%s -> %s, want -> %s", target, resolved, source)
	}
}

// stageGlobalAgentSources writes the two machine-global agent sources a
// recorded blueprint clone ships, plus the marker `pfm install` leaves so
// doctor can find that clone without being told where it is.
func stageGlobalAgentSources(t *testing.T, home string) string {
	t.Helper()
	repo := filepath.Join(home, "blueprint")
	agents := filepath.Join(repo, "templates", "global", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"rr", "walker"} {
		body := "---\nname: " + name + "\ndescription: " + name + " role.\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(agents, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(home, ".local", "share", "pfm", "install", "source-repo")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(repo+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

// linkGlobalAgents links every staged agent source into one account's
// agents/ registry — the shape a correct install leaves in EVERY configured
// account, not just the primary.
func linkGlobalAgents(t *testing.T, repo, configDir string, names ...string) {
	t.Helper()
	registry := filepath.Join(configDir, "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		source := filepath.Join(repo, "templates", "global", "agents", name+".md")
		if err := os.Symlink(source, filepath.Join(registry, name+".md")); err != nil {
			t.Fatal(err)
		}
	}
}

func twoReportAccounts(home string) []pfmconfig.Account {
	return []pfmconfig.Account{
		{ID: 1, ConfigDir: filepath.Join(home, ".claude")},
		{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")},
	}
}

// TestGlobalAgentsDoctorNamesTheAccountThatHasNoAgents is the visible half of
// the fanout defect: with account 1 fully linked and account 2 holding
// nothing, doctor must name account 2, name the agents it lacks, and count
// it as a warning — a per-account registry that reports nothing is exactly
// how this shipped unnoticed.
func TestGlobalAgentsDoctorNamesTheAccountThatHasNoAgents(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	linkGlobalAgents(t, repo, filepath.Join(home, ".claude"), "rr", "walker")

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 0 || failures != 1 {
		t.Fatalf(
			"warnings=%d failures=%d, want 0/1 (account 2 has no global agents)\n%s",
			warnings,
			failures,
			output.String(),
		)
	}
	if !strings.Contains(
		output.String(),
		"doctor: global-agents account=1 dir="+filepath.Join(home, ".claude")+" state=linked",
	) {
		t.Fatalf("account 1 was not reported linked:\n%s", output.String())
	}
	want := "doctor: global-agents account=2 dir=" + filepath.Join(home, ".cc", "2") + " state=MISSING names=rr,walker"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, output.String())
	}
	if !strings.Contains(output.String(), `hint="run pfm install"`) {
		t.Fatalf("a MISSING account carried no remediation hint:\n%s", output.String())
	}
}

// TestGlobalAgentsDoctorReportsEveryLinkedAccountClean is the other half:
// both accounts linked (the second through a hand-made directory symlink
// into the first's registry, the real host shape) is state=linked with no
// warning at all.
func TestGlobalAgentsDoctorReportsEveryLinkedAccountClean(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	first := filepath.Join(home, ".claude")
	linkGlobalAgents(t, repo, first, "rr", "walker")
	second := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(first, "agents"), filepath.Join(second, "agents")); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 0 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 0/0\n%s", warnings, failures, output.String())
	}
	for _, dir := range []string{first, second} {
		if !strings.Contains(output.String(), "doctor: global-agents account=") ||
			!strings.Contains(output.String(), "dir="+dir+" state=linked") {
			t.Fatalf("%s was not reported linked:\n%s", dir, output.String())
		}
	}
}

// TestGlobalAgentsDoctorDistinguishesUnreadableFromMissing pins the law an
// absence claim must never borrow: a registry entry that cannot be read is
// UNREADABLE ("we failed to look"), never MISSING ("nothing there").
func TestGlobalAgentsDoctorDistinguishesUnreadableFromMissing(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	linkGlobalAgents(t, repo, filepath.Join(home, ".claude"), "rr", "walker")
	second := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	// A self-referential registry symlink: resolving rr.md THROUGH it fails
	// with ELOOP, which is a failed look, not an absent agent.
	registry := filepath.Join(second, "agents")
	if err := os.Symlink(registry, registry); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 0 || failures != 1 {
		t.Fatalf("warnings=%d failures=%d, want 0/1\n%s", warnings, failures, output.String())
	}
	if !strings.Contains(output.String(), "doctor: global-agents account=2 dir="+second+" state=UNREADABLE error=") {
		t.Fatalf("an unreadable registry was not reported as UNREADABLE:\n%s", output.String())
	}
	if strings.Contains(output.String(), "state=MISSING") {
		t.Fatalf("an unreadable registry was rendered as absence:\n%s", output.String())
	}
}

// TestGlobalAgentsDoctorConflictNamesTheForeignLink covers the third warning
// shape: a registry entry that is a symlink pointing OUTSIDE the blueprint
// clone is never this installer's to replace, and doctor says CONFLICT
// rather than quietly calling the account linked.
func TestGlobalAgentsDoctorConflictNamesTheForeignLink(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	first := filepath.Join(home, ".claude")
	linkGlobalAgents(t, repo, first, "rr", "walker")
	second := filepath.Join(home, ".cc", "2")
	linkGlobalAgents(t, repo, second, "walker")
	elsewhere := filepath.Join(home, "mine.md")
	if err := os.WriteFile(elsewhere, []byte("an operator's own agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(second, "agents", "rr.md")); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 1 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 1/0\n%s", warnings, failures, output.String())
	}
	want := "doctor: global-agents account=2 dir=" + second + " state=CONFLICT names=rr"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, output.String())
	}
}

// TestGlobalAgentsDoctorNoSourcesIsAWarningNotACleanBill is the
// empty-enumeration law: a clone IS present (unlike a bare HOME, which is
// NO-CLONE) but ships no agent sources, so every account would trivially
// "have them all". Doctor must say it found no sources instead of certifying
// a roster it never enumerated.
func TestGlobalAgentsDoctorNoSourcesIsAWarningNotACleanBill(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".professor", "templates", "global", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 1 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 1/0\n%s", warnings, failures, output.String())
	}
	if strings.Contains(output.String(), "state=linked") {
		t.Fatalf("doctor certified accounts against an empty roster:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "state=NO-SOURCES") {
		t.Fatalf("doctor never named the source it could not enumerate:\n%s", output.String())
	}
}

// TestGlobalAgentsDoctorNoCloneIsNamedNotWarned pins the ruling: pfm installs
// without the blueprint clone, so a bare HOME with no source-repo marker and
// nothing at the default clone path has no global agents to be missing —
// this must be named, never counted as a warning, and never rendered as if
// every account were linked.
func TestGlobalAgentsDoctorNoCloneIsNamedNotWarned(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 0 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 0/0\n%s", warnings, failures, output.String())
	}
	if !strings.Contains(output.String(), "state=NO-CLONE") {
		t.Fatalf("doctor never named the missing clone:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "doctor: global-agents source=") {
		t.Fatalf("doctor never named the source it could not find:\n%s", output.String())
	}
	if strings.Contains(output.String(), "state=linked") {
		t.Fatalf("doctor certified accounts with no clone to check them against:\n%s", output.String())
	}
}

// TestGlobalAgentsDoctorUnreadableMarkerIsUnresolvedNotNoClone pins the other
// half: a marker path that could not even be LOOKED at (its parent directory
// unreadable) is UNRESOLVED — "we failed to look" — never NO-CLONE, which
// would misreport a failed look as an absent clone.
func TestGlobalAgentsDoctorUnreadableMarkerIsUnresolvedNotNoClone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip(
			"running as root — chmod 000 never blocks root's own Lstat, so the unreadable-marker fixture cannot be produced",
		)
	}
	home := t.TempDir()
	markerDir := filepath.Join(home, ".local", "share", "pfm", "install")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(markerDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(markerDir, 0o755) })

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if warnings != 1 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 1/0\n%s", warnings, failures, output.String())
	}
	if !strings.Contains(output.String(), "state=UNRESOLVED") {
		t.Fatalf("an unreadable marker was not reported as UNRESOLVED:\n%s", output.String())
	}
	if strings.Contains(output.String(), "state=NO-CLONE") {
		t.Fatalf("a failed look at the marker was rendered as an absent clone:\n%s", output.String())
	}
}

// TestGlobalAgentsDoctorClaudeAbsentIsNamedNotWarnedPerAccount pins D2's
// second row: with Claude absent, every configured account reports
// state=NO-CLAUDE, no account is ever certified state=linked (nothing was
// checked), and none of it counts a warning — the installer never wires an
// account with no Claude Code binary to run.
func TestGlobalAgentsDoctorClaudeAbsentIsNamedNotWarnedPerAccount(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	linkGlobalAgents(t, repo, filepath.Join(home, ".claude"), "rr", "walker")

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), true)
	if warnings != 0 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 0/0\n%s", warnings, failures, output.String())
	}
	if strings.Count(output.String(), "state=NO-CLAUDE") != 2 {
		t.Fatalf("want one NO-CLAUDE line per account:\n%s", output.String())
	}
	if strings.Contains(output.String(), "state=linked") {
		t.Fatalf("an absent-Claude account was certified linked:\n%s", output.String())
	}
}

// TestInspectGlobalAgentsSourceDirectoryStates pins the three-way split of a
// source directory that cannot be enumerated: absent entirely is NO-CLONE
// (nothing to link, never a warning), present but empty is NO-SOURCES (a
// broken clone, still a warning), and unreadable (e.g. the agents path is a
// file, not a directory) is UNREADABLE — "we failed to look", never MISSING.
func TestInspectGlobalAgentsSourceDirectoryStates(t *testing.T) {
	for _, tt := range []struct {
		name  string
		stage func(t *testing.T, home string)
		want  GlobalAgentsState
	}{
		{
			name:  "no agents directory at all",
			stage: func(_ *testing.T, _ string) {},
			want:  GlobalAgentsNoClone,
		},
		{
			name: "empty agents directory",
			stage: func(t *testing.T, home string) {
				if err := os.MkdirAll(filepath.Join(home, ".professor", "templates", "global", "agents"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: GlobalAgentsNoSources,
		},
		{
			name: "agents path is a file, not a directory",
			stage: func(t *testing.T, home string) {
				dir := filepath.Join(home, ".professor", "templates", "global")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "agents"), []byte("not a directory\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: GlobalAgentsUnreadable,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			tt.stage(t, home)
			statuses := InspectGlobalAgents(home, nil, false)
			if len(statuses) != 1 {
				t.Fatalf("statuses=%d, want 1: %+v", len(statuses), statuses)
			}
			if statuses[0].State != tt.want {
				t.Fatalf("state=%s, want %s: %+v", statuses[0].State, tt.want, statuses[0])
			}
		})
	}
}
