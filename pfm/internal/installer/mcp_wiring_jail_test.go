package installer

// A real child of the test binary shows install never writes through an inherited CLAUDE_CONFIG_DIR.

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestJailPinsClaudeConfigDir(t *testing.T) {
	t.Parallel()
	const (
		childEnv    = "PFM_TEST_INSTALLER_JAIL_CHILD"
		sentinelEnv = "PFM_TEST_INSTALLER_JAIL_SENTINEL"
	)
	if os.Getenv(childEnv) == "1" {
		sentinel := os.Getenv(sentinelEnv)
		home := t.TempDir()
		canonical := filepath.Join(home, ".claude")
		writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
		configPath := filepath.Join(home, "pfm.config.json")
		writeFixture(t, configPath, `{"version":2}`)
		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, ConfigDir: canonical,
			SourceRepo: t.TempDir(), MCPConfigPath: configPath,
			MCPEnabled: map[string]bool{"chat": true},
			MCPPort:    8377, Runner: &fakeRunner{}, Stdout: io.Discard,
		}); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(sentinel)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("ModeApply wrote through inherited CLAUDE_CONFIG_DIR %s: %v", sentinel, entries)
		}
		return
	}

	sentinel := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestJailPinsClaudeConfigDir$", "-test.v")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") &&
			!strings.HasPrefix(entry, childEnv+"=") &&
			!strings.HasPrefix(entry, sentinelEnv+"=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env,
		"CLAUDE_CONFIG_DIR="+sentinel,
		childEnv+"=1",
		sentinelEnv+"="+sentinel,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer test process escaped its CLAUDE_CONFIG_DIR jail: %v\n%s", err, output)
	}
}
