package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestReadOpenCodeSessionsWhileTheStoreIsCreated: OpenCode creates its store file before
// its migration commits the schema, so a store with none of the mirror's tables is a store
// being created and reads as no sessions; a store holding only some is a store not ready
// either: no sessions, no error, and one WARN record naming the store and which tables it
// holds and lacks.
func TestReadOpenCodeSessionsWhileTheStoreIsCreated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prepare  func(t *testing.T, root string)
		wantWarn string
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
			wantWarn: "opencode store is not ready: it holds some of the mirror's tables " +
				"path={root}/opencode.db reason=holds tables [message session], lacks [part project]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			root := t.TempDir()
			tc.prepare(t, root)
			sessions, err := ReadOpenCodeSessions(ctx, root)
			if err != nil || len(sessions) != 0 {
				t.Fatalf("sessions %#v, error %v; want none and no error", sessions, err)
			}
			var warned []string
			for _, record := range recorder.Records() {
				if record.Level != "WARN" && record.Level != "ERROR" {
					continue
				}
				path, _ := record.Field("path")
				reason, _ := record.Field("reason")
				warned = append(warned, fmt.Sprintf("%s path=%v reason=%v", record.Message, path, reason))
			}
			var want []string
			if tc.wantWarn != "" {
				want = []string{tc.wantWarn}
			}
			for index := range want {
				want[index] = strings.ReplaceAll(want[index], "{root}", root)
			}
			if fmt.Sprint(warned) != fmt.Sprint(want) {
				t.Fatalf("WARN and ERROR records %q, want %q: %s", warned, want, recorder.Raw())
			}
		})
	}
}
