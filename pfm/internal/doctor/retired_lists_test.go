package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// A clone whose retired lists cannot be read is one FAIL row naming the file,
// and the checks needing a list run on empty ones; a readable file prints nothing.
func TestLoadRetiredLists(t *testing.T) {
	for _, testCase := range []struct {
		name, raw string
		failures  int
		want      retired.Lists
	}{
		{name: "readable", raw: `{"units":["old.service"]}`, want: retired.Lists{Units: []string{"old.service"}}},
		{name: "unparseable", raw: `{"units":`, failures: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home, clone := t.TempDir(), t.TempDir()
			file := filepath.Join(clone, filepath.FromSlash(retired.FileRel))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(testCase.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			lists, failures := loadRetiredLists(&stdout, home)
			if failures != testCase.failures || !reflect.DeepEqual(lists, testCase.want) {
				t.Fatalf(
					"loadRetiredLists() = %+v, %d; want %+v, %d",
					lists,
					failures,
					testCase.want,
					testCase.failures,
				)
			}
			wantPrefix := ""
			if testCase.failures != 0 {
				wantPrefix = "doctor: retired_lists FAIL retired lists " + file + ": decode: "
			}
			if got := stdout.String(); wantPrefix == "" && got != "" || !strings.HasPrefix(got, wantPrefix) ||
				wantPrefix != "" && !strings.HasSuffix(got, " — restore "+retired.FileRel+" in the clone\n") {
				t.Fatalf("stdout = %q, want a row starting %q", got, wantPrefix)
			}
		})
	}
}
