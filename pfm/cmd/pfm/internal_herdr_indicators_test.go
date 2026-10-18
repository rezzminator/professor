package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestHerdrIndicatorsIsTheVerbThePluginExecs pins the registry name to the
// verb the Herdr plugin manifest execs.
func TestHerdrIndicatorsIsTheVerbThePluginExecs(t *testing.T) {
	if !slices.Contains(internalSubcommands, herdr.IndicatorVerb) {
		t.Fatalf("internalSubcommands lacks %q, the verb the Herdr indicator plugin execs", herdr.IndicatorVerb)
	}
}

// This runs the installed plugin's exact argv through the compiled CLI. No
// injected provider: fleet enumeration, SeatInspector, config, clock and wire
// transport are real; only Herdr's socket and engine processes are fixtures.
func TestHerdrIndicatorsCompiledCLIShowsAndStopsTabPulse(t *testing.T) {
	root := testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	status, label := "working", "Chat"
	revision := uint64(1)
	engineAlive := true
	paneTokens, workspaceTokens := map[string]string{}, map[string]string{}
	renames := []string{}
	changed := make(chan string, 64)
	listed := make(chan struct{}, 64)
	done := make(chan struct{})
	const id = "6fafd8a7-5059-4288-bf58-23e60c5fdb88"
	// A live native seat is owned by its pfm seat-host supervisor beneath the
	// pane's shell; the proof finds it through shell_pid and the process table.
	const shellPID, supervisorPID = 900100, 900101
	if err := os.WriteFile(filepath.Join(values.ProcRoot, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFakeProcess(
		t,
		values.ProcRoot,
		fakeProcessSpec{pid: shellPID, parentPID: 1, comm: "zsh", cmdline: []string{"zsh"}},
	)
	writeFakeProcess(t, values.ProcRoot, fakeProcessSpec{
		pid:       supervisorPID,
		parentPID: shellPID,
		comm:      "pfm",
		cmdline:   []string{"/fixture/pfm", "internal", "seat-host", "seat1"},
	})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					var request struct {
						ID     string `json:"id"`
						Method string `json:"method"`
						Params struct {
							Label  string             `json:"label"`
							Tokens map[string]*string `json:"tokens"`
						} `json:"params"`
					}
					if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
						t.Error(err)
						return
					}
					mu.Lock()
					pane := map[string]any{
						"pane_id":      "w1:p1",
						"terminal_id":  "term1",
						"workspace_id": "w1",
						"tab_id":       "w1:t1",
						"focused":      true,
						"revision":     revision,
						"agent_status": status,
						"tokens":       paneTokens,
						"agent_session": map[string]string{
							"source": "pfm",
							"agent":  "claude",
							"kind":   "id",
							"value":  id,
						},
					}
					var result any
					switch request.Method {
					case "pane.list":
						result = map[string]any{"type": "pane_list", "panes": []any{pane}}
						select {
						case listed <- struct{}{}:
						default:
						}
					case "pane.get":
						result = map[string]any{"type": "pane_info", "pane": pane}
					case "workspace.list":
						result = map[string]any{
							"type": "workspace_list",
							"workspaces": []any{
								map[string]any{
									"workspace_id": "w1",
									"label":        "Workspace",
									"agent_status": status,
									"tokens":       workspaceTokens,
								},
							},
						}
					case "agent.get":
						result = map[string]any{"type": "agent_info", "agent": pane}
					case "pane.process_info":
						processes := []any{}
						if engineAlive {
							processes = append(
								processes,
								map[string]any{"pid": 42, "name": "claude", "argv0": "claude"},
							)
						}
						result = map[string]any{
							"type": "pane_process_info",
							"process_info": map[string]any{
								"pane_id":              "w1:p1",
								"shell_pid":            shellPID,
								"foreground_processes": processes,
							},
						}
					case "tab.get":
						result = map[string]any{
							"type": "tab_info",
							"tab": map[string]any{
								"tab_id":       "w1:t1",
								"workspace_id": "w1",
								"number":       1,
								"focused":      true,
								"pane_count":   1,
								"label":        label,
								"agent_status": status,
							},
						}
					case "tab.rename":
						label = request.Params.Label
						renames = append(renames, label)
						changed <- label
						result = map[string]string{"type": "ok"}
					case "pane.report_metadata", "workspace.report_metadata":
						tokens := paneTokens
						if request.Method == "workspace.report_metadata" {
							tokens = workspaceTokens
						}
						for key, value := range request.Params.Tokens {
							if value == nil {
								delete(tokens, key)
							} else {
								tokens[key] = *value
							}
						}
						result = map[string]string{"type": "ok"}
					default:
						t.Errorf("unexpected Herdr method %s", request.Method)
						result = map[string]string{"type": "ok"}
					}
					err := json.NewEncoder(conn).Encode(map[string]any{"id": request.ID, "ok": true, "result": result})
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	seat := fleetdb.Seat{
		SeatID:          "seat1",
		SessionID:       id,
		Engine:          "cc",
		Backend:         "herdr",
		HerdrSocket:     socket,
		HerdrPane:       "w1:p1",
		HerdrWorkspace:  "w1",
		HerdrTerminalID: "term1",
		Name:            "Chat",
		State:           "live",
	}
	if err := gather.StampHerdrHost(gather.NewProcFS(values.ProcRoot), &seat, supervisorPID); err != nil {
		t.Fatal(err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcript,
		[]byte(
			`{"type":"user","cwd":"/work/project","message":{"role":"user","content":"go"}}`+"\n"+`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ready"}]}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	appendTurn := func(record string) {
		t.Helper()
		file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := fmt.Fprintln(file, record)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatalf("append turn: %v %v", writeErr, closeErr)
		}
	}
	config := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := atomicfile.Write(config, []byte(`{"version":2,"chat":{"herdrTabStatus":"emoji"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, testPFMBinary, "internal", "herdr-indicators")
	command.Env = append(os.Environ(), "PFM_CONFIG="+config, "HERDR_SOCKET_PATH="+socket)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
			t.Error("indicator child failed to exit")
		}
	})
	await := func(want string) {
		t.Helper()
		for {
			select {
			case got := <-changed:
				if got == want {
					return
				}
			case <-ctx.Done():
				// Wait for the killed child so its output is complete and race-free.
				exit := <-waited
				waited <- exit
				t.Fatalf("wanted tab label %q; painter timed out (exit %v): %s", want, exit, output.String())
			}
		}
	}
	await("🟡 Chat")
	mu.Lock()
	status = "blocked"
	revision++
	mu.Unlock()
	seen := map[string]bool{}
	for !seen["🔴 Chat"] || !seen["⬜ Chat"] {
		select {
		case label := <-changed:
			seen[label] = true
		case <-ctx.Done():
			t.Fatal("focused blocked tab did not pulse in both phases")
		}
	}
	mu.Lock()
	status = "idle"
	revision++
	mu.Unlock()
	await("🟢 Chat")
	// Observe several real monitor polls, rather than sleeping and inferring.
	for i := 0; i < 12; i++ {
		select {
		case <-listed:
		case <-ctx.Done():
			t.Fatal("idle monitor stopped")
		}
	}
	mu.Lock()
	count := len(renames)
	mu.Unlock()
	for i := 0; i < 8; i++ {
		select {
		case <-listed:
		case <-ctx.Done():
			t.Fatal("idle monitor stopped")
		}
	}
	mu.Lock()
	if len(renames) != count || label != "🟢 Chat" {
		t.Errorf("idle self-triggered rename loop: %v", renames)
	}
	mu.Unlock()
	// A failed turn arrives only in the transcript, with no idle hook.
	appendTurn(
		`{"type":"assistant","isApiErrorMessage":true,"error":"server_error","message":{"role":"assistant","content":[{"type":"text","text":"API Error: overloaded"}]}}`,
	)
	await("🟥 Chat")
	// New work invalidates the old error; a failed tool is not a failed turn.
	appendTurn(`{"type":"user","message":{"role":"user","content":"try again"}}`)
	mu.Lock()
	status = "working"
	revision++
	mu.Unlock()
	await("🟡 Chat")
	appendTurn(
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","is_error":true,"content":"tool failed"}]}}`,
	)
	// Abrupt engine death has no SessionEnd or idle hook. Native process truth
	// changes while the reporter still says working and the pane identity stays.
	mu.Lock()
	engineAlive = false
	mu.Unlock()
	await("⬛ Chat")
	if err := atomicfile.Write(config, []byte(`{"version":2,"chat":{"herdrTabStatus":"off"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	await("Chat")
	select {
	case err := <-waited:
		// Put completion back so cleanup can account for the same child.
		waited <- nil
		if err != nil {
			t.Fatalf("indicator CLI: %v\n%s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("disabled painter did not exit")
	}
	if strings.Contains(output.String(), "error") {
		t.Fatalf("unexpected painter diagnostic: %s", output.String())
	}
	mu.Lock()
	t.Logf("compiled CLI tab.rename requests: %v", renames)
	mu.Unlock()
}
