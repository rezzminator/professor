package gather_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gather/gathertest"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	doorUID  = 501
	doorSelf = 4242
)

var doorArgv = []string{"/opt/pfm/bin/pfm", "chat", "watch"}

func doorTarget() gather.Identity {
	return gather.Identity{
		PID: 300, Start: 7000, UID: doorUID, Argv: slices.Clone(doorArgv), Executable: "/opt/pfm/bin/pfm",
	}
}

func newDoorTable() *gathertest.Table {
	return &gathertest.Table{Processes: map[int]*gathertest.Process{
		300: {Start: 7000, UID: doorUID, ParentPID: 1, Argv: slices.Clone(doorArgv), Exe: "/opt/pfm/bin/pfm"},
	}}
}

func sentTo(pid int, sigs ...syscall.Signal) []gathertest.Sent {
	out := make([]gathertest.Sent, 0, len(sigs))
	for _, sig := range sigs {
		out = append(out, gathertest.Sent{PID: pid, Sig: sig})
	}
	return out
}

// TestDoorSignal: a signal reaches only the generation discovery captured,
// still matching the site's own check; every other live state sends nothing.
func TestDoorSignal(t *testing.T) {
	never := func(gather.Identity) (bool, error) { return false, nil }
	failing := func(gather.Identity) (bool, error) { return false, errors.New("environment unreadable") }
	refuseOpen := func(err error) func(*gathertest.Table, *gathertest.Kernel) {
		return func(_ *gathertest.Table, kernel *gathertest.Kernel) {
			kernel.OnOpen = func(int) error { return err }
		}
	}
	tests := []struct {
		name    string
		mutate  func(table *gathertest.Table)
		target  func(id gather.Identity) gather.Target
		kernel  func(table *gathertest.Table, kernel *gathertest.Kernel)
		want    gather.Outcome
		wantErr string
		wantOut []gathertest.Sent
	}{
		{name: "same identity", want: gather.OutcomeSent, wantOut: sentTo(300, syscall.SIGTERM)},
		{
			name:   "pid reused by a later generation",
			mutate: func(table *gathertest.Table) { table.Processes[300].Start = 7100 },
			want:   gather.OutcomeGone,
		},
		{
			name:   "uid differs",
			mutate: func(table *gathertest.Table) { table.Processes[300].UID = 0 },
			want:   gather.OutcomeChanged,
		},
		{
			name:   "argv changed",
			mutate: func(table *gathertest.Table) { table.Processes[300].Argv = []string{"/usr/bin/vim"} },
			want:   gather.OutcomeChanged,
		},
		{
			name:   "executable changed",
			mutate: func(table *gathertest.Table) { table.Processes[300].Exe = "/usr/bin/vim" },
			want:   gather.OutcomeChanged,
		},
		{
			name:   "executable unreadable still identifies by start, owner and argv",
			mutate: func(table *gathertest.Table) { table.Processes[300].Exe = "" },
			want:   gather.OutcomeSent, wantOut: sentTo(300, syscall.SIGTERM),
		},
		{
			// A child read the moment its starter returns: the kernel publishes argv
			// after it closes the exec's close-on-exec fds, so the capture saw none.
			name: "argv not yet published at capture still identifies by start, owner and executable",
			target: func(id gather.Identity) gather.Target {
				id.Argv = nil
				return gather.Target{Identity: id}
			},
			want: gather.OutcomeSent, wantOut: sentTo(300, syscall.SIGTERM),
		},
		{
			name:   "gone",
			mutate: func(table *gathertest.Table) { delete(table.Processes, 300) },
			want:   gather.OutcomeGone,
		},
		{
			name:   "zombie",
			mutate: func(table *gathertest.Table) { table.Processes[300].Zombie = true },
			want:   gather.OutcomeGone,
		},
		{
			name:   "check no longer holds",
			target: func(id gather.Identity) gather.Target { return gather.Target{Identity: id, Check: never} },
			want:   gather.OutcomeChanged,
		},
		{
			name:    "check cannot tell",
			target:  func(id gather.Identity) gather.Target { return gather.Target{Identity: id, Check: failing} },
			wantErr: "check pid 300 before terminated: environment unreadable",
		},
		{
			name: "pid reused between discovery and open",
			kernel: func(table *gathertest.Table, kernel *gathertest.Kernel) {
				kernel.OnOpen = func(pid int) error {
					table.Processes[pid].Start = 7100
					return nil
				}
			},
			want: gather.OutcomeGone,
		},
		{name: "open finds no process", kernel: refuseOpen(syscall.ESRCH), want: gather.OutcomeGone},
		{
			name: "process exits before delivery",
			kernel: func(_ *gathertest.Table, kernel *gathertest.Kernel) {
				kernel.OnSignal = func(int, syscall.Signal) error { return syscall.ESRCH }
			},
			want: gather.OutcomeGone,
		},
		{
			name:    "open fails",
			kernel:  refuseOpen(syscall.ENOSYS),
			wantErr: "open pid 300 to signal: function not implemented",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := newDoorTable()
			kernel := &gathertest.Kernel{}
			if tt.kernel != nil {
				tt.kernel(table, kernel)
			}
			door := gathertest.Door(table, kernel, doorUID, doorSelf)
			if tt.mutate != nil {
				tt.mutate(table)
			}
			target := gather.Target{Identity: doorTarget()}
			if tt.target != nil {
				target = tt.target(doorTarget())
			}
			got, err := door.Signal(target, syscall.SIGTERM)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Signal() error = %v; want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Signal() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Signal() = %v; want %v", got, tt.want)
			}
			if !slices.Equal(kernel.Sent, tt.wantOut) {
				t.Fatalf("signals sent = %v; want %v", kernel.Sent, tt.wantOut)
			}
		})
	}
}

