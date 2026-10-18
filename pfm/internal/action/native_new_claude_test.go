package action

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func mustProfessorPromptPath(t *testing.T, home string) string {
	t.Helper()
	path, err := ProfessorPromptPath(home)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// stageProfessorPrompt writes a prompt into a private fixture clone.
func stageProfessorPrompt(t *testing.T, home string) string {
	t.Helper()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	path := mustProfessorPromptPath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("stage professor prompt dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("professor prompt\n"), 0o644); err != nil {
		t.Fatalf("stage professor prompt file: %v", err)
	}
	return path
}

func TestNewClaudeUsesNativeConfiguredSpawn(t *testing.T) {
	home := t.TempDir()
	stageProfessorPrompt(t, home)
	machine := configuredMachinePolicy(home)
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
	prompt := "fresh prompt with '$HOME' and $(touch nope)"
	request := Request{
		Row: compose.Row{
			Kind: compose.NewClaude,
			CWD:  "/work/project with spaces",
		},
		PrimaryAccount: 42,
		Cache1H:        false,
		Bunker:         true,
		Home:           home,
		FreshSocket:    "cc-native-42",
		Config:         machine,
		Prompt:         prompt,
	}
	plan, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CLAUDE_CONFIG_DIR=" + Quote(machine.Accounts[0].ConfigDir),
		Quote(machine.Claude.Binary),
		Quote(prompt),
	} {
		if !strings.Contains(plan.Run, want) {
			t.Fatalf("native fresh run %q lacks %q", plan.Run, want)
		}
	}
	parsed := parsedShell(t, plan.Run)
	if parsed.SessionID == "" || parsed.PromptFile != mustProfessorPromptPath(t, home) ||
		launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "5m" || plan.Record == nil ||
		plan.Record.SessionID != parsed.SessionID || plan.Record.Account != 42 {
		t.Fatalf("fresh launch id=%q prompt=%q record=%#v", parsed.SessionID, parsed.PromptFile, plan.Record)
	}
	if strings.Contains(plan.Run, "skip-permissions") {
		t.Fatalf("prompted account received autonomy bypass: %q", plan.Run)
	}
	if got, want := plan.Line, attachLine(request.FreshSocket, request.FreshSocket, true); got != want {
		t.Fatalf("native fresh line = %q, want bunker attach line %q", got, want)
	}
	if plan.ChatServer == nil || plan.ChatServer.Run != plan.Run || plan.ChatServer.CWD != request.Row.CWD {
		t.Fatalf("native fresh server = %#v, want the plan's run in the row's cwd", plan.ChatServer)
	}
}

// TestNewClaudeNativeSpawnOmitsMissingSystemPromptFile is F3/F4's regression
// test: a fresh account chose SystemPromptProfessor, but the home has no
// source-repo marker (a brand-new machine). The
// door must degrade to the lean fallback rather than pass claude a
// --system-prompt-file flag pointing at nothing, which would brick the
// launch instead of merely losing the extra prompt material — the fail-open
// contract promptFile's doc comment states.
func TestNewClaudeNativeSpawnOmitsMissingSystemPromptFile(t *testing.T) {
	home := t.TempDir()
	// Deliberately leave this home without a source-repo marker.
	machine := configuredMachinePolicy(home)
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
	request := Request{
		Row: compose.Row{
			Kind: compose.NewClaude,
			CWD:  "/work/project with spaces",
		},
		PrimaryAccount: 42,
		Home:           home,
		FreshSocket:    "cc-native-42",
		Config:         machine,
	}
	plan, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Run, "--system-prompt-file") {
		t.Fatalf("native fresh run carried --system-prompt-file for a missing composed prompt: %q", plan.Run)
	}
}

func TestNewClaudeNativeSpawnPreservesBypassLeanAndCachePolicy(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.PermissionMode = pfmconfig.PermissionBypass
	machine.Claude.SystemPrompt = pfmconfig.SystemPromptLean
	request := Request{
		Row:            compose.Row{Kind: compose.NewClaude, CWD: "/work/project"},
		PrimaryAccount: 42,
		Cache1H:        true,
		Home:           home,
		FreshSocket:    "cc-native-policy",
		Config:         machine,
	}
	plan, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parsedShell(t, plan.Run)
	if launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "1h" ||
		parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != "1" || !parsed.Autonomy {
		t.Fatalf("native fresh settings=%#v autonomy=%t", parsed.SettingsEnv, parsed.Autonomy)
	}
	if strings.Contains(plan.Run, "--system-prompt-file") {
		t.Fatalf("lean prompt mode also received professor prompt file: %q", plan.Run)
	}
	if strings.HasPrefix(plan.Line, "TMUX= exec ") {
		t.Fatalf("non-bunker fresh launch used exec prefix: %q", plan.Line)
	}
}

