package statusline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

type keepwarmProcFixture struct {
	gather.ProcFS
	argv        []string
	argvErr     error
	zombie      bool
	identityErr error
	born        int64
	birthErr    error
}

func (proc keepwarmProcFixture) Cmdline(int) ([]string, error) { return proc.argv, proc.argvErr }
func (proc keepwarmProcFixture) ProcessIdentity(int) (gather.ProcessIdentity, error) {
	return gather.ProcessIdentity{Zombie: proc.zombie}, proc.identityErr
}
func (proc keepwarmProcFixture) Birth(int) (int64, error) { return proc.born, proc.birthErr }

// Probe failures differ from negative liveness: unreadable is ERR, never ?.
func TestCacheKeepwarmProbe(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		signal, argvErr, identityErr, birthErr error
		born                                   int64
		argv                                   []string
		zombie, live, wantErr, exitDuringRead  bool
	}{
		{name: "Claude", argv: []string{"claude"}, live: true},
		{name: "native version", argv: []string{"/state/versions/2.1.302"}, live: true},
		{name: "other program", argv: []string{"bash"}},
		{name: "gone", signal: syscall.ESRCH},
		{name: "another user", signal: syscall.EPERM},
		{name: "signal failure", signal: syscall.EIO, wantErr: true},
		{name: "zombie", argv: []string{"claude"}, zombie: true},
		{name: "argv unreadable", argvErr: syscall.EIO, wantErr: true},
		{name: "identity unreadable", identityErr: syscall.EIO, wantErr: true},
		{name: "exit race", argvErr: os.ErrNotExist, exitDuringRead: true},
		{name: "writer within the birth slack", argv: []string{"claude"}, born: 1001, live: true},
		{name: "pid reused by a newer Claude", argv: []string{"claude"}, born: 1003},
		{name: "start time unreadable", argv: []string{"claude"}, birthErr: syscall.EIO, wantErr: true},
		{name: "exit during start time read", argv: []string{"claude"}, birthErr: os.ErrNotExist, exitDuringRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := keepwarmProcFixture{
				argv:        tc.argv,
				argvErr:     tc.argvErr,
				zombie:      tc.zombie,
				identityErr: tc.identityErr,
				born:        tc.born,
				birthErr:    tc.birthErr,
			}
			calls := 0
			live, err := keepwarmProbe(proc, func(int) error {
				calls++
				if tc.exitDuringRead && calls > 1 {
					return syscall.ESRCH
				}
				return tc.signal
			})(42, 1000*1000)
			if live != tc.live || (err != nil) != tc.wantErr {
				t.Fatalf("live=%t err=%v, want live=%t error=%t", live, err, tc.live, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "42") {
				t.Fatalf("error lacks pid context: %v", err)
			}
		})
	}
}

