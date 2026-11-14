package mockengine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeDoctorStartsItsConfig pins what Claude's self-doctor writes, as a real start does: the
// global config (in CLAUDE_CONFIG_DIR when set, else $HOME/.claude.json) with numStartups one higher,
// and, when a config was there, its old bytes backed up into {config dir}/backups.
func TestClaudeDoctorStartsItsConfig(t *testing.T) {
	for _, row := range []struct {
		name       string
		configDir  bool
		existing   string
		wantConfig string
	}{
		{name: "no config dir and no config: $HOME/.claude.json is created", wantConfig: `{"numStartups":1}`},
		{
			name: "no config dir with a config: backed up into ~/.claude/backups", existing: `{"numStartups":2,"x":1}`,
			wantConfig: `{"numStartups":3,"x":1}`,
		},
		{
			name: "a config dir with a config: both inside it", configDir: true, existing: `{"numStartups":4}`,
			wantConfig: `{"numStartups":5}`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			env := map[string]string{"CLAUDE_CONFIG_DIR": ""}
			config, backups := filepath.Join(fix.home, ".claude.json"), filepath.Join(fix.home, ".claude", "backups")
			if row.configDir {
				env["CLAUDE_CONFIG_DIR"] = fix.configDir
				config, backups = filepath.Join(fix.configDir, ".claude.json"), filepath.Join(fix.configDir, "backups")
			}
			if row.existing != "" {
				if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte(row.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			code := Main(context.Background(), "claude", []string{"doctor"}, strings.NewReader(""), &stdout, &stderr,
				fix.env(env))
			if code != 0 || stderr.String() != "" {
				t.Fatalf("claude doctor exit %d stderr %q, want 0 and none", code, stderr.String())
			}
			if got, err := os.ReadFile(config); err != nil || string(got) != row.wantConfig {
				t.Fatalf("%s = %q (%v), want %q", config, got, err, row.wantConfig)
			}
			entries, err := os.ReadDir(backups)
			if row.existing == "" {
				if !os.IsNotExist(err) {
					t.Fatalf(
						"%s exists (%v, %d entries) after a start with no config, want none",
						backups,
						err,
						len(entries),
					)
				}
				return
			}
			if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".claude.json.backup.") {
				t.Fatalf("%s holds %v (%v), want one .claude.json.backup.{ms}", backups, entries, err)
			}
			if saved, err := os.ReadFile(
				filepath.Join(backups, entries[0].Name()),
			); err != nil ||
				string(saved) != row.existing {
				t.Fatalf("backup = %q (%v), want the old config %q", saved, err, row.existing)
			}
		})
	}
}
