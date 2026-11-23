package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// TestRetirePredecessorsRemovesConfigFiles pins the configFiles door: a
// literal path and every glob match retire under their reason, a regular file
// or a link backed up first (an operator's own link stays restorable), and a
// file the lists do not name stays. Each retired embedded asset's link goes
// from every account's config dir before it.
func TestRetirePredecessorsRemovesConfigFiles(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	literal := filepath.Join(config, "bin", "cx-kill.sh")
	backup := filepath.Join(config, "bin", "cx-recover.sh.pre-professor-20260101")
	kept := filepath.Join(config, "bin", "cx-recover.sh")
	for _, path := range []string{literal, backup, kept} {
		writeFixture(t, path, "old\n")
	}
	managed := filepath.Join(home, "managed")
	staleManaged := filepath.Join(managed, "handoff.skill.md")
	writeFixture(t, staleManaged, "embedded")
	staleReload := filepath.Join(managed, "reload.command.md")
	writeFixture(t, staleReload, "embedded")
	reloadLink := filepath.Join(config, "commands", "reload.md")
	symlinkFixture(t, staleReload, reloadLink)
	oldLink := filepath.Join(config, "skills", "handoff", "SKILL.md")
	symlinkFixture(t, staleManaged, oldLink)
	account := filepath.Join(home, "account2")
	foreign := filepath.Join(account, "skills", "handoff", "SKILL.md")
	writeFixture(t, foreign, "user's own\n")
	accountReloadLink := filepath.Join(account, "commands", "reload.md")
	symlinkFixture(t, staleReload, accountReloadLink)
	operatorFile := filepath.Join(home, "mine", "old.md")
	writeFixture(t, operatorFile, "operator's own\n")
	operatorLink := filepath.Join(config, "commands", "old.md")
	symlinkFixture(t, operatorFile, operatorLink)
	var output bytes.Buffer
	installer := &engine{
		managedRoot: managed,
		options: Options{
			Home: home, ConfigDir: config, Stdout: &output,
			ClaudeAccounts: []pfmconfig.Account{{ID: 2, ConfigDir: account}},
			retired: &retired.Lists{ConfigFiles: []retired.ConfigFile{
				{Path: "bin/cx-kill.sh", Reason: "native pfm command"},
				{Path: "bin/cx-recover.sh.pre-professor-*", Reason: "retired recovery artifact"},
				{Path: "commands/reload.md", Reason: "retired /reload command link; successor /pfm:reload"},
				{Path: "commands/old.md", Reason: "retired command link"},
			}},
		},
		apply: true,
		stamp: "20260101-000000",
	}
	if err := installer.retirePredecessors(); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(home, ".local", "state", "pfm", "retired-config")
	literalSaved := filepath.Join(saved, "cx-kill.sh.pre-professor-20260101-000000")
	backupSaved := filepath.Join(saved, "cx-recover.sh.pre-professor-20260101.pre-professor-20260101-000000")
	linkSaved := filepath.Join(saved, "old.md.pre-professor-20260101-000000")
	want := "  change  retire " + oldLink + " (the /handoff skill is a global command now)\n" +
		"  ok      removed empty " + filepath.Dir(oldLink) + "\n" +
		"  change  retire " + staleManaged + " (embedded /handoff skill retired)\n" +
		"  change  retire " + reloadLink + " (the /reload command is /pfm:reload now)\n" +
		"  change  retire " + accountReloadLink + " (the /reload command is /pfm:reload now)\n" +
		"  change  retire " + staleReload + " (embedded /reload command retired)\n" +
		"  change  retire " + literal + " (native pfm command; backup: " + literalSaved + ")\n" +
		"  change  retire " + backup + " (retired recovery artifact; backup: " + backupSaved + ")\n" +
		"  change  retire " + operatorLink + " (retired command link; backup: " + linkSaved + ")\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	for _, saved := range []string{literalSaved, backupSaved} {
		if got := readFixture(t, saved); got != "old\n" {
			t.Fatalf("backup %s = %q, want the retired file's content", saved, got)
		}
	}
	if target, err := os.Readlink(linkSaved); err != nil || target != operatorFile {
		t.Fatalf("link backup %s -> %q, %v; want -> %s", linkSaved, target, err, operatorFile)
	}
	for _, path := range []string{
		literal, backup, oldLink, filepath.Dir(oldLink), staleManaged, staleReload, reloadLink, accountReloadLink,
		operatorLink,
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("retired %s survives: %v", path, err)
		}
	}
	if got := readFixture(t, foreign); got != "user's own\n" {
		t.Fatalf("foreign %s = %q, want it untouched", foreign, got)
	}
	if got := readFixture(t, kept); got != "old\n" {
		t.Fatalf("unlisted %s = %q, want it untouched", kept, got)
	}
}

// TestRetirePredecessorsPlansALinkOnceThroughALinkedConfigDir: a dry run over a
// config dir reached through a symlink names the retired embedded link once,
// under the physical path, and the configFiles row for it adds nothing.
func TestRetirePredecessorsPlansALinkOnceThroughALinkedConfigDir(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	physical := filepath.Join(root, "real-claude")
	config := filepath.Join(home, ".claude")
	symlinkFixture(t, physical, config)
	managed := filepath.Join(home, "managed")
	staleReload := filepath.Join(managed, "reload.command.md")
	writeFixture(t, staleReload, "embedded")
	reloadLink := filepath.Join(physical, "commands", "reload.md")
	symlinkFixture(t, staleReload, reloadLink)
	var output bytes.Buffer
	installer := &engine{
		managedRoot: managed,
		options: Options{
			Home: home, ConfigDir: config, Stdout: &output,
			retired: &retired.Lists{ConfigFiles: []retired.ConfigFile{
				{Path: "commands/reload.md", Reason: "retired /reload command link; successor /pfm:reload"},
			}},
		},
		stamp: "20260101-000000",
	}
	if err := installer.retirePredecessors(); err != nil {
		t.Fatal(err)
	}
	want := "  change  retire " + reloadLink + " (the /reload command is /pfm:reload now)\n" +
		"  change  retire " + staleReload + " (embedded /reload command retired)\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	if _, err := os.Lstat(reloadLink); err != nil {
		t.Fatalf("dry run removed %s: %v", reloadLink, err)
	}
}