func TestNewClaudeRequiresFreshSocket(t *testing.T) {
	home := t.TempDir()
	_, err := Synthesize(Request{
		Row:            compose.Row{Kind: compose.NewClaude, CWD: "/work/project"},
		PrimaryAccount: 1,
		Home:           home,
		Config:         testMachineConfig(home),
	})
	if err == nil || !strings.Contains(err.Error(), "fresh socket") {
		t.Fatalf("missing fresh socket error = %v", err)
	}
}

// The picker's plan carries what the starter must be told: a professor-mode
// chat opened without the fleet prompt, whichever way it opens.
func TestSynthesizeClaudePlansWarnWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, kind := range []compose.Kind{compose.NewClaude, compose.ResumeClaude, compose.Agent} {
		for _, staged := range []bool{false, true} {
			t.Run(kind.String()+"/staged="+strconv.FormatBool(staged), func(t *testing.T) {
				home := t.TempDir()
				if staged {
					stageProfessorPrompt(t, home)
				}
				machine := configuredMachinePolicy(home)
				machine.Claude.SystemPrompt = pfmconfig.SystemPromptProfessor
				plan, err := Synthesize(Request{
					Row:            compose.Row{Kind: kind, ID: id, CWD: "/work/project"},
					PrimaryAccount: 42, Home: home, FreshSocket: "cc-warn", Config: machine,
				})
				if err != nil {
					t.Fatal(err)
				}
				if staged {
					if len(plan.Warnings) != 0 {
						t.Fatalf("warnings = %q, want none", plan.Warnings)
					}
					return
				}
				if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "no source repository recorded") {
					t.Fatalf("warnings = %q, want one naming the missing clone", plan.Warnings)
				}
			})
		}
	}
}

// A picker inside a Herdr pane starts or resumes a chat IN that pane: the seat
// is reserved for the caller's own pane, the line the shell runs is the
// seat-host exec that makes the pane's process the chat, no new tab is laid
// out, and the caller's sole tab takes the chat's label (a named launch its
// name, a resume its row's name, an unnamed chat its project folder). A tab
// Herdr will not read keeps its label, named on stderr, and the birth proceeds.
func TestHerdrPickerOpensInItsOwnPane(t *testing.T) {
	const resumeID = "abcdef12-1234-1234-1234-123456789abc"
	for _, test := range []struct {
		name, launchName, label, refuse string
		row                             compose.Row
	}{
		{"a named new claude chat", "REVIEW", "REVIEW", "", compose.Row{Kind: compose.NewClaude}},
		{"an unnamed new claude chat", "", "", "", compose.Row{Kind: compose.NewClaude}},
		{"a new codex chat", "", "", "", compose.Row{Kind: compose.NewCodex}},
		{"a new opencode chat", "", "", "", compose.Row{Kind: compose.NewOpenCode}},
		{"a named claude resume", "", "OLD CHAT", "", compose.Row{Kind: compose.ResumeClaude, ID: resumeID, Name: "OLD CHAT"}},
		{"an unnamed claude resume", "", "", "", compose.Row{Kind: compose.ResumeClaude, ID: resumeID}},
		{"a codex resume", "", "", "", compose.Row{Kind: compose.ResumeCodex, ID: resumeID}},
		{"an opencode resume", "", "", "", compose.Row{Kind: compose.ResumeOpenCode, ID: resumeID}},
		{"a tab Herdr will not read", "", "", "tab.get", compose.Row{Kind: compose.NewClaude}},
		{"a pane this process does not front", "", "", "stale", compose.Row{Kind: compose.NewClaude}},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fake := newPickerHerdr(t)
			fake.mu.Lock()
			fake.refuse = test.refuse
			fake.mu.Unlock()
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", fake.socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			t.Setenv("TMUX", "")
			home := values.Home
			machine := testMachineConfig(home)
			machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 2, Home: home + "/opencode"}}
			binary := filepath.Join(home, "engine")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			machine.Claude.Binary, machine.Codex.Binary, machine.OpenCode.Binary = binary, binary, binary
			var stderr strings.Builder
			executor, err := New(Dependencies{Tmux: &fakeActionTmux{alive: map[string]bool{}}, Stderr: &stderr})
			if err != nil {
				t.Fatal(err)
			}
			row := test.row
			row.CWD = home
			label := cmp.Or(test.label, filepath.Base(home))
			line, err := executor.Open(context.Background(), Request{
				Row: row, LaunchName: test.launchName, Home: home, PrimaryAccount: 2,
				FreshSocket: "unused", Config: machine,
			})
			if test.refuse == "stale" {
				// A stale or inherited HERDR_PANE_ID: nothing reserved, nothing relabelled.
				db := fleetdb.OpenSharedState(context.Background(), values)
				_, found, readErr := db.SeatByPane(context.Background(), fake.socket, "w6:p3")
				if closeErr := db.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				_, renames := fake.seen()
				if err == nil || !strings.Contains(err.Error(), "ownership unproved") || line != "" || found ||
					readErr != nil || len(renames) != 0 {
					t.Fatalf("stale pane open line=%q err=%v seat found=%v (%v) renames=%q",
						line, err, found, readErr, renames)
				}
				return
			}
			if err != nil {
				t.Fatalf("picker open: %v", err)
			}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			seat, found, err := db.SeatByPane(context.Background(), fake.socket, "w6:p3")
			if err != nil || !found || seat.Backend != "herdr" || seat.HerdrPane != "w6:p3" ||
				seat.Engine != string(compose.EngineForKind(row.Kind)) || seat.SessionID == "" {
				t.Fatalf("caller pane seat=%+v found=%v err=%v", seat, found, err)
			}
			if want := "exec pfm internal seat-host " + seat.SeatID; line != want {
				t.Fatalf("line = %q, want %q", line, want)
			}
			if row.ID != "" {
				run, err := db.ReadSeatRun(seat.SeatID)
				if err != nil || seat.SessionID != row.ID || !slices.Contains(run.Args, row.ID) {
					t.Fatalf("resumed seat=%+v run=%+v err=%v", seat, run, err)
				}
			}
			methods, renames := fake.seen()
			if slices.Contains(methods, "layout.apply") {
				t.Fatalf("a picker open laid out a new Herdr tab: %q", methods)
			}
			if test.refuse != "" {
				if len(renames) != 0 || !strings.Contains(stderr.String(), "Herdr tab of pane w6:p3 keeps its label") {
					t.Fatalf("refused tab renames = %q, stderr = %q", renames, stderr.String())
				}
				return
			}
			if want := []string{"w6:t1=" + label}; !slices.Equal(renames, want) {
				t.Fatalf("tab renames = %q, want %q (methods %q)", renames, want, methods)
			}
		})
	}
}