// Exercise the shipped JSON boundary and the real cache segment, not a copied renderer.
func TestCacheKeepwarmShieldStatesAndFailures(t *testing.T) {
	now := time.Unix(1800000000, 0)
	cases := []struct {
		name, state, want, color, bad                    string
		change                                           func(map[string]any)
		absent, directory, fifo, oversized, otherProcess bool
		reborn, relativeDir                              bool
	}{
		{name: "warming", state: "warming", want: "🛡 waiting", color: cWarn},
		{name: "on remains valid while quiet", state: "on", want: "🛡 on", color: cMuted},
		{
			name:   "stopped",
			state:  "stopped",
			want:   "🛡 stopped",
			color:  cBad,
			change: func(r map[string]any) { r["pokes"] = map[string]any{"count": 4, "max": 4}; r["reason"] = "poke-limit" },
		},
		{
			name:   "off",
			state:  "off",
			want:   "🛡 off",
			color:  cMuted,
			change: func(r map[string]any) { r["enabled"] = false; r["reason"] = "off" },
		},
		{
			name:   "unknown optional measurements",
			state:  "warming",
			want:   "🛡 waiting",
			color:  cWarn,
			change: func(r map[string]any) { r["pokes"] = nil; r["nextAt"] = nil; r["protectingTokensEq"] = nil },
		},
		{name: "missing", absent: true},
		{name: "no session", absent: true, bad: "no-session"},
		{
			name:   "dead pid: as without the plugin",
			state:  "warming",
			change: func(r map[string]any) { r["pid"] = 2147483647 },
		},
		{name: "pid recycled into another program", state: "warming", otherProcess: true},
		{name: "pid recycled into a newer Claude", state: "warming", reborn: true},
		{name: "relative state dir", state: "on", want: "🛡 ERR", color: cBad, relativeDir: true},
		{name: "version", state: "on", want: "🛡 ERR", color: cBad, change: func(r map[string]any) { r["v"] = 2 }},
		{name: "unknown state", state: "mystery", want: "🛡 ERR", color: cBad},
		{name: "malformed", bad: "{", want: "🛡 ERR", color: cBad},
		{name: "directory", directory: true, want: "🛡 ERR", color: cBad},
		{name: "pipe", fifo: true, want: "🛡 ERR", color: cBad},
		{name: "oversize", oversized: true, want: "🛡 ERR", color: cBad},
		{
			name:   "session mismatch",
			state:  "on",
			want:   "🛡 ERR",
			color:  cBad,
			change: func(r map[string]any) { r["sessionId"] = "other" },
		},
		{name: "invalid pid", state: "on", want: "🛡 ERR", color: cBad, change: func(r map[string]any) { r["pid"] = 0 }},
		{
			name:   "fractional pid",
			state:  "on",
			want:   "🛡 ERR",
			color:  cBad,
			change: func(r map[string]any) { r["pid"] = 1.5 },
		},
		{
			name:   "invalid source",
			state:  "on",
			want:   "🛡 ERR",
			color:  cBad,
			change: func(r map[string]any) { r["source"] = "unknown" },
		},
		{
			name:   "missing enabled",
			state:  "on",
			want:   "🛡 ERR",
			color:  cBad,
			change: func(r map[string]any) { delete(r, "enabled") },
		},
		{
			name:   "negative measurement",
			state:  "warming",
			want:   "🛡 ERR",
			color:  cBad,
			change: func(r map[string]any) { r["protectingTokensEq"] = -1 },
		},
		{
			name:   "bad counters",
			state:  "warming",
			want:   "🛡 ERR",
			color:  cBad,
			change: func(r map[string]any) { r["pokes"] = map[string]any{"count": 5, "max": 4} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "state")
			path := filepath.Join(dir, "S.json")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			proc := filepath.Join(home, "proc")
			argv := []byte("claude\x00")
			if tc.otherProcess {
				argv = []byte("unrelated-server\x00")
			}
			procDir := filepath.Join(proc, fmt.Sprint(os.Getpid()))
			if err := os.MkdirAll(procDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(procDir, "cmdline"), argv, 0o600); err != nil {
				t.Fatal(err)
			}
			btime := int64(0)
			if tc.reborn {
				btime = now.Unix()
			}
			bootStat := []byte(fmt.Sprintf("cpu 0\nbtime %d\n", btime))
			if err := os.WriteFile(filepath.Join(proc, "stat"), bootStat, 0o600); err != nil {
				t.Fatal(err)
			}
			stat := fmt.Sprintf("%d (claude) S%s\n", os.Getpid(), strings.Repeat(" 0", 25))
			if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte(stat), 0o600); err != nil {
				t.Fatal(err)
			}
			record := map[string]any{
				"v":                  1,
				"sessionId":          "S",
				"pid":                os.Getpid(),
				"updatedAt":          1,
				"enabled":            true,
				"source":             "launch",
				"state":              tc.state,
				"reason":             "idle",
				"pokes":              map[string]any{"count": 2, "max": 4},
				"nextAt":             now.Add(3*time.Minute + 59*time.Second).UnixMilli(),
				"protectingTokensEq": 328600,
			}
			if tc.state == "warming" {
				record["reason"] = "eligible"
			}
			if tc.change != nil {
				tc.change(record)
			}
			body, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.absent:
			case tc.directory:
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case tc.fifo:
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				if tc.bad != "" {
					body = []byte(tc.bad)
				}
				if tc.oversized {
					body = []byte(strings.Repeat(" ", 65537))
				}
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var warn bytes.Buffer
			stateDir := dir
			if tc.relativeDir {
				stateDir = "relative/state"
			}
			runtime := Runtime{
				Home:     home,
				ProcRoot: proc,
				Env:      map[string]string{"CACHE_KEEPWARM_STATE_DIR": stateDir},
				Warn:     &warn,
			}
			expires := now.Add(5 * time.Minute).Unix()
			session := "S"
			if tc.bad == "no-session" {
				session = ""
			}
			got := cacheWindowSegment(runtime, now, "", 94, &promptCache{TTL: "5m", ExpiresAt: &expires}, session)
			plain := stripANSICodes(got)
			if !strings.HasPrefix(plain, stripANSICodes(sep)+"💾5m✓5m:0s 94%") {
				t.Fatalf("cache prefix lost: %q", plain)
			}
			if tc.want == "" {
				if strings.Contains(plain, "🛡") {
					t.Fatalf("unexpected shield: %q", plain)
				}
			} else if !strings.HasSuffix(got, tc.color+tc.want+reset) {
				t.Fatalf("segment = %q, want suffix %q", got, tc.color+tc.want+reset)
			}
			lines := strings.Split(strings.TrimSpace(warn.String()), "\n")
			switch {
			case tc.relativeDir:
				if len(lines) != 1 || !strings.Contains(lines[0], `"relative/state" is not absolute`) ||
					strings.Contains(lines[0], "state  (") {
					t.Fatalf("want one warning naming the unresolved directory, got %q", warn.String())
				}
			case tc.want == "🛡 ERR":
				if len(lines) != 1 || !strings.Contains(lines[0], path) || !strings.Contains(lines[0], ": ") {
					t.Fatalf("want one warning with path/cause, got %q", warn.String())
				}
			case warn.Len() != 0:
				t.Fatalf("unexpected warning %q", warn.String())
			}
		})
	}
}
