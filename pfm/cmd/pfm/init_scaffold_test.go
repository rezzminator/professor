package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
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
	if _, ok := baseline.Files[".claude/commands/per-project/testing-manual.md"]; ok {
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
		"docs/commands/git/references/gitter-history.md",
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
	) + " § Install interview — it fills tokens and deploys the per-project files"
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

func TestInitRefusesASecondScaffoldWithoutForce(t *testing.T) {
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
		t.Fatalf("load first baseline: %v", err)
	}
	pins := len(baseline.Files)
	if pins == 0 {
		t.Fatalf("first init pinned nothing: %#v", baseline.Files)
	}

	stdout.Reset()
	stderr.Reset()
	code := runInit([]string{target}, &stdout, &stderr, runtime)
	if code != 2 {
		t.Fatalf("second runInit() code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "is already scaffolded") ||
		!strings.Contains(stderr.String(), "pfm init --force") {
		t.Fatalf("refusal stderr=%q", stderr.String())
	}
	baseline, err = professor.Load(target)
	if err != nil {
		t.Fatalf("load baseline after refused second init: %v", err)
	}
	if got := len(baseline.Files); got != pins {
		t.Fatalf("pin count after refused second init=%d, want %d: %#v", got, pins, baseline.Files)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runInit([]string{"--force", target}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("runInit(--force) code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	baseline, err = professor.Load(target)
	if err != nil {
		t.Fatalf("load baseline after forced re-init: %v", err)
	}
	if got := len(baseline.Files); got == 0 {
		t.Fatalf("forced re-init did not re-pin: %#v", baseline.Files)
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
		"templates/project/commands/per-project/testing-manual.md": {
			content: "---\nname: testing-manual\n---\nbody\n",
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
		"templates/project/epics/TEMPLATE.md": {content: "# Epic\n", mode: 0o600},
		"templates/project/codex/config.toml": {
			content: "model = \"{TOKEN}\"\n",
			mode:    0o600,
		},
		"templates/project/docs-commands/git/references/gitter-history.md": {
			content: "# Gitter History\n",
			mode:    0o600,
		},
		"templates/project/docs-agents/_index.md": {content: "# Agents\n", mode: 0o600},
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

// TestInitRefusalNamesADeterministicPinDate pins F6: the refusal used to
// range Baseline.Files — a map — for "the" date, so Go's randomized iteration
// order made the reported date an arbitrary entry's as soon as two pins
// disagreed, and a baseline that pinned NOTHING still claimed a date it never
// had. The refusal now names the newest stamp, says so when the pins span
// several dates, and gives the empty baseline its own message.
func TestInitRefusalNamesADeterministicPinDate(t *testing.T) {
	spread := professor.Baseline{
		Version: professor.BaselineVersion,
		Files: map[string]professor.FilePin{
			"a.md": {PinnedAt: "2026-01-02"},
			"b.md": {PinnedAt: "2026-03-04"},
			"c.md": {PinnedAt: "2026-02-03"},
		},
	}
	// Twenty refusals over the same baseline: one arbitrary-entry read is
	// enough to make this flake, which is the defect stated as a test.
	for range 20 {
		target := writeBaselineFixture(t, spread)
		var stderr bytes.Buffer
		if code, refused := refuseRescaffold(false, target, &stderr); code != 2 || !refused {
			t.Fatalf("refuseRescaffold() = (%d, %t), want (2, true)", code, refused)
		}
		got := stderr.String()
		if !strings.Contains(got, "2026-03-04") || !strings.Contains(got, "pins span several dates") {
			t.Fatalf("refusal over pins stamped on three dates=%q, want the newest (2026-03-04) named as a spread", got)
		}
		if strings.Contains(got, "2026-01-02") || strings.Contains(got, "2026-02-03") {
			t.Fatalf("refusal named a pin date that is not the newest: %q", got)
		}
	}

	target := writeBaselineFixture(t, professor.Baseline{Version: professor.BaselineVersion})
	var stderr bytes.Buffer
	if code, refused := refuseRescaffold(false, target, &stderr); code != 2 || !refused {
		t.Fatalf("refuseRescaffold() over an empty baseline = (%d, %t), want (2, true)", code, refused)
	}
	if got := stderr.String(); !strings.Contains(got, "pinning no files at all") ||
		strings.Contains(got, "pinned by pfm init on") {
		t.Fatalf("refusal over a baseline that pins nothing=%q, want its own message and no date claim", got)
	}
}

// writeBaselineFixture drops one baseline.json into a fresh directory and
// returns that directory.
func writeBaselineFixture(t *testing.T, baseline professor.Baseline) string {
	t.Helper()
	target := t.TempDir()
	path := professor.BaselinePath(target)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return target
}
