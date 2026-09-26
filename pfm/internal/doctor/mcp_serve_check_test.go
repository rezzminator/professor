package doctor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/stale"
)

type mcpServeProcTable struct {
	pids       []int
	pidsErr    error
	cmdlines   map[int][]string
	cmdlineErr map[int]error
	environs   map[int]map[string]string
	environErr map[int]error
	images     map[int]gather.FileID
	imageErr   map[int]error
	fdLinks    map[int][]gather.FDLink
	fdLinkErr  map[int]error
}

func (table mcpServeProcTable) PIDs() ([]int, error) { return table.pids, table.pidsErr }

func (table mcpServeProcTable) Cmdline(pid int) ([]string, error) {
	return table.cmdlines[pid], table.cmdlineErr[pid]
}

func (table mcpServeProcTable) Environ(pid int) (map[string]string, error) {
	return table.environs[pid], table.environErr[pid]
}

func (table mcpServeProcTable) FDLinks(pid int) ([]gather.FDLink, error) {
	return table.fdLinks[pid], table.fdLinkErr[pid]
}

func (mcpServeProcTable) Stat(int) (gather.ProcStat, error) { return gather.ProcStat{}, nil }

func (table mcpServeProcTable) Image(pid int) (gather.FileID, error) {
	return table.images[pid], table.imageErr[pid]
}

type mcpServeImagelessTable struct{ gather.ProcFS }

type mcpServeIdentityTable struct {
	mcpServeProcTable
	identities   map[int]gather.ProcessIdentity
	identityErr  map[int]error
	commandReads map[int]int
}

func (table *mcpServeIdentityTable) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	return table.identities[pid], table.identityErr[pid]
}

func (table *mcpServeIdentityTable) Cmdline(pid int) ([]string, error) {
	table.commandReads[pid]++
	return table.mcpServeProcTable.Cmdline(pid)
}

type mcpServeChangingCmdlineTable struct {
	mcpServeProcTable
	reads int
}

func (table *mcpServeChangingCmdlineTable) Cmdline(pid int) ([]string, error) {
	table.reads++
	if table.reads > 1 {
		return nil, errors.New("command probe failed")
	}
	return table.mcpServeProcTable.Cmdline(pid)
}

// Every fixture pid in this file sits above any kernel's pid ceiling (Linux
// PID_MAX_LIMIT is 4194304): stale.Find skips the scanning process's own pid,
// so a small fixture pid equal to the test binary's pid — likely in a fresh
// fence container — would silently drop that row.
func newMCPServeDoctorFixture(t *testing.T) (config.Runtime, gather.FileID, gather.FileID) {
	t.Helper()
	home := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "pfm")
	if err := writeExecutable(binary); err != nil {
		t.Fatal(err)
	}
	fresh, err := gather.FileIDOf(binary)
	if err != nil {
		t.Fatal(err)
	}
	staleImage := fresh
	staleImage.Inode++
	return config.Runtime{Paths: paths.Values{Home: home}}, fresh, staleImage
}

func writeExecutable(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("fixture"), 0o700)
}

