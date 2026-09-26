package installer

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestApplyRetiresInstalledBBCardsAndHook(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	commandTarget := filepath.Join(config, "commands", "bb.md")
	skillTarget := filepath.Join(home, ".agents", "skills", "bb")
	writeFixture(t, filepath.Join(managed, "bb.command.md"), "old managed command\n")
	writeFixture(t, filepath.Join(managed, "codex-skills", "bb", "SKILL.md"), "old managed skill\n")
	writeFixture(t, filepath.Join(managed, "codex-skills", "bb", "agents", "openai.yaml"), "old managed metadata\n")
	if err := os.MkdirAll(filepath.Dir(commandTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managed, "bb.command.md"), commandTarget); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, commandTarget+".pre-professor-20300102-030405", "operator command\n")
	if err := os.MkdirAll(filepath.Dir(skillTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managed, "codex-skills", "bb"), skillTarget); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(config, "settings.json"), `{
  "hooks": {
    "UserPromptSubmit": [
      {"matcher":"","hooks":[
        {"type":"command","command":"`+home+`/.local/bin/pfm chat bb"},
        {"type":"command","command":"fixture-keep"}
      ]}
    ]
  }
}`)

	now := func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Now: now, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if content := readFixture(t, commandTarget); content != "operator command\n" {
		t.Fatalf("retirement did not restore operator card: %q", content)
	}
	for _, retired := range []string{
		skillTarget,
		filepath.Join(managed, "bb.command.md"),
		filepath.Join(managed, "codex-skills"),
	} {
		if _, err := os.Lstat(retired); !os.IsNotExist(err) {
			t.Fatalf("retired /bb surface remains at %s: %v", retired, err)
		}
	}
	settings := readFixture(t, filepath.Join(config, "settings.json"))
	if !strings.Contains(settings, "chat bb") || !strings.Contains(settings, "fixture-keep") {
		t.Fatalf("install changed account settings: %s", settings)
	}

	var second bytes.Buffer
	report, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Now: now, Stdout: &second, Runner: &fakeRunner{},
	})
	if err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", report, err, second.String())
	}
}

func TestApplyLeavesUnrelatedBBSymlinkAlone(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".claude", "commands", "bb.md")
	operatorSource := filepath.Join(home, "operator", "bb.md")
	writeFixture(t, operatorSource, "operator command\n")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(operatorSource, target); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".claude", "settings.json"), `{}`)

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t, target, operatorSource)
}

func TestApplyRetiresDanglingBBLinksFromTheRecordedProfessorClone(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "professor-clone")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	commandTarget := filepath.Join(home, ".claude", "commands", "bb.md")
	skillTarget := filepath.Join(home, ".agents", "skills", "bb")
	for _, target := range []string{commandTarget, skillTarget} {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	legacyRoot := filepath.Join(repo, "blueprint", "templates", "host-swap")
	if err := os.Symlink(filepath.Join(legacyRoot, "bb.command.md"), commandTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacyRoot, "codex-skills", "bb"), skillTarget); err != nil {
		t.Fatal(err)
	}

	installer := &engine{
		options: Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          ModeApply,
			Home:          home,
			ConfigDir:     filepath.Join(home, ".claude"),
			Stdout:        io.Discard,
		},
		apply:       true,
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.retireBBInstall(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{commandTarget, skillTarget} {
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("recorded legacy /bb link remains at %s: %v", target, err)
		}
	}
}
