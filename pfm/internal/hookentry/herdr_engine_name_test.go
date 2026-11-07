package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestClearAndRenameCarryNativeHerdrName(t *testing.T) {
	for _, event := range []string{"clear", "rename"} {
		t.Run(event, func(t *testing.T) {
			server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
			root := t.TempDir()
			t.Setenv(paths.EnvHome, root)
			t.Setenv("HOME", root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "state.db"))
			t.Setenv(paths.EnvCacheDB, filepath.Join(root, "cache.db"))
			t.Setenv("TMUX", "")
			for key, value := range herdrPaneEnv(server, "w6:p3") {
				t.Setenv(key, value)
			}
			values := paths.Values{Home: root, StateDB: filepath.Join(root, "state.db")}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = db.Close() }()
			const id = "22222222-2222-4222-8222-222222222222"
			seat := fleetdb.Seat{
				SeatID:      "native-seat",
				SessionID:   id,
				Engine:      "cc",
				Backend:     "herdr",
				State:       "live",
				Name:        "Desk",
				HerdrSocket: server.path,
				HerdrPane:   "w6:p3",
			}
			stampNativeReporterSeat(t, &seat)
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			title := "Renamed Desk"
			source := "startup"
			if event == "clear" {
				fresh, err := naming.ClearSuccessorName("Desk", id)
				if err != nil {
					t.Fatal(err)
				}
				title = fresh
				source = "clear"
			}
			path := filepath.Join(root, "successor.jsonl")
			rawTitle := title
			if event == "clear" {
				rawTitle = "Desk"
			}
			body, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": rawTitle})
			if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(
				map[string]string{
					"hook_event_name": "SessionStart",
					"session_id":      id,
					"source":          source,
					"transcript_path": path,
				},
			)
			var stderr bytes.Buffer
			var code int
			var out bytes.Buffer
			if event == "clear" {
				runtime, err := config.LoadRuntime("")
				if err != nil {
					t.Fatal(err)
				}
				code = ClearName(nil, strings.NewReader(string(payload)), &out, &stderr, runtime)
			} else {
				code = reportToHerdr([]string{"start"}, strings.NewReader(string(payload)), &stderr, realHerdrDeps())
			}
			if code != 0 || stderr.Len() != 0 || out.Len() != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &stderr)
			}
			current, _, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || current.Name != title {
				t.Fatalf("native name=%q err=%v want %q; calls=%v", current.Name, err, title, server.requests())
			}
			methods := map[string]bool{}
			for _, line := range server.requests() {
				var req struct {
					Method string `json:"method"`
					Params struct {
						Label string `json:"label"`
					} `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					t.Fatal(err)
				}
				methods[req.Method] = true
				if (req.Method == "pane.rename" || req.Method == "tab.rename") && req.Params.Label != title {
					t.Fatalf("Herdr %s received %q, want fresh title %q", req.Method, req.Params.Label, title)
				}
			}
			for _, method := range []string{"pane.rename", "agent.rename", "tab.rename"} {
				if !methods[method] {
					t.Fatalf("Herdr missed %s: %v", method, server.requests())
				}
			}
		})
	}
}

func TestEngineNameReadsTitleAtPromptNotEveryTool(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "absent.db")}
	path := filepath.Join(root, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"custom-title","customTitle":"Desk"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reads := 0
	restore := transcript.ObserveWholeReads(func(string) { reads++ })
	defer restore()
	reporter := herdrReporter{
		options: herdrOptions{engine: engine.Claude, event: herdr.EventWorking},
		deps:    herdrDeps{seatPaths: &values},
	}
	payload := herdrPayload{SessionID: "22222222-2222-4222-8222-222222222222", TranscriptPath: path}
	for _, event := range []string{"PostToolUse", "PostToolUseFailure", "PostToolUse"} {
		payload.HookEventName = event
		name, err := reporter.engineName(context.Background(), payload)
		if err != nil || name != "" {
			t.Fatalf("tool title sync name=%q err=%v", name, err)
		}
	}
	payload.HookEventName = "UserPromptSubmit"
	name, err := reporter.engineName(context.Background(), payload)
	if err != nil || name != "Desk" || reads != 1 {
		t.Fatalf("prompt/tool title reads=%d name=%q err=%v", reads, name, err)
	}
}

func TestClearTmuxNameUsesExistingHerdrWindowHook(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "state.db")}
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { _ = state.Close() }()
	const id = "22222222-2222-4222-8222-222222222222"
	fresh, err := naming.ClearSuccessorName("Desk", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.PutClearName(ctx, id, naming.ClearName{Inherited: "Desk", Name: fresh}, 1); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	reporter := herdrReporter{
		options:  herdrOptions{engine: engine.Claude, event: herdr.EventStart, socket: "fixture"},
		tmuxPane: "%1",
		deps: herdrDeps{seatPaths: &values, tmux: func(_ context.Context, _ string, args ...string) (string, error) {
			calls = append(calls, args)
			if args[0] == "show-options" {
				return "Desk", nil
			}
			return "", nil
		}},
	}
	if err := reporter.syncTmuxEngineName(ctx, herdrPayload{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"show-options", "-qv", "-t", "%1", herdr.ChatNameOption}, gather.ChatRenameCommand("%1", fresh)}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("name/window hook commands=%v want %v", calls, want)
	}
}

func TestReservedSeatLabel(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "state.db")}
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { _ = state.Close() }()
	const id = "22222222-2222-4222-8222-222222222222"
	fresh, err := naming.ClearSuccessorName("Desk", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.PutClearName(ctx, id, naming.ClearName{Inherited: "Desk", Name: fresh}, 1); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "transcript.jsonl")
	for _, label := range []string{"seat:child", "SeAt:child"} {
		body, err := json.Marshal(map[string]string{"type": "custom-title", "customTitle": label})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		reporter := herdrReporter{
			options: herdrOptions{engine: engine.Claude, event: herdr.EventStart},
			deps:    herdrDeps{seatPaths: &values},
			stderr:  &stderr,
		}
		name, err := reporter.engineName(ctx, herdrPayload{SessionID: id, TranscriptPath: path})
		wantWarning := `pfm internal herdr-state start: engine title not used: label "` + label +
			`" is reserved for durable seat handles` + "\n"
		if err != nil || name != fresh || stderr.String() != wantWarning {
			t.Errorf("reserved engine title %q: name=%q err=%v stderr=%q, want %q and %q",
				label, name, err, stderr.String(), fresh, wantWarning)
		}
	}
}

