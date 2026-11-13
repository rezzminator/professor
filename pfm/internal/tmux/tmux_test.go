package tmux

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/hostfixture"
)

// TestCouldNotRunClassifiesHostfixtureNoTmuxsLookPathError is hostfixture
// case 4 (NoTmux): a caller that resolves tmux through a deps.Runner
// (deps.FakeRunner.LookPath scripted ENOENT, exactly the shape NoTmux
// builds) must have that error classified as CouldNotRun — the same "tmux
// itself never started" bucket the real-exec ENOENT cases above land in —
// so a probe sweeping every socket fails whole instead of reporting an
// empty fleet.
func TestCouldNotRunClassifiesHostfixtureNoTmuxsLookPathError(t *testing.T) {
	fixture := hostfixture.NoTmux(t)
	_, err := fixture.Runner.LookPath("tmux")
	if err == nil {
		t.Fatal("NoTmux fixture's LookPath(tmux) returned nil error")
	}
	if !CouldNotRun(err) {
		t.Fatalf("CouldNotRun(%v) = false, want true for NoTmux's scripted ENOENT", err)
	}
}

func TestSocketPath(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"/tmp/tmux-1000/cc-1,4242,0", "/tmp/tmux-1000/cc-1"},
		{"", ""},
		{",4242,0", ""},
		{"/tmp/tmux-1000/cc-1", "/tmp/tmux-1000/cc-1"},
	} {
		if got := SocketPath(tc.value); got != tc.want {
			t.Errorf("SocketPath(%q)=%q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestNamedServer(t *testing.T) {
	for _, tc := range []struct {
		name              string
		argv              []string
		label, socketPath string
		isTmux            bool
	}{
		{"socket path", []string{"/usr/bin/tmux", "-S", "/sockets/cc-1", "new-session", "-d"}, "", "/sockets/cc-1", true},
		{"label", []string{"tmux", "-L", "cc-1", "attach"}, "cc-1", "", true},
		{"attached values", []string{"tmux", "-Lcc-1", "-S/sockets/cc-2"}, "cc-1", "/sockets/cc-2", true},
		{"config value skipped", []string{"tmux", "-f", "-L", "-L", "cc-1"}, "cc-1", "", true},
		{"grouped label", []string{"tmux", "-uL", "cc-1", "attach"}, "cc-1", "", true},
		{"grouped attached socket path", []string{"tmux", "-2uS/sockets/cc-2"}, "", "/sockets/cc-2", true},
		{"grouped flags without a value", []string{"tmux", "-2u", "-L", "cc-1"}, "cc-1", "", true},
		{"flags after the command ignored", []string{"tmux", "new-session", "-L", "cc-1"}, "", "", true},
		{"end of options", []string{"tmux", "--", "-L", "cc-1"}, "", "", true},
		{"not tmux", []string{"/usr/bin/vim", "-L", "cc-1"}, "", "", false},
		{"empty", nil, "", "", false},
	} {
		label, socketPath, isTmux := NamedServer(tc.argv)
		if label != tc.label || socketPath != tc.socketPath || isTmux != tc.isTmux {
			t.Errorf("%s: NamedServer(%q) = %q, %q, %v; want %q, %q, %v",
				tc.name, tc.argv, label, socketPath, isTmux, tc.label, tc.socketPath, tc.isTmux)
		}
	}
}
