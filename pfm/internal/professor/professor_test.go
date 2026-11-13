package professor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestStubStoreRunnerForTestSwapsAndRestoresTheStoreGit(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"templates", ".git"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &deps.FakeRunner{}
	fake.ScriptLookPath("git", "scripted-git", nil)
	fake.Script(
		[]string{"scripted-git", "rev-parse", "--short", "HEAD"},
		deps.RunResult{Stdout: []byte("stubbed\n")},
		nil,
	)
	previous := storeRunner
	restore := StubStoreRunnerForTest(fake)
	store, err := InspectStore(root)
	restore()
	if err != nil || store.SHA != "stubbed" {
		t.Fatalf("InspectStore() = %+v, %v; want SHA stubbed", store, err)
	}
	if storeRunner != previous {
		t.Fatalf("storeRunner after restore = %#v, want %#v", storeRunner, previous)
	}
}
