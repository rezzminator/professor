package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