// pickerHerdr is a Herdr socket for a picker running in pane w6:p3, alone in
// tab w6:t1 (labelled "2"); it records every method asked of it and every tab
// rename as "{tab}={label}".
type pickerHerdr struct {
	socket  string
	mu      sync.Mutex
	methods []string
	renames []string
	refuse  string // the one method answered with a refusal
}

func (fake *pickerHerdr) seen() (methods, renames []string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return slices.Clone(fake.methods), slices.Clone(fake.renames)
}

func herdrPickerSocket(t *testing.T) string {
	t.Helper()
	return newPickerHerdr(t).socket
}

func newPickerHerdr(t *testing.T) *pickerHerdr {
	t.Helper()
	fake := &pickerHerdr{socket: filepath.Join(testjail.ShortRoot(t), "herdr.sock")}
	listener, err := net.Listen("unix", fake.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
					Params struct {
						PaneID string `json:"pane_id"`
						TabID  string `json:"tab_id"`
						Label  string `json:"label"`
					} `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				fake.mu.Lock()
				refused, stale := req.Method == fake.refuse, fake.refuse == "stale"
				fake.methods = append(fake.methods, req.Method)
				if req.Method == "tab.rename" {
					fake.renames = append(fake.renames, req.Params.TabID+"="+req.Params.Label)
				}
				fake.mu.Unlock()
				if refused {
					_, _ = fmt.Fprintf(
						conn,
						`{"id":%q,"ok":false,"error":{"code":"unknown_method","message":"fixture refusal"}}`+"\n",
						req.ID,
					)
					return
				}
				var result any
				switch req.Method {
				case "pane.get", "pane.focus":
					result = testjail.HerdrPaneInfo(req.Params.PaneID)
				case "tab.get", "tab.rename":
					label := "2"
					if req.Method == "tab.rename" {
						label = req.Params.Label
					}
					result = map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": req.Params.TabID, "label": label, "pane_count": 1},
					}
				case "pane.process_info":
					foreground := os.Getpid()
					if stale {
						foreground = 1
					}
					result = map[string]any{"type": "pane_process_info", "process_info": map[string]any{
						"pane_id": req.Params.PaneID, "shell_pid": 1,
						"foreground_processes": []any{map[string]any{"pid": foreground, "name": "pfm"}},
					}}
				case "ping":
					result = map[string]any{"type": "pong", "version": "0.9.5", "protocol": 22}
				case "layout.apply":
					result = map[string]any{"layout": map[string]any{
						"workspace_id": "w6", "root": map[string]any{"type": "pane", "pane_id": "w6:p9"},
					}}
				default:
					result = map[string]any{"pane": map[string]any{"pane_id": "w6:p3"}}
				}
				body, err := json.Marshal(map[string]any{"id": req.ID, "ok": true, "result": result})
				if err != nil {
					t.Errorf("encode Herdr %s reply: %v", req.Method, err)
					return
				}
				_, _ = fmt.Fprintf(conn, "%s\n", body)
			}()
		}
	}()
	return fake
}
