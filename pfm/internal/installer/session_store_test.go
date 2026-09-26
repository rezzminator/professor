package installer

import (
	"reflect"
	"testing"
)

// TestSessionPathsNameTheSessionKeyedEntries pins the entries the layout links
// from every account into the one session store: each a single top-level
// name under a Claude config dir, none named twice.
func TestSessionPathsNameTheSessionKeyedEntries(t *testing.T) {
	want := []string{"projects", "file-history", "tasks", "session-env"}
	if !reflect.DeepEqual(SessionPaths, want) {
		t.Fatalf("SessionPaths = %v, want %v", SessionPaths, want)
	}
}
