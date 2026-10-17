package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestFullscreenClearHonorsEveryMCPAlias(t *testing.T) {
	for _, kind := range []string{"home", "roster", "ambient", "config"} {
		t.Run(kind, func(t *testing.T) {
			account := fullscreenAccount(t, `{"tui":"fullscreen"}`, canaryRegistry)
			installer, _ := fullscreenEngine(t, true, account)
			installer.options.Env = &paths.MapEnv{}
			aliasDir := t.TempDir()
			switch kind {
			case "home":
				aliasDir = installer.options.Home
			case "roster":
				installer.options.RosterConfigDirs = []string{aliasDir}
			case "ambient":
				installer.options.Env = &paths.MapEnv{Values: map[string]string{"CLAUDE_CONFIG_DIR": aliasDir}}
			case "config":
				installer.options.ConfigDir = aliasDir
			}
			alias := filepath.Join(aliasDir, ".claude.json")
			if err := os.Symlink(filepath.Join(account.ConfigDir, ".claude.json"), alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(alias+".lock", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := installer.clearFullscreenAutoDisable(); err == nil {
				t.Fatal("fullscreen clear ignored native alias lock")
			}
			if got := readRegistry(t, account); got != canaryRegistry {
				t.Fatalf("locked registry overwritten: %s", got)
			}
		})
	}
}
