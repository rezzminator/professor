package gather

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestDetectAgentsFindsClaudeDaemonJobs pins the daemon-job door. Claude Code
// runs a backgrounded chat in a pre-started spare whose argv names no session
// ("claude bg-spare --bg-spare <claim socket>"); the session it claimed is
// only in <config>/sessions/<pid>.json. Missing it made pfm treat a running
// chat as dormant and resume it in a second process on the same session.
// The record is believed only for the very process that wrote it, and only
// when it says "bg".
func TestDetectAgentsFindsClaudeDaemonJobs(t *testing.T) {
	const jobSession = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	const recycledSession = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
	const interactiveSession = "88888888-9999-4aaa-8bbb-cccccccccccc"
	const primaryJobSession = "99999999-aaaa-4bbb-8ccc-dddddddddddd"
	home := t.TempDir()
	configDir := filepath.Join(home, ".cc", "3")
	primaryDir := filepath.Join(home, ".claude")
	record := func(dir string, pid int, session, kind string, procStart uint64) {
		t.Helper()
		sessions := filepath.Join(dir, "sessions")
		if err := os.MkdirAll(sessions, 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"pid":` + strconv.Itoa(pid) + `,"sessionId":"` + session + `","cwd":"/work/app","procStart":"` +
			strconv.FormatUint(procStart, 10) + `","version":"2.1.275","kind":"` + kind + `","jobId":"66666666"}`
		if err := os.WriteFile(filepath.Join(sessions, strconv.Itoa(pid)+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spare := func(dir string, start uint64) fakeProcess {
		return fakeProcess{
			cmdline: []string{"claude", "bg-spare", "--bg-spare", "/tmp/cc-daemon/spare/c351c249.claim.sock"},
			environ: map[string]string{"CLAUDE_CONFIG_DIR": dir},
			stat:    ProcStat{ParentPID: 1, StartTime: start},
		}
	}
	record(configDir, 800, jobSession, "bg", 27840284)
	record(primaryDir, 801, primaryJobSession, "bg", 500)          // primary config dir: still a job
	record(configDir, 802, recycledSession, "bg", 111)             // pid recycled since the record was written
	record(configDir, 803, interactiveSession, "interactive", 222) // a pane chat: its crumb names it
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		800: spare(configDir, 27840284),
		801: spare(primaryDir, 500),
		802: spare(configDir, 999),
		803: spare(configDir, 222),
		804: spare(configDir, 333), // an unclaimed spare: no record at all
		805: spare(configDir, 444), // a record that exists but cannot be parsed
	}}
	corrupt := filepath.Join(configDir, "sessions", "805.json")
	if err := os.WriteFile(corrupt, []byte(`{"pid":805,"sessionId":`), 0o600); err != nil {
		t.Fatal(err)
	}

	agents, warnings, err := DetectAgents(proc, home, nil)
	if err != nil {
		t.Fatalf("DetectAgents() error = %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], corrupt) {
		t.Fatalf(
			"DetectAgents() warnings = %q, want one naming %s — "+
				"a record that failed to parse is a job the scan could not see, not an absent one",
			warnings, corrupt,
		)
	}
	if len(agents) != 2 {
		t.Fatalf("DetectAgents() = %#v, want exactly the two live daemon jobs", agents)
	}
	if agents[0].SessionID != jobSession || agents[0].PID != 800 ||
		agents[0].ConfigDir != configDir || agents[0].Socket != "" {
		t.Fatalf("DetectAgents()[0] = %#v, want pid 800 on %s with no pane", agents[0], configDir)
	}
	if agents[1].SessionID != primaryJobSession || agents[1].PID != 801 ||
		agents[1].ConfigDir != primaryDir {
		t.Fatalf("DetectAgents()[1] = %#v, want pid 801 on the primary config dir", agents[1])
	}
}