func TestMCPServeProcessesDoctorReportsFreshAndStaleRows(t *testing.T) {
	runtime, fresh, replaced := newMCPServeDoctorFixture(t)
	tests := []struct {
		name         string
		table        mcpServeProcTable
		wantWarnings int
		want         []string
		notWant      []string
	}{
		{
			name: "all fresh",
			table: mcpServeProcTable{
				pids:     []int{50000101},
				cmdlines: map[int][]string{50000101: {"pfm", "mcp", "serve", "--stdio"}},
				images:   map[int]gather.FileID{50000101: fresh},
			},
			want:    []string{"doctor: mcp-serve clean checked=1"},
			notWant: []string{"STALE", "UNREAD"},
		},
		{
			name: "one replaced with session identity",
			table: mcpServeProcTable{
				pids:       []int{50000201},
				cmdlines:   map[int][]string{50000201: {"pfm", "mcp", "serve", "--stdio"}},
				environs:   map[int]map[string]string{50000201: {resolve.ClaudeSessionEnv: "claude-session"}},
				images:     map[int]gather.FileID{50000201: replaced},
				imageErr:   map[int]error{},
				environErr: map[int]error{},
			},
			wantWarnings: 1,
			want: []string{
				"doctor: mcp-serve STALE pid=50000201 chat=claude-session command=pfm mcp serve --stdio",
			},
			notWant: []string{"clean"},
		},
		{
			name: "three replaced",
			table: mcpServeProcTable{
				pids: []int{50000301, 50000302, 50000303},
				cmdlines: map[int][]string{
					50000301: {"pfm", "mcp", "serve", "--stdio"},
					50000302: {"pfm", "mcp", "serve", "--stdio"},
					50000303: {"pfm", "mcp", "serve", "--stdio"},
				},
				environs: map[int]map[string]string{
					50000301: {resolve.CodexThreadEnv: "codex-thread"},
					50000302: {"TMUX_PANE": "%2"},
					50000303: {"TMUX": "/tmp/tmux-user/cx-chat,42,0"},
				},
				images: map[int]gather.FileID{50000301: replaced, 50000302: replaced, 50000303: replaced},
			},
			wantWarnings: 3,
			want: []string{
				"pid=50000301 chat=codex-thread",
				"pid=50000302 chat=%2",
				"pid=50000303 chat=/tmp/tmux-user/cx-chat",
			},
			notWant: []string{"clean"},
		},
		{
			name: "same pane on different sockets",
			table: mcpServeProcTable{
				pids: []int{50000304, 50000305},
				cmdlines: map[int][]string{
					50000304: {"pfm", "mcp", "serve", "--stdio"},
					50000305: {"pfm", "mcp", "serve", "--stdio"},
				},
				environs: map[int]map[string]string{
					50000304: {"TMUX": "/tmp/tmux-user/cc-first,42,0", "TMUX_PANE": "%0"},
					50000305: {"TMUX": "/tmp/tmux-user/cc-second,43,0", "TMUX_PANE": "%0"},
				},
				images: map[int]gather.FileID{50000304: replaced, 50000305: replaced},
			},
			wantWarnings: 2,
			want: []string{
				"pid=50000304 chat=/tmp/tmux-user/cc-first:%0",
				"pid=50000305 chat=/tmp/tmux-user/cc-second:%0",
			},
			notWant: []string{"clean"},
		},
		{
			name: "configured servers",
			table: mcpServeProcTable{
				pids: []int{50000351, 50000352},
				cmdlines: map[int][]string{
					50000351: {"pfm", "--config", "/config/pfm.json", "mcp", "serve", "--stdio"},
					50000352: {"pfm", "--config=/config/pfm.json", "mcp", "serve", "--stdio"},
				},
				images: map[int]gather.FileID{50000351: replaced, 50000352: replaced},
			},
			wantWarnings: 2,
			want: []string{
				"pid=50000351 chat=UNRESOLVED",
				"pid=50000352 chat=UNRESOLVED",
			},
			notWant: []string{"clean"},
		},
		{
			// The upgrade that brings the professor server replaces the binary
			// under stdio servers a chat launched with the retired argv; they
			// are exactly the stale processes this check exists to name.
			name: "pre-professor stdio servers still running",
			table: mcpServeProcTable{
				pids: []int{50000361, 50000362, 50000363},
				cmdlines: map[int][]string{
					50000361: {"pfm", "mcp"},
					50000362: {"pfm", "mcp", "chat", "serve"},
					50000363: {"pfm", "mcp", "harvester", "serve", "--transport", "stdio"},
				},
				images: map[int]gather.FileID{50000361: replaced, 50000362: replaced, 50000363: replaced},
			},
			wantWarnings: 3,
			want: []string{
				"pid=50000361 chat=UNRESOLVED",
				"pid=50000362 chat=UNRESOLVED",
				"pid=50000363 chat=UNRESOLVED",
			},
			notWant: []string{"clean"},
		},
		{
			name: "unresolved chat",
			table: mcpServeProcTable{
				pids:       []int{50000401},
				cmdlines:   map[int][]string{50000401: {"pfm", "mcp", "serve"}},
				environErr: map[int]error{50000401: errors.New("permission denied")},
				images:     map[int]gather.FileID{50000401: replaced},
			},
			wantWarnings: 1,
			want:         []string{"pid=50000401 chat=UNRESOLVED"},
		},
		{
			name: "non mcp serve is excluded",
			table: mcpServeProcTable{
				pids: []int{50000501, 50000502},
				cmdlines: map[int][]string{
					50000501: {"pfm", "ls"},
					50000502: {"other", "mcp", "serve"},
				},
				images: map[int]gather.FileID{50000501: replaced, 50000502: replaced},
			},
			want:    []string{"doctor: mcp-serve clean checked=0"},
			notWant: []string{"pid=50000501", "STALE", "UNREAD"},
		},
		{
			name: "unreadable non mcp process is excluded",
			table: mcpServeProcTable{
				pids:     []int{50000551},
				cmdlines: map[int][]string{50000551: {"pfm", "ls"}},
				imageErr: map[int]error{50000551: errors.New("readlink denied")},
			},
			want:    []string{"doctor: mcp-serve clean checked=0"},
			notWant: []string{"pid=50000551", "STALE", "UNREAD"},
		},
		{
			name: "unreadable unclassified process is reported",
			table: mcpServeProcTable{
				pids:       []int{50000552},
				cmdlineErr: map[int]error{50000552: errors.New("permission denied")},
			},
			wantWarnings: 1,
			want:         []string{"doctor: mcp-serve UNREAD — pid=50000552  read command: permission denied"},
			notWant:      []string{"clean"},
		},
		{
			name: "fresh server does not hide unreadable unclassified process",
			table: mcpServeProcTable{
				pids:       []int{50000553, 50000554},
				cmdlines:   map[int][]string{50000553: {"pfm", "mcp", "serve", "--stdio"}},
				cmdlineErr: map[int]error{50000554: errors.New("permission denied")},
				images:     map[int]gather.FileID{50000553: fresh},
			},
			wantWarnings: 1,
			want:         []string{"doctor: mcp-serve UNREAD — pid=50000554  read command: permission denied"},
			notWant:      []string{"clean"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			warnings := printMCPServeProcessesDoctorWithSignaler(
				&output,
				runtime,
				test.table,
				func(int, syscall.Signal) error { return nil },
			)
			if warnings != test.wantWarnings {
				t.Fatalf("warnings=%d, want %d\n%s", warnings, test.wantWarnings, output.String())
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(output.String(), notWant) {
					t.Fatalf("output unexpectedly contains %q:\n%s", notWant, output.String())
				}
			}
		})
	}
}