// TestDoorSignalRechecksAfterTheCheck: a site's Check may take time, so the
// door re-reads after it whatever it answered: a generation that replaced the
// pid meanwhile gets nothing, a process that exited reads gone (not changed),
// and the check itself saw the live process.
func TestDoorSignalRechecksAfterTheCheck(t *testing.T) {
	tests := []struct {
		name  string
		holds bool
		// during is what happens to pid 300 while the Check runs.
		during func(table *gathertest.Table)
		want   gather.Outcome
	}{
		{
			name:  "pid reused while the check held",
			holds: true,
			during: func(table *gathertest.Table) {
				table.Processes[300] = &gathertest.Process{Start: 7100, UID: doorUID, Argv: slices.Clone(doorArgv)}
			},
			want: gather.OutcomeGone,
		},
		{
			name:   "exited while the check failed",
			during: func(table *gathertest.Table) { delete(table.Processes, 300) },
			want:   gather.OutcomeGone,
		},
		{name: "check failed on the same process", during: func(*gathertest.Table) {}, want: gather.OutcomeChanged},
		{
			name:   "check held on the same process",
			holds:  true,
			during: func(*gathertest.Table) {},
			want:   gather.OutcomeSent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := newDoorTable()
			kernel := &gathertest.Kernel{}
			door := gathertest.Door(table, kernel, doorUID, doorSelf)
			var checked gather.Identity
			target := gather.Target{Identity: doorTarget(), Check: func(live gather.Identity) (bool, error) {
				checked = live
				tt.during(table)
				return tt.holds, nil
			}}
			got, err := door.Signal(target, syscall.SIGTERM)
			if err != nil || got != tt.want {
				t.Fatalf("Signal() = %v, %v; want %v", got, err, tt.want)
			}
			if checked.Start != 7000 || !slices.Equal(checked.Argv, doorArgv) {
				t.Fatalf("Check saw %+v; want the captured generation", checked)
			}
			wantSent := []gathertest.Sent(nil)
			if tt.want == gather.OutcomeSent {
				wantSent = []gathertest.Sent{{PID: 300, Sig: syscall.SIGTERM}}
			}
			if !slices.Equal(kernel.Sent, wantSent) {
				t.Fatalf("signals sent = %v; want %v", kernel.Sent, wantSent)
			}
		})
	}
}

// TestDoorRefuses: pid 1 and below, this process, another user's process and
// an identity without a start time are never opened, whatever the table says.
func TestDoorRefuses(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(id *gather.Identity)
		wantErr string
	}{
		{
			name:    "init",
			edit:    func(id *gather.Identity) { id.PID = 1 },
			wantErr: "pid 1: signal refused: not a single user process",
		},
		{
			name:    "process group",
			edit:    func(id *gather.Identity) { id.PID = 0 },
			wantErr: "pid 0: signal refused: not a single user process",
		},
		{
			name:    "every process",
			edit:    func(id *gather.Identity) { id.PID = -1 },
			wantErr: "pid -1: signal refused: not a single user process",
		},
		{
			name:    "this process",
			edit:    func(id *gather.Identity) { id.PID = doorSelf },
			wantErr: "pid 4242: signal refused: this process",
		},
		{
			name:    "another user",
			edit:    func(id *gather.Identity) { id.UID = 0 },
			wantErr: "pid 300: signal refused: runs as uid 0, not 501",
		},
		{
			name:    "no start time",
			edit:    func(id *gather.Identity) { id.Start = 0 },
			wantErr: "pid 300: signal refused: no start time to tell its generation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := newDoorTable()
			kernel := &gathertest.Kernel{}
			door := gathertest.Door(table, kernel, doorUID, doorSelf)
			id := doorTarget()
			tt.edit(&id)
			table.Processes[id.PID] = &gathertest.Process{Start: id.Start, UID: id.UID, Argv: id.Argv}
			got, err := door.Signal(gather.Target{Identity: id}, syscall.SIGKILL)
			if !errors.Is(err, gather.ErrRefused) || err.Error() != tt.wantErr {
				t.Fatalf("Signal() = %v, %v; want %q", got, err, tt.wantErr)
			}
			if kernel.Opens != 0 || len(kernel.Sent) != 0 {
				t.Fatalf("kernel opened %d, sent %v; want neither", kernel.Opens, kernel.Sent)
			}
		})
	}
}

