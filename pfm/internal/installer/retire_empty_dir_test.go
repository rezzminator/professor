package installer

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

func TestRetireEmptyDir(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                      string
		apply, removed, wantError bool
		planErrors                int
	}{
		{name: "dry run conflict", planErrors: 1},
		{name: "apply refusal", apply: true, wantError: true},
		{name: "same-pass removals discounted", removed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			dir := skillStoreRoot(home)
			stray := filepath.Join(dir, "entry")
			writeFixture(t, stray, "preserved entry\n")
			var output bytes.Buffer
			installer := &engine{options: Options{Home: home, Stdout: &output}, apply: test.apply}
			if test.removed {
				installer.markRemoved(stray)
			}
			refusal := fmt.Sprintf(
				"refuse to retire non-empty directory %s — move or delete %s, then rerun",
				dir,
				stray,
			)
			err := installer.retireEmptyDir(dir)
			if test.wantError {
				if err == nil || err.Error() != refusal {
					t.Fatalf("error=%v, want %q", err, refusal)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(installer.planErrors) != test.planErrors {
				t.Fatalf("plan errors=%v, want %d", installer.planErrors, test.planErrors)
			}
			if test.planErrors != 0 && installer.planErrors[0].Error() != refusal {
				t.Fatalf("plan error=%q, want %q", installer.planErrors[0], refusal)
			}
			want := ""
			if !test.apply {
				if test.removed {
					want = "  change  remove empty " + dir + "\n"
				} else {
					want = "  conflict " + refusal + "\n"
				}
			}
			if output.String() != want {
				t.Fatalf("transcript=%q, want %q", output.String(), want)
			}
			if got := readFixture(t, stray); got != "preserved entry\n" {
				t.Fatalf("entry=%q, want preserved bytes", got)
			}
		})
	}
}