func TestMCPServeProcessesDoctorClassifiesCompatibleProxies(t *testing.T) {
	runtime, _, replaced := newMCPServeDoctorFixture(t)
	handle, err := stale.HoldCompatibleProxy(runtime.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := handle.Close(); closeErr != nil {
			t.Errorf("close compatibility marker: %v", closeErr)
		}
	})
	marker, err := filepath.EvalSymlinks(handle.Name())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		table        mcpServeProcTable
		signalErr    map[int]error
		wantWarnings int
		want         []string
		notWant      []string
	}{
		{
			name: "compatible only",
			table: mcpServeProcTable{
				pids:     []int{50000211},
				cmdlines: map[int][]string{50000211: {"pfm", "mcp", "serve", "--stdio"}},
				images:   map[int]gather.FileID{50000211: replaced},
				fdLinks:  map[int][]gather.FDLink{50000211: {{FD: 9, Target: marker}}},
			},
			want: []string{
				"doctor: mcp-serve COMPATIBLE pid=50000211 chat=UNRESOLVED command=pfm mcp serve --stdio",
			},
			notWant: []string{"STALE", "clean"},
		},
		{
			name: "mixed compatible and obsolete",
			table: mcpServeProcTable{
				pids: []int{50000212, 50000213},
				cmdlines: map[int][]string{
					50000212: {"pfm", "mcp", "serve", "--stdio"},
					50000213: {"pfm", "mcp", "serve", "--stdio"},
				},
				images:  map[int]gather.FileID{50000212: replaced, 50000213: replaced},
				fdLinks: map[int][]gather.FDLink{50000212: {{FD: 9, Target: marker}}},
			},
			wantWarnings: 1,
			want:         []string{"COMPATIBLE pid=50000212", "STALE pid=50000213"},
			notWant:      []string{"clean"},
		},
		{
			name: "descriptor unreadable",
			table: mcpServeProcTable{
				pids:      []int{50000214},
				cmdlines:  map[int][]string{50000214: {"pfm", "mcp", "serve", "--stdio"}},
				images:    map[int]gather.FileID{50000214: replaced},
				fdLinkErr: map[int]error{50000214: errors.New("descriptor denied")},
			},
			wantWarnings: 2,
			want: []string{
				"doctor: mcp-serve UNREAD — inspect descriptors for pid=50000214",
				marker,
				"descriptor denied",
				"doctor: mcp-serve STALE pid=50000214 chat=UNRESOLVED command=pfm mcp serve --stdio",
			},
			notWant: []string{"COMPATIBLE", "clean"},
		},
		{
			name: "classification failure keeps every stale row",
			table: mcpServeProcTable{
				pids: []int{50000216, 50000217, 50000218},
				cmdlines: map[int][]string{
					50000216: {"pfm", "mcp", "serve", "--stdio"},
					50000217: {"pfm", "mcp", "serve"},
					50000218: {"pfm", "mcp", "chat", "serve"},
				},
				images: map[int]gather.FileID{
					50000216: replaced, 50000217: replaced, 50000218: replaced,
				},
				fdLinkErr: map[int]error{50000216: errors.New("descriptor denied")},
			},
			wantWarnings: 4,
			want: []string{
				"doctor: mcp-serve UNREAD — inspect descriptors for pid=50000216",
				"doctor: mcp-serve STALE pid=50000216 chat=UNRESOLVED command=pfm mcp serve --stdio",
				"doctor: mcp-serve STALE pid=50000217 chat=UNRESOLVED command=pfm mcp serve",
				"doctor: mcp-serve STALE pid=50000218 chat=UNRESOLVED command=pfm mcp chat serve",
			},
			notWant: []string{"COMPATIBLE", "clean"},
		},
		{
			name: "candidate exits during classification",
			table: mcpServeProcTable{
				pids:      []int{50000215},
				cmdlines:  map[int][]string{50000215: {"pfm", "mcp", "serve", "--stdio"}},
				images:    map[int]gather.FileID{50000215: replaced},
				fdLinkErr: map[int]error{50000215: errors.New("process vanished")},
			},
			signalErr: map[int]error{50000215: syscall.ESRCH},
			want:      []string{"doctor: mcp-serve clean checked=1"},
			notWant:   []string{"pid=50000215", "STALE", "COMPATIBLE", "UNREAD"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			warnings := printMCPServeProcessesDoctorWithSignaler(
				&output,
				runtime,
				test.table,
				func(pid int, _ syscall.Signal) error { return test.signalErr[pid] },
			)
			if warnings != test.wantWarnings {
				t.Fatalf("warnings=%d, want %d\n%s", warnings, test.wantWarnings, output.String())
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(output.String(), notWant) {
					t.Fatalf("output unexpectedly contains %q:\n%s", notWant, output.String())
				}
			}
		})
	}
}

