package installer

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// fixtureRetiredCommands are the retired commands the retirement is tested
// on; the shipped lists are empty.
var fixtureRetiredCommands = retired.Lists{Commands: retired.Commands{
	HomeBin:    []string{"cc-fleet", "cc-ls", "cc-open"},
	AccountBin: []string{"cc-launch.sh", "cc-open.sh", "cc-fleet.zsh"},
}}

func TestApplyRetiresLegacyCCCommandsAcrossConfiguredAccounts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configDirs := []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".cc", "4"),
		filepath.Join(home, ".cc", "7"),
	}
	var retiredPaths []string
	for _, name := range fixtureRetiredCommands.Commands.HomeBin {
		path := filepath.Join(home, ".local", "bin", name)
		writeFixture(t, path, "legacy command\n")
		retiredPaths = append(retiredPaths, path)
	}
	for _, configDir := range configDirs {
		for _, name := range fixtureRetiredCommands.Commands.AccountBin {
			path := filepath.Join(configDir, "bin", name)
			writeFixture(t, path, "legacy script\n")
			retiredPaths = append(retiredPaths, path)
		}
	}
	operatorCommand := filepath.Join(configDirs[1], "bin", "cc-operator-owned.sh")
	writeFixture(t, operatorCommand, "keep\n")
	accountData := filepath.Join(configDirs[1], "projects", "transcript.jsonl")
	writeFixture(t, accountData, "keep\n")

	if _, err := Run(context.Background(), Options{
		MCPConfigPath:  testConfigPath(t),
		Mode:           ModeApply,
		Home:           home,
		ClaudeAccounts: []pfmconfig.Account{{ID: 4, ConfigDir: configDirs[1]}, {ID: 7, ConfigDir: configDirs[2]}},
		Runner:         &fakeRunner{},
		retired:        &fixtureRetiredCommands,
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range retiredPaths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("retired command remains at %s: %v", path, err)
		}
	}
	for _, path := range []string{operatorCommand, accountData} {
		if got := readFixture(t, path); got != "keep\n" {
			t.Errorf("installer touched non-retired account file %s: %q", path, got)
		}
	}
}

func TestApplyQuarantinesExactNamedOperatorFilesBeforeRetirement(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	public := filepath.Join(home, ".local", "bin", "cc-open")
	account := filepath.Join(config, "bin", "cc-open.sh")
	writeFixture(t, public, "operator public bytes\n")
	writeFixture(t, account, "operator account bytes\n")
	if err := os.Chmod(public, 0o751); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(account, 0o640); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(home, "operator", "real-cc-ls")
	writeFixture(t, target, "symlink target survives\n")
	link := filepath.Join(home, ".local", "bin", "cc-ls")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath:  testConfigPath(t),
		Mode:           ModeApply,
		Home:           home,
		ClaudeAccounts: []pfmconfig.Account{{ID: 4, ConfigDir: config}},
		Runner:         &fakeRunner{},
		retired:        &fixtureRetiredCommands,
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{public, account, link} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("retired collision remains at %s: %v", path, err)
		}
	}
	if got := readFixture(t, target); got != "symlink target survives\n" {
		t.Fatalf("retiring symlink touched its target: %q", got)
	}

	root := filepath.Join(home, ".local", "state", "pfm", "retired-commands")
	for content, mode := range map[string]fs.FileMode{
		"operator public bytes\n":  0o751,
		"operator account bytes\n": 0o640,
	} {
		path := findRetiredFixtureByContent(t, root, content)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Errorf("quarantined %s mode = %o, want %o", path, got, mode)
		}
	}
}

func findRetiredFixtureByContent(t *testing.T, root, want string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || found != "" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(content) == want {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan retired-command quarantine %s: %v", root, err)
	}
	if strings.TrimSpace(found) == "" {
		t.Fatalf("retired-command quarantine %s has no file containing %q", root, want)
	}
	return found
}
