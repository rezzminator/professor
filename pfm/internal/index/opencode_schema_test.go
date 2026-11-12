package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestReadOpenCodeSessionsWhileTheStoreIsCreated: OpenCode creates its store file before
// its migration commits the schema, so a store with none of the mirror's tables is a store
// being created and reads as no sessions; a store holding only some is broken and says
// which tables it holds and lacks.
func TestReadOpenCodeSessionsWhileTheStoreIsCreated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, root string)
		wantErr string
	}{
		{name: "empty store file", prepare: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "opencode.db"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "only a table the mirror does not read", prepare: func(t *testing.T, root string) {
			mutateOpenCodeStore(t, root, "CREATE TABLE migration (id TEXT)")
		}},
		{
			name: "some of the mirror's tables",
			prepare: func(t *testing.T, root string) {
				mutateOpenCodeStore(t, root, "CREATE TABLE message (id TEXT); CREATE TABLE session (id TEXT)")
			},
			wantErr: "opencode store holds tables [message session] but not [part project]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.prepare(t, root)
			sessions, err := ReadOpenCodeSessions(context.Background(), root)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tc.wantErr || len(sessions) != 0 {
				t.Fatalf("sessions %#v, error %q; want none, error %q", sessions, gotErr, tc.wantErr)
			}
		})
	}
}