func TestMCPServeProcessesDoctorScopesCommandReadsByIdentity(t *testing.T) {
	runtime, _, _ := newMCPServeDoctorFixture(t)
	const currentUID = uint32(1200)
	tests := []struct {
		name         string
		pid          int
		identity     gather.ProcessIdentity
		identityErr  error
		cmdline      []string
		commandErr   error
		wantReads    int
		wantWarnings int
		want         []string
		notWant      string
	}{
		{
			name:         "other owner is excluded before command read",
			pid:          50000701,
			identity:     gather.ProcessIdentity{EffectiveUID: currentUID + 1, Command: "pfm"},
			commandErr:   errors.New("permission denied"),
			wantWarnings: 0,
			want:         []string{"doctor: mcp-serve clean checked=0"},
			notWant:      "pid=50000701",
		},
		{
			name:         "same owner other command is excluded before command read",
			pid:          50000702,
			identity:     gather.ProcessIdentity{EffectiveUID: currentUID, Command: "sleep"},
			commandErr:   errors.New("operation not permitted"),
			wantWarnings: 0,
			want:         []string{"doctor: mcp-serve clean checked=0"},
			notWant:      "pid=50000702",
		},
		{
			name:         "same owner pfm command failure remains unread",
			pid:          50000703,
			identity:     gather.ProcessIdentity{EffectiveUID: currentUID, Command: "pfm"},
			commandErr:   errors.New("permission denied"),
			wantReads:    1,
			wantWarnings: 1,
			want:         []string{"doctor: mcp-serve UNREAD — pid=50000703  read command: permission denied"},
			notWant:      "clean",
		},
		{
			name:         "identity failure with another pfm subcommand is dropped",
			pid:          50000704,
			identityErr:  errors.New("status denied"),
			cmdline:      []string{"pfm", "ls"},
			wantReads:    1,
			wantWarnings: 0,
			want:         []string{"doctor: mcp-serve clean checked=0"},
			notWant:      "pid=50000704",
		},
		{
			name:         "identity failure with an mcp serve command is checked as a candidate",
			pid:          50000706,
			identityErr:  errors.New("status denied"),
			cmdline:      []string{"pfm", "mcp", "serve", "--stdio"},
			wantReads:    1,
			wantWarnings: 2,
			want: []string{
				"doctor: mcp-serve STALE pid=50000706 chat=UNRESOLVED command=pfm mcp serve --stdio",
				"doctor: mcp-serve UNREAD — pid=50000706  read process identity: status denied",
			},
			notWant: "clean",
		},
		{
			name:         "empty command identity of a candidate remains unread",
			pid:          50000705,
			identity:     gather.ProcessIdentity{EffectiveUID: currentUID},
			cmdline:      []string{"pfm", "mcp", "serve"},
			wantReads:    1,
			wantWarnings: 2,
			want: []string{
				"doctor: mcp-serve STALE pid=50000705",
				"doctor: mcp-serve UNREAD — pid=50000705  read process identity: empty command name",
			},
			notWant: "clean",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := &mcpServeIdentityTable{
				mcpServeProcTable: mcpServeProcTable{
					pids:       []int{test.pid},
					cmdlines:   map[int][]string{test.pid: test.cmdline},
					cmdlineErr: map[int]error{test.pid: test.commandErr},
				},
				identities:   map[int]gather.ProcessIdentity{test.pid: test.identity},
				identityErr:  map[int]error{test.pid: test.identityErr},
				commandReads: make(map[int]int),
			}
			var output bytes.Buffer
			warnings := printMCPServeProcessesDoctorWithSignalerAndUID(
				&output,
				runtime,
				table,
				func(int, syscall.Signal) error { return nil },
				currentUID,
			)
			if warnings != test.wantWarnings {
				t.Fatalf("warnings=%d, want %d\n%s", warnings, test.wantWarnings, output.String())
			}
			if reads := table.commandReads[test.pid]; reads != test.wantReads {
				t.Fatalf("Cmdline(%d) reads=%d, want %d", test.pid, reads, test.wantReads)
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
			}
			if strings.Contains(output.String(), test.notWant) {
				t.Fatalf("output unexpectedly contains %q:\n%s", test.notWant, output.String())
			}
		})
	}
}

