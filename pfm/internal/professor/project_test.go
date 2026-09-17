package professor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfessorDoctorProjectLine(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "templates", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "VERSION"), []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(store, "templates", "project", "current.md")
	if err := os.WriteFile(template, []byte("current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".professor"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"interview":{"blueprint_clone_path":"` + store + `"}}`)
	if err := os.WriteFile(filepath.Join(project, ".professor", "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "current.md"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := HashTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(project, Baseline{
		Version: BaselineVersion,
		Files: map[string]FilePin{
			"current.md": {Template: "project/current.md", TemplateHash: hash},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if warnings := PrintDoctor(&output, project, t.TempDir()); warnings != 0 ||
		!strings.Contains(output.String(), "professor: current 1 · review-required 0") {
		t.Fatalf("PrintDoctor() warnings=%d output=%q", warnings, output.String())
	}
	if err := os.WriteFile(BaselinePath(project), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if warnings := PrintDoctor(&output, project, t.TempDir()); warnings != 1 ||
		!strings.Contains(output.String(), "professor: UNREADABLE") {
		t.Fatalf("PrintDoctor() unreadable warnings=%d output=%q", warnings, output.String())
	}
}

func TestWriteProjectUnmanagedHumanAndJSON(t *testing.T) {
	var human bytes.Buffer
	writeProjectUnmanaged(&human, false)
	if got := human.String(); !strings.HasPrefix(got, "NOT-MANAGED — ") ||
		!strings.Contains(got, "pfm update check") || strings.Contains(got, "pfm init") {
		t.Fatalf("WriteProjectUnmanaged(human) = %q", got)
	}

	var jsonBuffer bytes.Buffer
	writeProjectUnmanaged(&jsonBuffer, true)
	var object map[string]any
	if err := json.Unmarshal(jsonBuffer.Bytes(), &object); err != nil {
		t.Fatalf("JSON output is not one object: %v", err)
	}
	terminal, ok := object["terminal"].(string)
	if !ok || !strings.HasPrefix(terminal, "NOT-MANAGED — ") {
		t.Fatalf("JSON terminal=%#v", object["terminal"])
	}
}
