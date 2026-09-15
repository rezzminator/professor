package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/professor"
)

func TestInitDeploysMappedTemplatesAndPinsExactlyTheDeployedSet(t *testing.T) {
	source := newScaffoldStoreFixture(t)
	home := t.TempDir()
	if err := installer.WriteSourceRepoMarker(home, source); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	runtime := commandRuntime{Paths: paths.Values{Home: home}}
	var stdout, stderr bytes.Buffer
	if code := runInit([]string{target}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runInit() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	baseline, err := professor.Load(target)
	if err != nil {
		t.Fatalf("load deployed baseline: %v", err)
	}
	if got, want := len(baseline.Files), 11; got != want {
		t.Fatalf("pin count=%d, want %d: %#v", got, want, baseline.Files)
	}
	if _, ok := baseline.Files[".claude/agents/per-project/developer.md"]; ok {
		t.Fatal("per-project interview template was deployed and pinned")
	}
	if _, err := os.Stat(filepath.Join(target, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md must remain generated, stat error=%v", err)
	}
	for _, relative := range []string{
		"CLAUDE.md",
		".claude/settings.json",
		".rumdl.toml",
		".claude/commands/dev.md",
		".claude/agents/gitter.md",
		".claude/scripts/dev.sh",
		".claude/skills/legal/SKILL.md",
		"docs/epics/TEMPLATE.md",
		".codex/config.toml",
		"docs/commands/wave/references/fix-core.md",
		"docs/agents/_index.md",
	} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("deployed %s: %v", relative, err)
		}
	}
	// .rumdl.toml is neither a frontmatter .md nor a shebang .sh, so
	// addScaffoldMarker deliberately leaves it untouched — assert the
	// deployed bytes equal the template's exactly, the guard against a
	// future marker-placement change silently corrupting a TOML file.
	rumdlToml, err := os.ReadFile(filepath.Join(target, ".rumdl.toml"))
	if err != nil {
		t.Fatal(err)
	}
	wantRumdlToml, err := os.ReadFile(filepath.Join(source, "templates", "project", "rumdl-policy.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rumdlToml, wantRumdlToml) {
		t.Fatalf(".rumdl.toml deployed bytes=%q, want the template's bytes unmodified=%q", rumdlToml, wantRumdlToml)
	}
	commandRaw, err := os.ReadFile(filepath.Join(target, ".claude", "commands", "dev.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(commandRaw), "---\n# pfm-scaffold: project/commands/dev.md@") ||
		!strings.Contains(string(commandRaw), "{TOKEN}") {
		t.Fatalf("frontmatter marker/token placement=%q", commandRaw)
	}
	scriptPath := filepath.Join(target, ".claude", "scripts", "dev.sh")
	scriptRaw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(scriptRaw), "#!/usr/bin/env bash\n# pfm-scaffold: project/scripts/dev.sh@") {
		t.Fatalf("script marker placement=%q", scriptRaw)
	}
	if info, err := os.Stat(scriptPath); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("script executable mode=%v err=%v", info, err)
	}
	handoff := "open Claude here and follow " + filepath.Join(
		source,
		"docs",
		"SETUP.md",
	) + " § Install interview — it fills tokens and deploys per-project agents"
	if !strings.Contains(stdout.String(), "deployed 11 project files") || !strings.Contains(stdout.String(), handoff) {
		t.Fatalf("init output=%q", stdout.String())
	}
}

func TestInitCollisionSkipsWithoutPinAndForceOverwrites(t *testing.T) {
	source := newScaffoldStoreFixture(t)
	home := t.TempDir()
	if err := installer.WriteSourceRepoMarker(home, source); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	collision := filepath.Join(target, ".claude", "commands", "dev.md")
	if err := os.MkdirAll(filepath.Dir(collision), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collision, []byte("local truth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Paths: paths.Values{Home: home}}
	var stdout, stderr bytes.Buffer
	if code := runInit([]string{target}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runInit() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	baseline, err := professor.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, pinned := baseline.Files[".claude/commands/dev.md"]; pinned {
		t.Fatal("colliding local file was pinned")
	}
	if got, _ := os.ReadFile(collision); string(got) != "local truth\n" {
		t.Fatalf("collision content=%q", got)
	}
	if !strings.Contains(stdout.String(), "CONFLICT .claude/commands/dev.md: exists") {
		t.Fatalf("collision output=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runInit([]string{"--force", target}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runInit(--force) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	baseline, err = professor.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, pinned := baseline.Files[".claude/commands/dev.md"]; !pinned {
		t.Fatal("forced file was not pinned")
	}
	if got, _ := os.ReadFile(collision); !strings.Contains(string(got), "name: dev") {
		t.Fatalf("forced collision content=%q", got)
	}
}

func newScaffoldStoreFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"VERSION":                         {content: "0.65.0\n", mode: 0o600},
		"templates/project/CLAUDE.md":     {content: "# {TOKEN} contract\n", mode: 0o600},
		"templates/project/settings.json": {content: "{}\n", mode: 0o600},
		"templates/project/rumdl-policy.toml": {
			content: "# fixture rumdl policy\n[global]\n",
			mode:    0o600,
		},
		"templates/project/commands/dev.md": {
			content: "---\nname: dev\n---\n{TOKEN}\n",
			mode:    0o600,
		},
		"templates/project/agents/gitter.md": {
			content: "---\nname: gitter\n---\nbody\n",
			mode:    0o600,
		},
		"templates/project/agents/per-project/developer.md": {
			content: "---\nname: developer\n---\nbody\n",
			mode:    0o600,
		},
		"templates/project/scripts/dev.sh": {
			content: "#!/usr/bin/env bash\nset -euo pipefail\n",
			mode:    0o755,
		},
		"templates/project/skills/legal/SKILL.md": {
			content: "---\nname: legal\n---\nbody\n",
			mode:    0o600,
		},
		"templates/project/epics/TEMPLATE.md":                         {content: "# Epic\n", mode: 0o600},
		"templates/project/codex/config.toml":                         {content: "model = \"{TOKEN}\"\n", mode: 0o600},
		"templates/project/docs-commands/wave/references/fix-core.md": {content: "# Fix Core\n", mode: 0o600},
		"templates/project/docs-agents/_index.md":                     {content: "# Agents\n", mode: 0o600},
	}
	for relative, fixture := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fixture.content), fixture.mode); err != nil {
			t.Fatal(err)
		}
	}
	gitTemp(t, root, "init", "-q")
	gitTemp(t, root, "config", "user.email", "fixture.invalid")
	gitTemp(t, root, "config", "user.name", "fixture-identity")
	gitTemp(t, root, "add", ".")
	gitTemp(t, root, "commit", "-qm", "fixture store")
	return root
}
