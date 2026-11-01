//go:build linux

package hookentry

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// This supervisor fixture supplies native identity, enumeration and shell proof.
// The older bridge fixture has no native pane.get contract.
func newSeatHostHerdr(t *testing.T, roots ...string) *fakeHerdr {
	t.Helper()
	server := &fakeHerdr{path: filepath.Join(testjail.ShortRoot(t), "h.sock")}
	listener, err := net.Listen("unix", server.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		proofReads := 0
		currentPane := "w6:p3"
		text := "❯ "
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string                     `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				var pane string
				_ = json.Unmarshal(req.Params["pane_id"], &pane)
				line, _ := json.Marshal(req)
				server.mu.Lock()
				server.lines = append(server.lines, string(line))
				server.mu.Unlock()
				result := map[string]any{"type": "ok"}
				if len(roots) > 0 {
					if raw, err := os.ReadFile(filepath.Join(roots[0], "canonical-pane")); err == nil {
						currentPane = string(raw)
					} else if !os.IsNotExist(err) {
						t.Error(err)
					}
				}
				switch req.Method {
				case "ping":
					result = map[string]any{"type": "pong", "version": "fixture", "protocol": 22}
				case "pane.get":
					result = testjail.HerdrPaneInfo(pane)
					result["pane"].(map[string]any)["terminal_id"] = "stable-terminal"
				case "pane.list":
					proofReads++
					if len(roots) > 0 {
						if _, err := os.Stat(filepath.Join(roots[0], "move-on-proof")); err == nil && proofReads >= 2 {
							currentPane = "w9:p2"
						}
					}
					pane := testjail.HerdrPaneInfo(currentPane)["pane"].(map[string]any)
					pane["terminal_id"] = "stable-terminal"
					result = map[string]any{"type": "pane_list", "panes": []any{pane}}
				case "pane.process_info":
					shell := 0
					proc := gather.NewProcFS("/proc")
					pids, err := proc.PIDs()
					if err != nil {
						t.Error(err)
					}
					for _, pid := range pids {
						argv, err := proc.Cmdline(pid)
						if err != nil {
							continue
						}
						host := len(argv) == 4 && argv[1] == "internal" && argv[2] == "seat-host"
						launch := len(argv) == 2 && argv[1] == "-test.run=^TestSeatHostPTY$"
						if !host && !launch {
							continue
						}
						env, err := proc.Environ(pid)
						if err != nil {
							continue
						}
						if len(roots) > 0 && env["PFM_S3_ROOT"] != roots[0] {
							continue
						}
						shell = pid
						break
					}
					result = map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id": pane, "shell_pid": shell,
							"foreground_processes": []any{map[string]any{"pid": shell}},
						},
					}
				}
				if len(roots) > 1 && roots[1] == "reload-failure-locked" {
					switch req.Method {
					case "pane.process_info":
						raw, err := os.ReadFile(filepath.Join(roots[0], "engine-report"))
						if err == nil {
							var report seatFixtureReport
							if err := json.Unmarshal(raw, &report); err != nil {
								t.Error(err)
							}
							result["process_info"].(map[string]any)["foreground_processes"] = []any{
								map[string]any{
									"pid":   report.PID,
									"name":  "claude",
									"argv0": "claude",
									"argv":  []string{"claude"},
								},
							}
						} else if !os.IsNotExist(err) {
							t.Error(err)
						}
					case "agent.get":
						result = map[string]any{
							"type": "agent_info",
							"agent": map[string]any{
								"pane_id":          currentPane,
								"terminal_id":      "stable-terminal",
								"agent_status":     "idle",
								"workspace_id":     strings.Split(currentPane, ":")[0],
								"tab_id":           strings.Split(currentPane, ":")[0] + ":t1",
								"focused":          true,
								"state_change_seq": 0,
								"revision":         1,
							},
						}
					case "pane.read":
						result = map[string]any{
							"type": "pane_read",
							"read": map[string]any{
								"pane_id":      currentPane,
								"workspace_id": "w6",
								"text":         text,
								"truncated":    false,
								"format":       "ansi",
							},
						}
					case "pane.send_text":
						var value string
						if err := json.Unmarshal(req.Params["text"], &value); err != nil {
							t.Error(err)
						}
						text += value
					case "pane.send_keys":
						var keys []string
						if err := json.Unmarshal(req.Params["keys"], &keys); err != nil {
							t.Error(err)
						}
						for _, key := range keys {
							if key == "Enter" {
								if err := os.WriteFile(
									filepath.Join(roots[0], "engine-report-release"),
									[]byte("ready"),
									0o600,
								); err != nil {
									t.Error(err)
								}
							}
						}
					}
				}

				_ = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result})
			}()
		}
	}()
	return server
}
