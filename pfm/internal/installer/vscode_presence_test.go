package installer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestVSCodeProductRootsNamesOnlyExistingDirectories pins what counts as a VS
// Code install: an existing product directory, never a file of that name, and
// VSCODE_PORTABLE only when it is an absolute existing directory.
func TestVSCodeProductRootsNamesOnlyExistingDirectories(t *testing.T) {
	home := t.TempDir()
	portable := filepath.Join(t.TempDir(), "portable-data")
	for _, dir := range []string{filepath.Join(home, ".vscode"), filepath.Join(home, ".vscode-server-insiders"), portable} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".vscode-server"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		portable string
		want     []string
	}{
		{
			name: "no portable",
			want: []string{filepath.Join(home, ".vscode"), filepath.Join(home, ".vscode-server-insiders")},
		},
		{
			name:     "relative portable ignored",
			portable: "portable-data",
			want:     []string{filepath.Join(home, ".vscode"), filepath.Join(home, ".vscode-server-insiders")},
		},
		{
			name: "absolute portable", portable: portable,
			want: []string{filepath.Join(home, ".vscode"), filepath.Join(home, ".vscode-server-insiders"), portable},
		},
		{
			name:     "absent portable",
			portable: filepath.Join(home, "gone"),
			want:     []string{filepath.Join(home, ".vscode"), filepath.Join(home, ".vscode-server-insiders")},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := &paths.MapEnv{}
			if testCase.portable != "" {
				env = &paths.MapEnv{Values: map[string]string{"VSCODE_PORTABLE": testCase.portable}}
			}
			if got := VSCodeProductRoots(home, env); !slices.Equal(got, testCase.want) {
				t.Errorf("VSCodeProductRoots = %v, want %v", got, testCase.want)
			}
		})
	}
}