// nativeTitleRig is a proved native Claude seat "Desk" in Herdr pane w6:p3
// whose transcript titles the chat; prompt fires its UserPromptSubmit hook.
type nativeTitleRig struct {
	server  *fakeHerdr
	db      *fleetdb.Store
	seat    fleetdb.Seat
	lockDir string
	prompt  func() herdrRun
}

func newNativeTitleRig(t *testing.T, title string, beforeSend ...func()) nativeTitleRig {
	t.Helper()
	server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal", beforeSend...)
	root := t.TempDir()
	t.Setenv(paths.EnvHome, root)
	t.Setenv("HOME", root)
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "state.db"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "cache.db"))
	t.Setenv("TMUX", "")
	for key, value := range herdrPaneEnv(server, "w6:p3") {
		t.Setenv(key, value)
	}
	values := paths.Values{Home: root, StateDB: filepath.Join(root, "state.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	t.Cleanup(func() { _ = db.Close() })
	const id = "22222222-2222-4222-8222-222222222222"
	seat := fleetdb.Seat{
		SeatID: "native-seat", SessionID: id, Engine: "cc", Backend: "herdr", State: "live", Name: "Desk",
		HerdrSocket: server.path, HerdrPane: "w6:p3",
	}
	stampNativeReporterSeat(t, &seat)
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "transcript.jsonl")
	body, _ := json.Marshal(map[string]string{"type": "custom-title", "customTitle": title})
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{
		"hook_event_name": "UserPromptSubmit", "session_id": id, "transcript_path": path, "prompt": "go on",
	})
	prompt := func() herdrRun {
		var stderr bytes.Buffer
		deps := realHerdrDeps()
		deps.spawn = nil // the transcript watch is herdr_watch_test.go's
		rc := reportToHerdr([]string{"working"}, bytes.NewReader(payload), &stderr, deps)
		return herdrRun{rc: rc, stderr: stderr.String()}
	}
	return nativeTitleRig{server: server, db: db, seat: seat, lockDir: paths.HerdrLockDir(root), prompt: prompt}
}

