package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPhysicalPath(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, input, want string }{
		{"existing through symlink", filepath.Join(link, "settings.json"), filepath.Join(actual, "settings.json")},
		{"missing tail through symlink", filepath.Join(link, "missing", "settings.json"), filepath.Join(actual, "missing", "settings.json")},
		{"plain cleaned", filepath.Join(actual, ".", "settings.json"), filepath.Join(actual, "settings.json")},
		{"non-not-exist error", filepath.Join(loop, "settings.json"), filepath.Join(loop, "settings.json")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := PhysicalPath(test.input); got != test.want {
				t.Fatalf("PhysicalPath(%q)=%q, want %q", test.input, got, test.want)
			}
		})
	}
}
