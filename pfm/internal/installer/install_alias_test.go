package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallFromAliasUsesPhysicalCloneForShellAndGlobalLinks(t *testing.T) {
	home, clone, alias := aliasInstallFixture(t)
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), SourceRepo: alias,
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	want := sourceLine(filepath.Join(physical, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh"))
	if got := readFixture(t, filepath.Join(home, ".zshrc")); !strings.Contains(got, want) {
		t.Fatalf("zshrc=%q, want %q", got, want)
	}
	for _, entry := range []struct{ target, source string }{
		{filepath.Join(home, ".claude", "commands", "fixture.md"), filepath.Join(physical, "templates", "global", "commands", "fixture.md")},
		{filepath.Join(home, ".claude", "skills", "fixture"), filepath.Join(physical, "templates", "global", "skills", "fixture")},
		{filepath.Join(home, ".claude", "agents", "fixture.md"), filepath.Join(physical, "templates", "global", "agents", "fixture.md")},
	} {
		assertLink(t, entry.target, entry.source)
	}
}

func TestInstallFromAliasAfterPhysicalCloneDoesNotRewriteShellOrGlobalLinks(t *testing.T) {
	home, clone, alias := aliasInstallFixture(t)
	options := Options{
		MCPConfigPath: testConfigPath(t), SourceRepo: clone,
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	options.SourceRepo, options.Stdout = alias, &output
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("alias reinstall: %v\n%s", err, output.String())
	}
	for _, path := range []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".claude", "commands", "fixture.md"),
		filepath.Join(home, ".claude", "skills", "fixture"),
		filepath.Join(home, ".claude", "agents", "fixture.md"),
	} {
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.Contains(line, "change") && strings.Contains(line, path) {
				t.Fatalf("alias reinstall changed %s:\n%s", path, output.String())
			}
		}
	}
	if backups, err := filepath.Glob(filepath.Join(home, ".zshrc.pre-professor-*")); err != nil || len(backups) != 0 {
		t.Fatalf("alias reinstall left zshrc backups: %v, %v", backups, err)
	}
}

func aliasInstallFixture(t *testing.T) (home, clone, alias string) {
	t.Helper()
	home, clone = t.TempDir(), t.TempDir()
	alias = filepath.Join(t.TempDir(), "clone-alias")
	if err := os.Symlink(clone, alias); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(clone, "templates", "global", "commands", "fixture.md"), "# command\n")
	writeFixture(t, filepath.Join(clone, "templates", "global", "skills", "fixture", "SKILL.md"), "# skill\n")
	writeFixture(t, filepath.Join(clone, "templates", "global", "agents", "fixture.md"),
		"---\nname: fixture\ndescription: Fixture agent.\n---\nbody\n")
	return home, clone, alias
}