func TestMCPServeProcessesDoctorChecksUnreadableCommandLiveness(t *testing.T) {
	runtime, _, _ := newMCPServeDoctorFixture(t)
	tests := []struct {
		name         string
		pid          int
		signalErr    error
		wantWarnings int
		want         []string
		notWant      []string
	}{
		{
			name:         "process exited while reading command",
			pid:          50000555,
			signalErr:    syscall.ESRCH,
			wantWarnings: 0,
			want:         []string{"doctor: mcp-serve clean checked=0"},
			notWant:      []string{"pid=50000555", "UNREAD"},
		},
		{
			name:         "liveness probe is indeterminate",
			pid:          50000556,
			signalErr:    syscall.EPERM,
			wantWarnings: 1,
			want: []string{
				"doctor: mcp-serve UNREAD — pid=50000556  read command: permission denied; " +
					"probe liveness with signal 0: operation not permitted",
			},
			notWant: []string{"clean"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := mcpServeProcTable{
				pids:       []int{test.pid},
				cmdlineErr: map[int]error{test.pid: errors.New("permission denied")},
			}
			var signals []syscall.Signal
			var output bytes.Buffer
			warnings := printMCPServeProcessesDoctorWithSignaler(
				&output,
				runtime,
				table,
				func(pid int, signal syscall.Signal) error {
					if pid != test.pid {
						t.Fatalf("signaled pid=%d, want %d", pid, test.pid)
					}
					signals = append(signals, signal)
					return test.signalErr
				},
			)
			if warnings != test.wantWarnings {
				t.Fatalf("warnings=%d, want %d\n%s", warnings, test.wantWarnings, output.String())
			}
			if len(signals) != 1 || signals[0] != 0 {
				t.Fatalf("signals=%v, want one signal-zero liveness probe", signals)
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(output.String(), notWant) {
					t.Fatalf("output unexpectedly contains %q:\n%s", notWant, output.String())
				}
			}
		})
	}
}

