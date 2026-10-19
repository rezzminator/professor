package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func readSubagentTrace(t *testing.T, home string) ([]subagentTraceRecord, string) {
	t.Helper()
	raw, err := os.ReadFile(paths.SubagentStatuslineTrace(home))
	if err != nil {
		t.Fatal(err)
	}
	var records []subagentTraceRecord
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		var record subagentTraceRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("trace is not JSON lines: %v", err)
		}
		records = append(records, record)
	}
	return records, string(raw)
}

func TestSubagentTraceIsOptInAndDoesNotChangeRows(t *testing.T) {
	const payload = `{"tasks":[{"id":"zero","type":"local_bash","status":"running",` +
		`"model":"gpt-6-sol","tokenCount":0,"contextWindowSize":0,` +
		`"label":"PRIVATE-LABEL","description":"PRIVATE-DESCRIPTION","cwd":"/PRIVATE-CWD"},` +
		`{"id":"absent","type":"local_bash","status":"running"}],` +
		`"transcript_path":"/PRIVATE-TRANSCRIPT","cwd":"/PRIVATE-SESSION-CWD"}`
	machine := paths.Values{Home: t.TempDir(), SIDDir: t.TempDir()}
	var baseline bytes.Buffer
	if code := ServeSubagents(strings.NewReader(payload), &baseline, &bytes.Buffer{}, machine, noEnv, ""); code != 0 {
		t.Fatalf("disabled trace exit = %d", code)
	}
	if _, err := os.Stat(filepath.Dir(paths.SubagentStatuslineTrace(machine.Home))); !os.IsNotExist(err) {
		t.Fatalf("disabled trace touched the log directory: %v", err)
	}
	for range 2 {
		var stdout, stderr bytes.Buffer
		env := &paths.MapEnv{Values: map[string]string{claudelaunch.EnvSubagentStatuslineTrace: "1"}}
		if code := ServeSubagents(strings.NewReader(payload), &stdout, &stderr, machine, env, ""); code != 0 {
			t.Fatalf("enabled trace exit = %d", code)
		}
		if stdout.String() != baseline.String() || stderr.Len() != 0 {
			t.Fatalf("trace changed rows or warned: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
	records, raw := readSubagentTrace(t, machine.Home)
	if len(records) != 14 {
		t.Fatalf("two appends: got %d records, want 14", len(records))
	}
	for run := range 2 {
		got := records[run*7 : (run+1)*7]
		if got[0].Event != "start" || got[1].Event != "input" || !got[1].Valid || got[1].Count != 2 {
			t.Fatalf("input was not captured before rendering: %+v", got)
		}
		zero, absent := got[2].Task, got[3].Task
		if zero == nil || zero.ID != "zero" || zero.Model != "gpt-6-sol" || zero.TokenCount == nil ||
			*zero.TokenCount != 0 || zero.ContextWindowSize == nil || *zero.ContextWindowSize != 0 {
			t.Fatalf("zero-valued counters were lost: %+v", zero)
		}
		if absent == nil || absent.ID != "absent" || absent.TokenCount != nil || absent.ContextWindowSize != nil {
			t.Fatalf("absent counters were mistaken for zero: %+v", absent)
		}
		if got[4].Event != "row" || got[4].Task.ID != "zero" || got[5].Task.ID != "absent" ||
			got[6].Event != "result" || got[6].Stage != "done" || !got[6].Valid || got[6].Count != 2 || got[6].ElapsedMS < 0 {
			t.Fatalf("emitted row IDs and result missing: %+v", got)
		}
		for _, record := range got {
			if record.PID <= 0 || record.Time.IsZero() {
				t.Fatalf("trace cannot correlate concurrent calls: %+v", record)
			}
		}
	}
	if strings.Contains(raw, "PRIVATE-") || strings.Contains(raw, "content") || strings.Contains(raw, "label") {
		t.Fatalf("trace retained private payload or output fields: %s", raw)
	}
}

func TestSubagentTraceRecordsFaultsWithoutCopyingInput(t *testing.T) {
	machine := paths.Values{Home: t.TempDir(), SIDDir: t.TempDir()}
	env := &paths.MapEnv{Values: map[string]string{claudelaunch.EnvSubagentStatuslineTrace: "1"}}
	var stdout, stderr bytes.Buffer
	ServeSubagents(strings.NewReader(`{"tasks":[{"id":"a"}],"label":"PRIVATE-TORN`),
		&stdout, &stderr, machine, env, "")
	records, raw := readSubagentTrace(t, machine.Home)
	if len(records) != 4 || records[1].Valid || records[2].Event != "row" || records[2].Task.ID != "a" ||
		records[3].Stage != "render" || records[3].Valid || records[3].Count != 1 {
		t.Fatalf("fault was not distinguished from a successful empty panel: %+v", records)
	}
	if strings.Contains(raw, "PRIVATE-") {
		t.Fatalf("malformed input leaked into trace: %s", raw)
	}
}

func TestSubagentTraceRotatesAndBoundsEachRecord(t *testing.T) {
	machine := paths.Values{Home: t.TempDir(), SIDDir: t.TempDir()}
	path := paths.SubagentStatuslineTrace(machine.Home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const capBytes = 4 << 20
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), capBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	env := &paths.MapEnv{Values: map[string]string{claudelaunch.EnvSubagentStatuslineTrace: "1"}}
	payload := `{"tasks":[{"id":"a","model":"` + strings.Repeat("m", capBytes+1) + `"}]}`
	ServeSubagents(strings.NewReader(payload), &bytes.Buffer{}, &bytes.Buffer{}, machine, env, "")
	for _, generation := range []string{path, path + ".1"} {
		info, err := os.Stat(generation)
		if err != nil || info.Size() > capBytes || info.Mode().Perm() != 0o600 {
			t.Fatalf("trace generation escaped its size/privacy cap: %s info=%v err=%v", generation, info, err)
		}
	}
	_, raw := readSubagentTrace(t, machine.Home)
	if len(raw) > 4096 || !strings.Contains(raw, strings.Repeat("m", 128)+"...") {
		t.Fatalf("one input field escaped its record bound: trace bytes=%d", len(raw))
	}
}

func TestSubagentTraceFailureDoesNotSuppressRows(t *testing.T) {
	machine := paths.Values{Home: t.TempDir(), SIDDir: t.TempDir()}
	// A regular file where the log directory should be prevents trace setup.
	if err := os.WriteFile(filepath.Join(machine.Home, ".local"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := &paths.MapEnv{Values: map[string]string{claudelaunch.EnvSubagentStatuslineTrace: "1"}}
	var stdout, stderr bytes.Buffer
	ServeSubagents(strings.NewReader(`{"tasks":[{"id":"a"}]}`), &stdout, &stderr, machine, env, "")
	if !strings.Contains(stdout.String(), `"id":"a"`) || !strings.Contains(stderr.String(), "open trace") {
		t.Fatalf("trace failure hid the row or cause: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