// bridgeHeld reports whether another open file holds the host-wide Herdr
// bridge lock right now.
func bridgeHeld(t *testing.T, lockDir string) bool {
	dir := filepath.Join(lockDir, "pfm-herdr-bridge")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Error(err)
		return false
	}
	file, err := os.OpenFile(filepath.Join(dir, "transaction.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Error(err)
		return false
	}
	defer func() { _ = file.Close() }()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true
	}
	if err != nil {
		t.Error(err)
		return false
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func requestMethod(t *testing.T, line string) string {
	t.Helper()
	var request struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal([]byte(line), &request); err != nil {
		t.Fatal(err)
	}
	return request.Method
}

// The engine-name sync of a native hook reads the whole transcript, opens the
// state database and asks Herdr to rename pane, agent and tab: none of it is
// the pane's state write, so it runs once the host-wide bridge lock is free,
// as the tmux transport's sync does, while the state report stays under it.
func TestNativeTitleSyncRunsOutsideTheBridgeLock(t *testing.T) {
	var mu sync.Mutex
	var held []bool
	lockDir := ""
	probe := func() {
		mu.Lock()
		dir := lockDir
		mu.Unlock()
		busy := bridgeHeld(t, dir)
		mu.Lock()
		held = append(held, busy)
		mu.Unlock()
	}
	rig := newNativeTitleRig(t, "Renamed Desk", probe)
	mu.Lock()
	lockDir = rig.lockDir
	mu.Unlock()
	if got := rig.prompt(); got.rc != 0 || got.stderr != "" {
		t.Fatalf("rc=%d stderr=%s", got.rc, got.stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	renamed, reported := false, false
	for i, line := range rig.server.requests() {
		switch method := requestMethod(t, line); method {
		case "pane.rename", "agent.rename", "tab.rename":
			renamed = true
			if held[i] {
				t.Errorf("%s ran under the host-wide bridge lock", method)
			}
		case "pane.report_agent":
			reported = true
			if !held[i] {
				t.Errorf("the state report ran outside the bridge lock")
			}
		}
	}
	if !renamed || !reported {
		t.Fatalf("renamed=%v reported=%v, want both; requests=%v", renamed, reported, rig.server.requests())
	}
}

// A pane Herdr tracks no agent in answers agent.rename with agent_not_found:
// the pane and its tab carry the title and the seat keeps it, so the next
// prompt neither asks Herdr again nor logs the same refusal.
func TestNativeTitleSyncNamesAPaneWithoutAnAgentOnce(t *testing.T) {
	rig := newNativeTitleRig(t, "Renamed Desk")
	rig.server.mu.Lock()
	rig.server.refuse = map[string]string{"agent.rename": "agent_not_found"}
	rig.server.mu.Unlock()
	for turn := 1; turn <= 2; turn++ {
		if got := rig.prompt(); got.rc != 0 || got.stderr != "" {
			t.Errorf("prompt %d: rc=%d stderr=%s", turn, got.rc, got.stderr)
		}
	}
	asked := 0
	for _, line := range rig.server.requests() {
		if requestMethod(t, line) == "agent.rename" {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("agent.rename asked %d times over two prompts, want once", asked)
	}
	current, _, err := rig.db.Seat(context.Background(), rig.seat.SeatID)
	if err != nil || current.Name != "Renamed Desk" {
		t.Errorf("seat name=%q err=%v, want the pane's new title kept", current.Name, err)
	}
}
