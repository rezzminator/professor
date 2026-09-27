package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestApplyRetiresTheWorkflowsDeepRRSkillLink is the regression for hosts
// installed while the fan-out still linked <clone>/workflows/deep-rr into
// every account's skills registry as deep-rr: once that tree is retired, the
// next install must remove the leftover link — live or dangling — from every
// configured account, while a regular deep-rr directory and a deep-rr link
// resolving outside the clone are an operator's own and stay untouched.
func TestApplyRetiresTheWorkflowsDeepRRSkillLink(t *testing.T) {
	for _, tc := range []struct {
		name string
		live bool
	}{
		{name: "dangling link", live: false},
		{name: "live link", live: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			repo := filepath.Join(home, ".professor")
			legacy := filepath.Join(repo, "workflows", "deep-rr")
			if tc.live {
				writeFixture(t, filepath.Join(legacy, "SKILL.md"), "# deep-rr skill\n")
			}
			linked := []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "2")}
			regularConfig := filepath.Join(home, ".cc", "3")
			foreignConfig := filepath.Join(home, ".cc", "4")
			for _, config := range linked {
				link := filepath.Join(config, "skills", "deep-rr")
				if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(legacy, link); err != nil {
					t.Fatal(err)
				}
			}
			regular := filepath.Join(regularConfig, "skills", "deep-rr", "SKILL.md")
			writeFixture(t, regular, "# operator deep-rr\n")
			operator := filepath.Join(home, "elsewhere", "deep-rr")
			writeFixture(t, filepath.Join(operator, "SKILL.md"), "# operator deep-rr\n")
			foreign := filepath.Join(foreignConfig, "skills", "deep-rr")
			if err := os.MkdirAll(filepath.Dir(foreign), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(operator, foreign); err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer
			if _, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{}, CodexHomes: []string{},
				ConfigDirs: append(append([]string{}, linked...), regularConfig, foreignConfig),
			}); err != nil {
				t.Fatalf("apply: %v\n%s", err, output.String())
			}
			for _, config := range linked {
				link := filepath.Join(config, "skills", "deep-rr")
				if _, err := os.Lstat(link); !os.IsNotExist(err) {
					t.Fatalf(
						"apply left the retired workflows/deep-rr skill link %s: %v\n%s",
						link,
						err,
						output.String(),
					)
				}
			}
			if got := readFixture(t, regular); got != "# operator deep-rr\n" {
				t.Fatalf("apply touched an operator-owned deep-rr skill directory: %q", got)
			}
			assertLink(t, foreign, operator)
		})
	}
}
