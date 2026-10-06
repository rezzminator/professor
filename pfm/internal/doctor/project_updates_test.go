package doctor

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/professor"
)

// TestDoctorProjectUpdatesFlagRunsOnlyTheProjectReport pins 1-a: with
// --project-updates, doctor runs only the project-template report — no other
// health line, no `doctor: failures=`/`doctor: warnings=` summary — and maps
// its exit through the doctor ladder (3 here: no baseline at cwd or above).
func TestDoctorProjectUpdatesFlagRunsOnlyTheProjectReport(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--project-updates"}, &stdout, &stderr, runtime)
	if code != 3 {
		t.Fatalf("doctor --project-updates code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "doctor:") {
		t.Fatalf("doctor --project-updates ran other checks:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAILED — ") {
		t.Fatalf("doctor --project-updates stdout=%q, want the missing-baseline terminal", stdout.String())
	}
}

// TestDoctorProjectUpdatesUsageRejectsMixedFlags pins the usage door: --root
// or --json without --project-updates, --project-updates with --verbose or
// --skip-harvest, and any positional, all exit 2.
func TestDoctorProjectUpdatesUsageRejectsMixedFlags(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	for _, args := range [][]string{
		{"--root", "."},
		{"--json"},
		{"--project-updates", "--verbose"},
		{"--project-updates", "--skip-harvest"},
		{"--project-updates", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runDoctor(args, &stdout, &stderr, runtime); code != 2 {
			t.Fatalf("doctor %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		want := "usage: pfm doctor [--verbose] [--skip-harvest] (exit 0 clean, 1 warnings, 3 failures) | " +
			"pfm doctor --project-updates [--root DIR] [--json] (exit 0 clean, 1 review required, 3 report failure); 2 usage error\n"
		if stderr.String() != want {
			t.Fatalf("doctor %v usage=%q, want %q", args, stderr.String(), want)
		}
	}
}

// TestDoctorProjectUpdatesReportsRetiredNamesInUnpinnedFiles is the issue's
// adopter case end to end: a retired name in an unpinned script and an
// unpinned codex-build.json is reported `path:line` with its successor and
// the run asks for review (exit 1); a line of live names sharing a retired
// prefix is no hit.
func TestDoctorProjectUpdatesReportsRetiredNamesInUnpinnedFiles(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	store := t.TempDir()
	for relative, content := range map[string]string{
		"VERSION":                             "0.1.0\n",
		"templates/project/CLAUDE.md":         "# fixture contract\n",
		"templates/project/settings.json":     "{}\n",
		"templates/project/rumdl-policy.toml": "[global]\n",
	} {
		writeProjectUpdatesFixture(t, store, relative, content)
	}
	for _, dir := range []string{
		"commands", "agents", "scripts", "skills", "epics", "codex", "docs-commands", "docs-agents",
	} {
		if err := os.MkdirAll(filepath.Join(store, "templates", "project", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	project := t.TempDir()
	if _, err := professor.Scaffold(store, project, false, io.Discard); err != nil {
		t.Fatalf("Scaffold() err = %v", err)
	}
	manifest, err := json.Marshal(map[string]any{"interview": map[string]string{"blueprint_clone_path": store}})
	if err != nil {
		t.Fatal(err)
	}
	writeProjectUpdatesFixture(t, project, ".professor/manifest.json", string(manifest))
	writeProjectUpdatesFixture(t, project, "scripts/legacy.sh", "#!/bin/sh\nclaude -p '/wave:refine the spec'\n")
	writeProjectUpdatesFixture(
		t, project, ".claude/codex-build.json", "{\n  \"skills\": [],\n  \"tools\": [\"mcp__chat__chat_ls\"]\n}\n",
	)
	writeProjectUpdatesFixture(t, project, "scripts/live.sh", "/pfm:release prepare; /pfm:workbench; /pcm\n")

	var stdout, stderr bytes.Buffer
	code := runDoctor([]string{"--project-updates", "--root", project}, &stdout, &stderr, runtime)
	if code != 1 {
		t.Fatalf(
			"doctor --project-updates code=%d, want 1 (review)\nstdout:\n%s\nstderr:\n%s",
			code, stdout.String(), stderr.String(),
		)
	}
	for _, want := range []string{
		"  RETIRED-NAME  2\n",
		"    scripts/legacy.sh:2   /wave — retired command, now /flights:spec\n",
		"    .claude/codex-build.json:3   mcp__chat__ — retired tool, now mcp__professor__chat_*\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor --project-updates missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "scripts/live.sh") {
		t.Fatalf("a live name sharing a retired prefix was reported:\n%s", stdout.String())
	}
}

func writeProjectUpdatesFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
