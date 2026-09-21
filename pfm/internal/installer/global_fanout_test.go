package installer

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

func TestGlobalCommandsReachConfigDirAndConfigDirs(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalFanoutSource(t, home)
	primary := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	third := filepath.Join(home, ".cc", "3")
	installer := &engine{
		options: Options{
			Home: home, ConfigDir: primary, ConfigDirs: []string{second, third},
			SourceRepo: repo, Stdout: io.Discard,
		},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       true,
		stamp:       "fixture",
	}

	if err := installer.wireGlobalCommands(); err != nil {
		t.Fatalf("wireGlobalCommands: %v", err)
	}
	source := filepath.Join(repo, "templates", "global", "commands", "alpha.md")
	for _, configDir := range []string{primary, second, third} {
		assertFanoutLink(t, filepath.Join(configDir, "commands", "alpha.md"), source)
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

// installGlobalCodexRoles writes the {home}/.codex/agents/<name>.toml role
// FILES in the exact shape a clean install leaves: regular files holding the
// bytes this binary compiles. It goes through CompileGlobalRoles rather than
// hand-rolling content, because doctor compares disk against that compiler —
// a fixture that merely looked plausible would certify a state no install
// produces. Tests exercising the per-account .claude/agents/*.md report call
// it so they do not pick up an unrelated codex-agents failure by accident.
func installGlobalCodexRoles(t *testing.T, home, repo string, names ...string) {
	t.Helper()
	registry := filepath.Join(home, ".codex", "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	roles, err := codexgen.CompileGlobalRoles(home, repo)
	if err != nil {
		t.Fatalf("CompileGlobalRoles: %v", err)
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	installed := 0
	for _, role := range roles {
		if !wanted[role.Name] {
			continue
		}
		if err := os.WriteFile(filepath.Join(registry, role.Name+".toml"), role.Content, 0o644); err != nil {
			t.Fatal(err)
		}
		installed++
	}
	if installed != len(names) {
		t.Fatalf(
			"staged %d of %d requested roles — the fixture names an agent the clone does not ship",
			installed,
			len(names),
		)
	}
}

// assertOwnedCodexRole fails the test unless path is the shape Codex loads and
// pfm owns: a regular file — never the symlink the loader refuses — whose
// first line is the generated marker.
func assertOwnedCodexRole(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not the regular file Codex loads (info=%v err=%v)", path, info, err)
	}
	if !codexgen.GeneratedGlobalRole([]byte(readFixture(t, path))) {
		t.Fatalf("%s carries no generated marker — pfm cannot prove it owns the file", path)
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
	installGlobalCodexRoles(t, home, repo, "rr", "walker")

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
	installGlobalCodexRoles(t, home, repo, "rr", "walker")
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
	installGlobalCodexRoles(t, home, repo, "rr", "walker")
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
	installGlobalCodexRoles(t, home, repo, "rr", "walker")
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

// TestGlobalAgentsDoctorNamesASymlinkedCodexRole is the regression for the
// defect that made every machine-global role unspawnable: Codex opens a role
// with O_NOFOLLOW and rejects a symlink as "agent type is currently not
// available", and the doctor that shipped before this reported exactly that
// registry as "linked" — a verdict that read the same whether the roles worked
// or none of them did. SYMLINK is its own outcome, never MISSING: the file is
// there, and that is precisely why the operator cannot see what is wrong.
func TestGlobalAgentsDoctorNamesASymlinkedCodexRole(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	linkGlobalAgents(t, repo, filepath.Join(home, ".claude"), "rr", "walker")
	linkGlobalAgents(t, repo, filepath.Join(home, ".cc", "2"), "rr", "walker")
	installGlobalCodexRoles(t, home, repo, "rr", "walker")
	registry := filepath.Join(home, ".codex", "agents")
	// The pre-migration shape: the role's bytes live in the retired generated
	// store and the registry entry is a link to them.
	legacy := filepath.Join(paths.LegacyGeneratedCodexAgentsDir(home), "rr.toml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(registry, "rr.toml"), legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, filepath.Join(registry, "rr.toml")); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if failures != 1 {
		t.Fatalf(
			"warnings=%d failures=%d, want failures=1 (a symlinked role cannot spawn)\n%s",
			warnings,
			failures,
			output.String(),
		)
	}
	want := "doctor: global-agents source=" + registry + " state=SYMLINK names=rr"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, output.String())
	}
	if !strings.Contains(output.String(), `hint="run pfm install --yes"`) {
		t.Fatalf("a SYMLINK row carried no remediation:\n%s", output.String())
	}
}

// TestGlobalAgentsDoctorNamesMismatchedMissingAndUnreadableCodexRoles pins the
// remaining three outcomes, and pins that one bucket never hides another: a
// role whose bytes drifted from the compiler is MISMATCH, an absent one is
// still named in the same line, and a role file that cannot be read at all is
// UNREADABLE — "we failed to look" is not "nothing there".
func TestGlobalAgentsDoctorNamesMismatchedMissingAndUnreadableCodexRoles(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	linkGlobalAgents(t, repo, filepath.Join(home, ".claude"), "rr", "walker")
	linkGlobalAgents(t, repo, filepath.Join(home, ".cc", "2"), "rr", "walker")
	registry := filepath.Join(home, ".codex", "agents")

	// rr drifted, walker absent.
	installGlobalCodexRoles(t, home, repo, "rr")
	if err := os.WriteFile(filepath.Join(registry, "rr.toml"), []byte("name = \"rr\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if failures != 1 {
		t.Fatalf("failures=%d, want 1\n%s", failures, output.String())
	}
	want := "doctor: global-agents source=" + registry + " state=MISMATCH names=rr missing=walker"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output missing %q — one bucket hid the other:\n%s", want, output.String())
	}

	// Both absent: MISSING names them, and nothing claims a mismatch.
	if err := os.RemoveAll(registry); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if _, failures = ReportGlobalAgents(&output, home, twoReportAccounts(home), false); failures != 1 {
		t.Fatalf("failures=%d, want 1\n%s", failures, output.String())
	}
	if !strings.Contains(output.String(), "source="+registry+" state=MISSING names=rr,walker") {
		t.Fatalf("an absent registry was not reported MISSING:\n%s", output.String())
	}

	// A role file that is a directory cannot be read as a role at all: the
	// check FAILED, which must not render as the absence just proven above.
	if err := os.MkdirAll(filepath.Join(registry, "rr.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if _, failures = ReportGlobalAgents(&output, home, twoReportAccounts(home), false); failures != 1 {
		t.Fatalf("failures=%d, want 1\n%s", failures, output.String())
	}
	if !strings.Contains(output.String(), "source="+registry+" state=UNREADABLE names=rr missing=walker") {
		t.Fatalf("an unloadable role file was not distinguished from an absent one:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "error=") {
		t.Fatalf("an UNREADABLE row carried no error — a failed look that names no failure:\n%s", output.String())
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

// TestUninstallRemovesOwnedCodexRolesAndTheLegacyGeneratedDirectory is the
// uninstall half of role ownership: the marker-carrying role files this
// installer wrote go, and so does the retired generated directory an earlier
// layout used as the role store — leaving neither behind.
func TestUninstallRemovesOwnedCodexRolesAndTheLegacyGeneratedDirectory(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	role := filepath.Join(home, ".codex", "agents", "alpha.toml")
	assertOwnedCodexRole(t, role)
	// A leftover from the retired layout, which uninstall owes removal too.
	legacy := paths.LegacyGeneratedCodexAgentsDir(home)
	writeFixture(t, filepath.Join(legacy, "alpha.toml"), "name = \"alpha\"\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the retired generated Codex agents directory: %v", err)
	}
	if _, err := os.Lstat(role); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the owned Codex role file: %v", err)
	}
}

// TestUninstallLeavesAForeignCodexAgentUntouched pins the boundary the
// ownership-by-target rule exists to hold: a regular file (or a link
// pointing somewhere the generated directory never wrote) at
// ~/.codex/agents/<name>.toml is an operator's own — uninstall must never
// delete it.
func TestUninstallLeavesAForeignCodexAgentUntouched(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, ".codex", "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(registry, "mine.toml")
	writeFixture(t, foreign, "name = \"mine\"\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, foreign); got != "name = \"mine\"\n" {
		t.Fatalf("uninstall touched an operator-owned Codex agent file: %q", got)
	}
}

// TestUninstallRetiresAPreMigrationLegacyCodexAgentLink covers the
// global_fanout.go:441 INFO gap: a host that upgraded to the generated-
// directory layout without ever rerunning `pfm install`/`pfm codex agents`
// in between can still carry a pre-migration ~/.codex/agents/<name>.toml
// link aimed at the retired in-clone twin
// (<blueprint>/templates/global/agents/<name>.toml). This installer wrote
// that link too, so uninstall must retire it — not leave it behind as if it
// were an operator's own file, the way TestUninstallLeavesAForeignCodexAgentUntouched
// pins for a GENUINELY foreign link.
func TestUninstallRetiresAPreMigrationLegacyCodexAgentLink(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "blueprint")
	if err := os.MkdirAll(filepath.Join(repo, "templates", "global", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, ".codex", "agents")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyTwin := filepath.Join(repo, "templates", "global", "agents", "rr.toml")
	legacyLink := filepath.Join(registry, "rr.toml")
	if err := os.Symlink(legacyTwin, legacyLink); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(legacyLink); !os.IsNotExist(err) {
		t.Fatalf("pre-migration legacy Codex agent link survived uninstall: %v", err)
	}
}

// TestGlobalAgentsDoctorCountsADeclaredVariantAsOwed: a variant is an agent
// the install owes every account; a host linked for the originals alone is
// MISSING the variant by name, and a declaration that cannot render is
// UNREADABLE with its error — never a roster quietly short of variants.
func TestGlobalAgentsDoctorCountsADeclaredVariantAsOwed(t *testing.T) {
	home := t.TempDir()
	repo := stageGlobalAgentSources(t, home)
	declaration := filepath.Join(repo, "templates", "global", "agents", "variants.json")
	if err := os.WriteFile(
		declaration,
		[]byte(`{"super-rr":{"from":"rr","description":"deep rr."}}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	for _, account := range twoReportAccounts(home) {
		linkGlobalAgents(t, repo, account.ConfigDir, "rr", "walker")
	}
	installGlobalCodexRoles(t, home, repo, "rr", "walker")

	var output bytes.Buffer
	_, failures := ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if failures != 3 {
		t.Fatalf(
			"failures=%d, want 3 (both accounts and the Codex registry lack super-rr)\n%s",
			failures,
			output.String(),
		)
	}
	if !strings.Contains(output.String(), "state=MISSING names=super-rr") {
		t.Fatalf("the missing variant was not named:\n%s", output.String())
	}

	if err := os.WriteFile(declaration, []byte(`{"super-rr":{"from":"ghost"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	_, failures = ReportGlobalAgents(&output, home, twoReportAccounts(home), false)
	if failures != 1 || !strings.Contains(output.String(), "state=UNREADABLE") ||
		!strings.Contains(output.String(), "super-rr") {
		t.Fatalf(
			"failures=%d; a broken declaration must be UNREADABLE naming the variant:\n%s",
			failures,
			output.String(),
		)
	}
}

// TestInstallRetiresACodexRoleTheCloneNoLongerShips is the live-host defect
// this sweep closes: three roles removed from the roster left their registry
// entries behind — stale symlinks Codex refuses to load, still occupying their
// agent_type. The install that wrote a role owes its removal; a file the
// operator wrote under a retired name is still never touched.
func TestInstallRetiresACodexRoleTheCloneNoLongerShips(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	registry := filepath.Join(home, ".codex", "agents")
	// A role pfm wrote for a source that is gone, and a pre-migration link to
	// the retired store for another.
	orphan := filepath.Join(registry, "retired.toml")
	writeFixture(
		t,
		orphan,
		"# Generated by pfm codex build from templates/global/agents/retired.md\nname = \"retired\"\n",
	)
	legacy := filepath.Join(paths.LegacyGeneratedCodexAgentsDir(home), "gone.toml")
	writeFixture(t, legacy, "name = \"gone\"\n")
	orphanLink := filepath.Join(registry, "gone.toml")
	if err := os.Symlink(legacy, orphanLink); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(registry, "mine.toml")
	writeFixture(t, mine, "name = \"mine\"\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{orphan, orphanLink} {
		if _, err := os.Lstat(retired); !os.IsNotExist(err) {
			t.Fatalf("install left a role the clone no longer ships at %s: %v", retired, err)
		}
	}
	if got := readFixture(t, mine); got != "name = \"mine\"\n" {
		t.Fatalf("the sweep touched an operator's own role file: %q", got)
	}
	assertOwnedCodexRole(t, filepath.Join(registry, "alpha.toml"))
}
