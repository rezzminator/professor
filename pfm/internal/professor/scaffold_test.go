package professor

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// newScaffoldFixtureStore lays down the smallest self-hosted blueprint clone
// Scaffold's InspectStore/planInitCopies accept: a VERSION file and every
// initTemplatePaths source present, three of them real files so Scaffold has
// something to deploy. No .git directory — storeSHAWithRunner then reports
// UnknownSelfHostedSHA without shelling out.
func newScaffoldFixtureStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"VERSION":                             "0.1.0\n",
		"templates/project/settings.json":     "{}\n",
		"templates/project/rumdl-policy.toml": "[global]\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{
		"commands", "agents", "scripts", "skills", "epics", "codex", "docs-commands", "docs-hub",
	} {
		if err := os.MkdirAll(filepath.Join(root, "templates", "project", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestScaffoldRecordsATransition: Scaffold walks the state door (spec §
// Middleware, `state`) — requested to scaffolded on success, comp=state,
// kind=professor — never the source/target paths it deployed. Scaffold's
// own trail runs over context.Background(), so obs.Test's process-scope
// installation is what a ctx-scoped recorder cannot see directly; the
// pattern (and why it still works) is TestRunProjectUpdateRecordsATransition
// in update_dispatch_test.go.
func TestScaffoldRecordsATransition(t *testing.T) {
	_, recorder := obs.Test(t)
	source := newScaffoldFixtureStore(t)
	target := t.TempDir()
	var stdout bytes.Buffer
	count, err := Scaffold(source, target, false, &stdout)
	if err != nil {
		t.Fatalf("Scaffold() err = %v, stdout=%q", err, stdout.String())
	}
	if count == 0 {
		t.Fatalf("Scaffold() deployed nothing: stdout=%q", stdout.String())
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "professor" {
			continue
		}
		if next, _ := record.Field("next"); next == "scaffolded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Scaffold() wrote no professor->scaffolded transition: %s", recorder.Raw())
	}
}

// TestScaffoldPersistsPinsForEntriesWrittenBeforeAFailure: one entry that
// cannot be deployed (a non-empty directory sits where .rumdl.toml goes) or
// read (an unreadable command template) is named SKIPPED and left unpinned;
// every other entry, before or after it, still deploys and pins, and the
// error counts both sides.
func TestScaffoldPersistsPinsForEntriesWrittenBeforeAFailure(t *testing.T) {
	source := newScaffoldFixtureStore(t)
	locked := filepath.Join(source, "templates", "project", "commands", "locked.md")
	if err := os.WriteFile(locked, []byte("# locked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "templates", "project", "agents", "after.md"),
		[]byte("# after\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	blocked := filepath.Join(target, ".rumdl.toml")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	count, err := Scaffold(source, target, true, &stdout)
	wantErr := "2 template(s) not deployed (SKIPPED above); the other 2 are deployed and pinned"
	if err == nil || err.Error() != wantErr || count != 2 {
		t.Fatalf("Scaffold() count=%d err=%v, want 2 and %q; stdout=%q", count, err, wantErr, stdout.String())
	}
	wantRead := "SKIPPED read template project/commands/locked.md: read " + locked + ": permission denied\n"
	if !strings.Contains(stdout.String(), wantRead) ||
		!strings.Contains(stdout.String(), "SKIPPED deploy project/rumdl-policy.toml to .rumdl.toml: ") {
		t.Fatalf("stdout = %q, want both SKIPPED lines (%q)", stdout.String(), wantRead)
	}
	baseline, loadErr := Load(target)
	if loadErr != nil {
		t.Fatalf("baseline was never persisted: %v (scaffold error: %v)", loadErr, err)
	}
	var pinned []string
	for local, pin := range baseline.Files {
		pinned = append(pinned, local+"="+pin.Template)
	}
	sort.Strings(pinned)
	want := []string{".claude/agents/after.md=project/agents/after.md", ".claude/settings.json=project/settings.json"}
	if !reflect.DeepEqual(pinned, want) {
		t.Fatalf("pins = %v, want %v", pinned, want)
	}
}

// TestScaffoldRefusesTwoTemplatesForOneLocalPath: project/docs-hub deploys
// into docs/, the parent of the docs/epics and docs/commands targets, so a
// blueprint shipping docs-hub/epics/TEMPLATE.md beside epics/TEMPLATE.md maps
// two templates onto one local path. Scaffold refuses the store before it
// writes anything, naming the path and both templates, rather than letting
// one silently overwrite the other and its pin.
func TestScaffoldRefusesTwoTemplatesForOneLocalPath(t *testing.T) {
	source := newScaffoldFixtureStore(t)
	for _, relative := range []string{"epics/TEMPLATE.md", "docs-hub/epics/TEMPLATE.md"} {
		path := filepath.Join(source, "templates", "project", filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# "+relative+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	target := t.TempDir()
	var stdout bytes.Buffer
	count, err := Scaffold(source, target, false, &stdout)
	if err == nil {
		t.Fatalf(
			"Scaffold() err = nil, want a refusal of two templates mapped to docs/epics/TEMPLATE.md; count=%d",
			count,
		)
	}
	for _, want := range []string{"docs/epics/TEMPLATE.md", "project/epics/TEMPLATE.md", "project/docs-hub/epics/TEMPLATE.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Scaffold() err = %q, want it to name %s", err, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(target, ".claude", "settings.json")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("a refused store still deployed .claude/settings.json (stat err %v)", statErr)
	}
}
