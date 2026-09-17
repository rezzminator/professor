package tmux

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"hostops/pfm/internal/hostfixture"
)

// TestCommandAddressesTheSocketAndClearsTMUX pins the runner's contract: the
// explicit socket first, the caller's arguments after it, the named binary,
// and $TMUX defined but empty whatever the caller's environment holds.
func TestCommandAddressesTheSocketAndClearsTMUX(t *testing.T) {
	t.Setenv("TMUX", "/tmp/caller-server,1,0")
	binary := filepath.Join(t.TempDir(), "tmux-fake")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := Command(context.Background(), binary, "/sockets/cc-1", "list-panes", "-a")
	if command.Path != binary {
		t.Fatalf("binary = %q, want %q", command.Path, binary)
	}
	if want := []string{binary, "-S", "/sockets/cc-1", "list-panes", "-a"}; !slices.Equal(command.Args, want) {
		t.Fatalf("args = %q, want %q", command.Args, want)
	}
	last := ""
	for _, entry := range command.Env {
		if len(entry) >= 5 && entry[:5] == "TMUX=" {
			last = entry
		}
	}
	if last != "TMUX=" {
		t.Fatalf("effective TMUX entry = %q, want it defined and empty", last)
	}
	if defaulted := Command(context.Background(), "", "/sockets/cc-1"); filepath.Base(defaulted.Args[0]) != "tmux" {
		t.Fatalf("default binary = %q, want tmux", defaulted.Args[0])
	}
}

// TestCommandRunsAConfiguredBinaryAsGiven pins that only the default goes
// through the dependency registry: a caller's configured binary is executed
// exactly as configured, never swapped for a registry-resolved (and
// process-cached) path.
func TestCommandRunsAConfiguredBinaryAsGiven(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "tmux-configured"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	path, _, _ := Invocation("tmux-configured", "/sockets/cc-1")
	if path != "tmux-configured" {
		t.Fatalf("configured binary = %q, want it as given", path)
	}
}

// TestCouldNotRunSeparatesAnUnstartableTmuxFromAFailingServer drives real
// exec outcomes, never hand-built errors: a bare name missing from PATH (the
// launchd daemon's live failure), a configured path that does not exist, and a
// file that is not executable all mean tmux never started. A tmux that ran and
// exited non-zero — the shape a dead socket takes — is a per-server failure
// and must stay distinguishable, or one gone chat would fail the whole fleet.
func TestCouldNotRunSeparatesAnUnstartableTmuxFromAFailingServer(t *testing.T) {
	directory := t.TempDir()
	notExecutable := filepath.Join(directory, "tmux-not-executable")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exitsNonZero := filepath.Join(directory, "tmux-exits-1")
	if err := os.WriteFile(
		exitsNonZero,
		[]byte("#!/bin/sh\necho 'no server running' >&2\nexit 1\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)
	for _, test := range []struct {
		name   string
		binary string
		want   bool
	}{
		{name: "bare name absent from PATH", binary: "pfm-test-missing-tmux", want: true},
		{name: "configured path absent", binary: filepath.Join(directory, "absent", "tmux"), want: true},
		{name: "configured path not executable", binary: notExecutable, want: true},
		{name: "tmux ran and exited non-zero", binary: exitsNonZero, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Command(context.Background(), test.binary, "/sockets/cc-1", "list-panes").Run()
			if err == nil {
				t.Fatalf("%s: command succeeded; the fixture must fail", test.binary)
			}
			if got := CouldNotRun(err); got != test.want {
				t.Fatalf("CouldNotRun(%v) = %v, want %v", err, got, test.want)
			}
		})
	}
	if CouldNotRun(nil) {
		t.Fatal("CouldNotRun(nil) = true")
	}
}

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
