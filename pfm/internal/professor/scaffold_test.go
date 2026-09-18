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