func TestMCPServeProcessesDoctorChecksUnreadableIdentityLiveness(t *testing.T) {
	runtime, fresh, _ := newMCPServeDoctorFixture(t)
	const currentUID = uint32(1200)
	tests := []struct {
		name         string
		pid          int
		commandErr   error
		signalErr    error
		wantSignals  int
		wantWarnings int
		want         []string
		notWant      []string
	}{
		{
			name:         "process exits after identity failure",
			pid:          50000557,
			signalErr:    syscall.ESRCH,
			wantSignals:  1,
			wantWarnings: 0,
			want:         []string{"doctor: mcp-serve clean checked=1"},
			notWant:      []string{"pid=50000557", "UNREAD"},
		},
		{
			name:         "identity liveness is indeterminate",
			pid:          50000558,
			signalErr:    syscall.EPERM,
			wantSignals:  1,
			wantWarnings: 1,
			want: []string{
				"doctor: mcp-serve UNREAD — pid=50000558  read process identity: status denied; " +
					"probe liveness with signal 0: operation not permitted",
			},
			notWant: []string{"clean"},
		},
		{
			name:         "both failed probes share one row",
			pid:          50000559,
			commandErr:   errors.New("command denied"),
			wantSignals:  1,
			wantWarnings: 1,
			want: []string{
				"doctor: mcp-serve UNREAD — pid=50000559  read process identity: status denied; " +
					"read command: command denied\n",
			},
			notWant: []string{"clean"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := &mcpServeIdentityTable{
				mcpServeProcTable: mcpServeProcTable{
					pids:       []int{test.pid},
					cmdlines:   map[int][]string{test.pid: {"pfm", "mcp", "serve"}},
					cmdlineErr: map[int]error{test.pid: test.commandErr},
					images:     map[int]gather.FileID{test.pid: fresh},
				},
				identityErr:  map[int]error{test.pid: errors.New("status denied")},
				commandReads: make(map[int]int),
			}
			var signals []syscall.Signal
			var output bytes.Buffer
			warnings := printMCPServeProcessesDoctorWithSignalerAndUID(
				&output,
				runtime,
				table,
				func(pid int, signal syscall.Signal) error {
					if pid != test.pid {
						t.Fatalf("signaled pid=%d, want %d", pid, test.pid)
					}
					signals = append(signals, signal)
					return test.signalErr
				},
				currentUID,
			)
			if warnings != test.wantWarnings {
				t.Fatalf("warnings=%d, want %d\n%s", warnings, test.wantWarnings, output.String())
			}
			if len(signals) != test.wantSignals {
				t.Fatalf("signals=%v, want %d signal-zero liveness probes", signals, test.wantSignals)
			}
			for _, signal := range signals {
				if signal != 0 {
					t.Fatalf("signal=%v, want signal zero", signal)
				}
			}
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(output.String(), notWant) {
					t.Fatalf("output unexpectedly contains %q:\n%s", notWant, output.String())
				}
			}
		})
	}
}