// ownerless is a process table that cannot read owners, as a fixture root.
type ownerless struct{ gather.ProcFS }

// TestDoorNeedsOwners: a table that cannot read owners (a fixture root)
// identifies nothing, so nothing is signalled.
func TestDoorNeedsOwners(t *testing.T) {
	kernel := &gathertest.Kernel{}
	door := gathertest.Door(ownerless{newDoorTable()}, kernel, doorUID, doorSelf)
	_, err := door.Signal(gather.Target{Identity: doorTarget()}, syscall.SIGTERM)
	want := "re-read pid 300 before signalling: identify process 300: the process table cannot read owners"
	if err == nil || err.Error() != want {
		t.Fatalf("Signal() error = %v; want %q", err, want)
	}
	if len(kernel.Sent) != 0 {
		t.Fatalf("signals sent = %v; want none", kernel.Sent)
	}
}

// TestDoorStop: SIGTERM, an identity-aware wait, then SIGKILL only to the
// same generation; a later generation on the pid is the target gone.
func TestDoorStop(t *testing.T) {
	tests := []struct {
		name     string
		before   func(table *gathertest.Table)
		onSignal func(table *gathertest.Table) func(pid int, sig syscall.Signal) error
		want     gather.Stopped
		wantErr  string
		wantOut  []gathertest.Sent
	}{
		{
			name: "exits on SIGTERM",
			onSignal: func(table *gathertest.Table) func(int, syscall.Signal) error {
				return gathertest.ExitOn(table, syscall.SIGTERM)
			},
			want:    gather.StoppedOnTerm,
			wantOut: sentTo(300, syscall.SIGTERM),
		},
		{
			name: "needs SIGKILL",
			onSignal: func(table *gathertest.Table) func(int, syscall.Signal) error {
				return gathertest.ExitOn(table, syscall.SIGKILL)
			},
			want:    gather.StoppedKilled,
			wantOut: sentTo(300, syscall.SIGTERM, syscall.SIGKILL),
		},
		{
			name: "pid reused after SIGTERM",
			onSignal: func(table *gathertest.Table) func(int, syscall.Signal) error {
				return func(pid int, _ syscall.Signal) error {
					table.Processes[pid] = &gathertest.Process{Start: 7100, UID: doorUID, Argv: slices.Clone(doorArgv)}
					return nil
				}
			},
			want:    gather.StoppedOnTerm,
			wantOut: sentTo(300, syscall.SIGTERM),
		},
		{
			name: "exec'd another program after SIGTERM",
			onSignal: func(table *gathertest.Table) func(int, syscall.Signal) error {
				return func(pid int, _ syscall.Signal) error {
					table.Processes[pid].Argv = []string{"/usr/bin/vim"}
					return nil
				}
			},
			wantErr: "pid 300 changed identity after SIGTERM; SIGKILL withheld",
			wantOut: sentTo(300, syscall.SIGTERM),
		},
		{
			name:    "survives SIGKILL",
			wantErr: "pid 300 still running after SIGKILL",
			wantOut: sentTo(300, syscall.SIGTERM, syscall.SIGKILL),
		},
		{
			name:   "gone before SIGTERM",
			before: func(table *gathertest.Table) { delete(table.Processes, 300) },
			want:   gather.StoppedGone,
		},
		{
			name:   "changed before SIGTERM",
			before: func(table *gathertest.Table) { table.Processes[300].Argv = []string{"/usr/bin/vim"} },
			want:   gather.StoppedChanged,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := newDoorTable()
			kernel := &gathertest.Kernel{}
			if tt.onSignal != nil {
				kernel.OnSignal = tt.onSignal(table)
			}
			door := gathertest.Door(table, kernel, doorUID, doorSelf)
			if tt.before != nil {
				tt.before(table)
			}
			esc := gather.Escalation{Term: 3 * time.Second, Kill: time.Second, Poll: 100 * time.Millisecond}
			got, err := door.Stop(context.Background(), gather.Target{Identity: doorTarget()}, esc)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Stop() error = %v; want %q", err, tt.wantErr)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("Stop() = %v, %v; want %v", got, err, tt.want)
			}
			if !slices.Equal(kernel.Sent, tt.wantOut) {
				t.Fatalf("signals sent = %v; want %v", kernel.Sent, tt.wantOut)
			}
		})
	}
}

