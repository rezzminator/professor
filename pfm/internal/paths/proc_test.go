package paths

import "testing"

func TestProcRootOrKeepsAConfiguredRoot(t *testing.T) {
	for _, row := range []struct {
		root string
		want string
	}{
		{root: "", want: "/proc"},
		{root: "/jail/proc", want: "/jail/proc"},
	} {
		if got := ProcRootOr(row.root); got != row.want {
			t.Errorf("ProcRootOr(%q) = %q, want %q", row.root, got, row.want)
		}
	}
}
