package installer

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRetiredGeneratedStores(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	generated := filepath.Join(home, ".local", "state", "pfm", "generated")
	want := []string{
		filepath.Join(generated, "claude-agents"),
		filepath.Join(generated, "codex-agents"),
	}
	if got := RetiredGeneratedStores(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("RetiredGeneratedStores(%q)=%v, want %v", home, got, want)
	}
}
