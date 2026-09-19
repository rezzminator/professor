package professor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/obs"
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
		"templates/project/CLAUDE.md":         "# fixture contract\n",
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
		"commands", "agents", "scripts", "skills", "epics", "codex", "docs-commands", "docs-agents",
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

// TestScaffoldPersistsPinsForEntriesWrittenBeforeAFailure pins L3-F8: plan
// entries sort ".claude/settings.json" before ".rumdl.toml" (byte order),
// so the first entry is durably written and pinnable before the second
// entry's write fails (a pre-existing non-empty directory sits where
// atomicfile.Write needs to rename a regular file). Before the fix, Scaffold
// returned the error without ever calling Save — the first entry's pin was
// lost, and a retry would hit `CONFLICT … exists` on it and never pin it.
// After the fix, the partial baseline is durably saved so a retry (`pfm init
// --force`) converges to a fully pinned install.
func TestScaffoldPersistsPinsForEntriesWrittenBeforeAFailure(t *testing.T) {
	source := newScaffoldFixtureStore(t)
	target := t.TempDir()
	// Force a hard failure on ".rumdl.toml": a non-empty directory where
	// atomicfile.Write needs to rename a regular file over it.
	blocked := filepath.Join(target, ".rumdl.toml")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "occupied"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	count, err := Scaffold(source, target, true, &stdout)
	if err == nil {
		t.Fatalf(
			"Scaffold() err = nil, want the blocked-directory write to fail; count=%d stdout=%q",
			count,
			stdout.String(),
		)
	}

	baseline, loadErr := Load(target)
	if loadErr != nil {
		t.Fatalf(
			"a failed Scaffold pass orphaned the entries it already wrote — baseline was never persisted: %v (scaffold error: %v)",
			loadErr,
			err,
		)
	}
	pin, ok := baseline.Files[".claude/settings.json"]
	if !ok {
		t.Fatalf(
			"baseline.json does not pin .claude/settings.json, written before the failure: %#v (scaffold error: %v)",
			baseline.Files,
			err,
		)
	}
	if pin.Template != "project/settings.json" {
		t.Fatalf("pin for .claude/settings.json = %#v, want template project/settings.json", pin)
	}
	if _, ok := baseline.Files[".rumdl.toml"]; ok {
		t.Fatalf("baseline.json pins .rumdl.toml, which never wrote successfully: %#v", baseline.Files)
	}
}