func TestMCPServeProcessesDoctorReportsUnreadableStates(t *testing.T) {
	runtime, _, replaced := newMCPServeDoctorFixture(t)
	tests := []struct {
		name  string
		table gather.ProcFS
		want  string
	}{
		{
			name: "one process image unreadable",
			table: mcpServeProcTable{
				pids:     []int{50000601},
				cmdlines: map[int][]string{50000601: {"pfm", "mcp", "serve", "--stdio"}},
				imageErr: map[int]error{50000601: errors.New("readlink denied")},
			},
			want: "doctor: mcp-serve UNREAD — pid=50000601  pfm mcp serve --stdio: readlink denied",
		},
		{
			name: "pid table unreadable",
			table: mcpServeProcTable{
				pidsErr: errors.New("process table denied"),
			},
			want: "doctor: mcp-serve UNREAD — list processes: process table denied",
		},
		{
			name: "image capability unavailable",
			table: mcpServeImagelessTable{ProcFS: mcpServeProcTable{
				pids:     []int{50000602},
				cmdlines: map[int][]string{50000602: {"pfm", "mcp", "serve"}},
				images:   map[int]gather.FileID{50000602: replaced},
			}},
			want: "doctor: mcp-serve UNREAD — this process table cannot read which file a process executes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			warnings := printMCPServeProcessesDoctorWithSignaler(
				&output,
				runtime,
				test.table,
				func(int, syscall.Signal) error { return nil },
			)
			if warnings != 1 {
				t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("output missing %q:\n%s", test.want, output.String())
			}
			if strings.Contains(output.String(), "clean") {
				t.Fatalf("unreadable scan printed a clean row:\n%s", output.String())
			}
		})
	}
}

func TestMCPServeProcessesDoctorUsesOneCommandSnapshot(t *testing.T) {
	runtime, _, replaced := newMCPServeDoctorFixture(t)
	table := &mcpServeChangingCmdlineTable{mcpServeProcTable: mcpServeProcTable{
		pids:       []int{50000603},
		cmdlines:   map[int][]string{50000603: {"pfm", "mcp", "serve", "--stdio"}},
		imageErr:   map[int]error{50000603: errors.New("image probe failed")},
		environErr: map[int]error{},
		images:     map[int]gather.FileID{50000603: replaced},
	}}
	var output bytes.Buffer
	warnings := printMCPServeProcessesDoctorWithSignaler(
		&output,
		runtime,
		table,
		func(int, syscall.Signal) error { return nil },
	)
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	if want := "doctor: mcp-serve UNREAD — pid=50000603  pfm mcp serve --stdio: image probe failed"; !strings.Contains(
		output.String(),
		want,
	) {
		t.Fatalf("output missing %q:\n%s", want, output.String())
	}
	if strings.Contains(output.String(), "clean") {
		t.Fatalf("unreadable server printed a clean row:\n%s", output.String())
	}
}

func TestMCPServeProcessesDoctorReportsMissingInstalledBinary(t *testing.T) {
	home := t.TempDir()
	runtime := config.Runtime{Paths: paths.Values{Home: home}}
	var output bytes.Buffer
	warnings := printMCPServeProcessesDoctorWithSignaler(
		&output,
		runtime,
		mcpServeProcTable{},
		func(int, syscall.Signal) error { return nil },
	)
	if warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	wantPath := filepath.Join(home, ".local", "bin", "pfm")
	if !strings.Contains(output.String(), "doctor: mcp-serve UNREAD — identify the installed binary "+wantPath) {
		t.Fatalf("output does not name the missing installed binary:\n%s", output.String())
	}
	if strings.Contains(output.String(), "clean") {
		t.Fatalf("missing installed binary printed a clean row:\n%s", output.String())
	}
}