// TestDoorStopAll: one schedule for several targets — SIGTERM to each, one
// shared wait, SIGKILL only to the generation that outlived it.
func TestDoorStopAll(t *testing.T) {
	table := newDoorTable()
	table.Processes[301] = &gathertest.Process{Start: 7001, UID: doorUID, Argv: slices.Clone(doorArgv)}
	kernel := &gathertest.Kernel{OnSignal: func(pid int, sig syscall.Signal) error {
		if (pid == 300 && sig == syscall.SIGTERM) || (pid == 301 && sig == syscall.SIGKILL) {
			table.Processes[pid].Zombie = true
		}
		return nil
	}}
	door := gathertest.Door(table, kernel, doorUID, doorSelf)
	second := gather.Identity{PID: 301, Start: 7001, UID: doorUID, Argv: slices.Clone(doorArgv)}
	esc := gather.Escalation{Term: 3 * time.Second, Kill: time.Second, Poll: 100 * time.Millisecond}
	targets := []gather.Target{{Identity: doorTarget()}, {Identity: second}}
	got := door.StopAll(context.Background(), targets, esc)
	want := []gather.StopResult{{Stopped: gather.StoppedOnTerm}, {Stopped: gather.StoppedKilled}}
	if !slices.Equal(got, want) {
		t.Fatalf("StopAll() = %v; want %v", got, want)
	}
	wantOut := append(sentTo(300, syscall.SIGTERM), sentTo(301, syscall.SIGTERM, syscall.SIGKILL)...)
	if !slices.Equal(kernel.Sent, wantOut) {
		t.Fatalf("signals sent = %v; want %v", kernel.Sent, wantOut)
	}
}

// TestInstallOwns: a process is this install's when it carries this install's
// mark, or carries none and executes this install's binary; another mark is
// another install however its binary is named.
func TestInstallOwns(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("pfm"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(directory, "pfm-link")
	if err := os.Symlink(binary, linked); err != nil {
		t.Fatal(err)
	}
	ours := paths.InstallMark(filepath.Join(directory, "home"))
	install := gather.Install{Mark: ours, Binary: binary}
	tests := []struct {
		name    string
		install gather.Install
		process gathertest.Process
		want    bool
		wantErr string
	}{
		{
			name:    "our mark",
			process: gathertest.Process{Environ: map[string]string{paths.EnvInstallMark: ours}, Exe: "/other/pfm"},
			want:    true,
		},
		{
			name: "another install's mark",
			process: gathertest.Process{
				Environ: map[string]string{paths.EnvInstallMark: "0123456789abcdef"}, Exe: binary,
			},
		},
		{name: "no mark, our binary", process: gathertest.Process{Exe: linked}, want: true},
		{name: "no mark, another binary", process: gathertest.Process{Exe: "/work/tree/tmp/bin/pfm.dev"}},
		{
			name:    "no mark, executable unreadable",
			process: gathertest.Process{},
			wantErr: "pid 300 carries no install mark: read its executable: permission denied",
		},
		{
			name:    "environment unreadable",
			process: gathertest.Process{EnvironErr: fs.ErrPermission, Exe: binary},
			wantErr: "read pid 300 environment for its install mark: permission denied",
		},
		{
			name:    "no install to compare",
			install: gather.Install{Binary: binary},
			process: gathertest.Process{Exe: binary},
			wantErr: "install ownership: no install mark or binary to compare",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			process := tt.process
			process.Start, process.UID, process.Argv = 7000, doorUID, doorArgv
			table := &gathertest.Table{Processes: map[int]*gathertest.Process{300: &process}}
			owner := install
			if tt.install != (gather.Install{}) {
				owner = tt.install
			}
			live := gather.Identity{PID: 300, Start: 7000, UID: doorUID, Argv: doorArgv}
			got, err := owner.Owns(table, live)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Owns() error = %v; want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Owns() = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}
