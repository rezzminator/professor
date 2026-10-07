package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// stageGlobalSource writes one recorded clone's machine-global sources: two
// commands (a file and a directory) and one template skill — exactly the
// three shapes wireGlobalCommands and wireGlobalSkills link in the store.
func stageGlobalSource(t *testing.T, repo string) {
	t.Helper()
	writeFixture(t, filepath.Join(repo, "templates", "global", "commands", "tokens.md"), "# tokens command\n")
	writeFixture(t, filepath.Join(repo, "templates", "global", "commands", "tools", "go.md"), "# go command\n")
	writeFixture(t, filepath.Join(repo, "templates", "global", "skills", "pcm", "SKILL.md"), "# pcm skill\n")
}

// TestUninstallRemovesEveryMachineGlobalCommandAndSkillLink checks store teardown, including a dead retired skill.
func TestUninstallRemovesEveryMachineGlobalCommandAndSkillLink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := filepath.Join(home, ".professor")
	stageGlobalSource(t, repo)
	accounts := []string{ClaudeStore(home)}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, config := range accounts {
		assertLink(t,
			filepath.Join(config, "commands", "tokens.md"),
			filepath.Join(repo, "templates", "global", "commands", "tokens.md"))
		assertLink(t,
			filepath.Join(config, "commands", "tools"),
			filepath.Join(repo, "templates", "global", "commands", "tools"))
		assertLink(t,
			filepath.Join(config, "skills", "pcm"),
			filepath.Join(repo, "templates", "global", "skills", "pcm"))
		if err := os.Symlink(
			filepath.Join(repo, "workflows", "deep-rr"),
			filepath.Join(config, "skills", "deep-rr"),
		); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeUninstall, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, config := range accounts {
		for _, relative := range []string{
			filepath.Join("commands", "tokens.md"),
			filepath.Join("commands", "tools"),
			filepath.Join("skills", "pcm"),
			filepath.Join("skills", "deep-rr"),
		} {
			path := filepath.Join(config, relative)
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("uninstall left the machine-global link %s: %v", path, err)
			}
		}
	}
}

// TestUninstallRemovesGlobalAgentLinks checks store agent links are unwired.
func TestUninstallRemovesGlobalAgentLinks(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := filepath.Join(home, ".professor")
	agentsSource := filepath.Join(repo, "templates", "global", "agents")
	for _, name := range []string{"collector", "tracer"} {
		body := "---\nname: " + name + "\ndescription: " + name + " role.\n---\n\nbody\n"
		writeFixture(t, filepath.Join(agentsSource, name+".md"), body)
	}
	accounts := []string{ClaudeStore(home)}
	codexHomes := []string{filepath.Join(home, ".codex")}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: codexHomes,
	}); err != nil {
		t.Fatal(err)
	}
	for _, config := range accounts {
		for _, name := range []string{"collector", "tracer"} {
			assertLink(t,
				filepath.Join(config, "agents", name+".md"),
				filepath.Join(repo, "templates", "global", "agents", name+".md"))
		}
	}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeUninstall, Home: home, Runner: &fakeRunner{}, CodexHomes: codexHomes,
	}); err != nil {
		t.Fatal(err)
	}
	for _, config := range accounts {
		for _, name := range []string{"collector", "tracer"} {
			path := filepath.Join(config, "agents", name+".md")
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("uninstall left the machine-global agent link %s: %v", path, err)
			}
		}
	}
}

// TestUninstallKeepsAndNamesAForeignGlobalLink pins the boundary of the
// ownership-by-target rule the sibling unwireGeneratedCodexAgents holds to: a
// registry entry that is not a link into the recorded clone's
// templates/global tree is never this installer's to remove — not a regular
// file, and not a symlink of a SHIPPED name that resolves somewhere else.
// The foreign same-named link is also NAMED in the transcript, so a kept
// entry is a reported decision rather than a silent omission.
func TestUninstallKeepsAndNamesAForeignGlobalLink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := filepath.Join(home, ".professor")
	stageGlobalSource(t, repo)
	config := filepath.Join(home, ".claude")

	operator := filepath.Join(home, "elsewhere", "tokens.md")
	writeFixture(t, operator, "# my own tokens command\n")
	foreign := filepath.Join(config, "commands", "tokens.md")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(operator, foreign); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(config, "skills", "mine", "SKILL.md")
	writeFixture(t, regular, "# operator skill\n")

	var transcript bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeUninstall, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{}, Stdout: &transcript,
	}); err != nil {
		t.Fatal(err)
	}
	target, linked := resolvedLink(foreign)
	if !linked || target != filepath.Clean(operator) {
		t.Fatalf("uninstall removed a foreign global command link: target=%q linked=%v", target, linked)
	}
	if got := readFixture(t, regular); got != "# operator skill\n" {
		t.Fatalf("uninstall touched an operator-owned skill file: %q", got)
	}
	if !strings.Contains(transcript.String(), foreign) {
		t.Fatalf("uninstall kept the foreign link %s without naming it:\n%s", foreign, transcript.String())
	}
}

// TestUninstallRemovesGlobalAgentVariantLinksAndTheirGeneratedDirectory: a
// variant's link resolves into the pfm-owned generated directory, not the
// clone, so the ownership-by-target rule has to know that directory too —
// otherwise uninstall leaves a working super-* agent in the store.
func TestUninstallRemovesGlobalAgentVariantLinksAndTheirGeneratedDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	agentsSource := filepath.Join(home, ".professor", "templates", "global", "agents")
	writeFixture(t, filepath.Join(agentsSource, "lead.md"),
		"---\nname: lead\ndescription: lead role.\neffort: low\n---\n\nbody\n")
	writeFixture(t, filepath.Join(agentsSource, "variants.json"), `{"super-lead":{"from":"lead","effort":"medium"}}`)
	accounts := []string{ClaudeStore(home)}
	codexHomes := []string{filepath.Join(home, ".codex")}
	generated := paths.GeneratedClaudeAgentsDir(home)

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: codexHomes,
	}); err != nil {
		t.Fatal(err)
	}
	for _, config := range accounts {
		assertLink(t, filepath.Join(config, "agents", "super-lead.md"), filepath.Join(generated, "super-lead.md"))
	}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeUninstall, Home: home, Runner: &fakeRunner{}, CodexHomes: codexHomes,
	}); err != nil {
		t.Fatal(err)
	}
	for _, config := range accounts {
		path := filepath.Join(config, "agents", "super-lead.md")
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall left the variant agent link %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(generated); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the generated Claude agents directory %s: %v", generated, err)
	}
}
