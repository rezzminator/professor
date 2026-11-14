package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestOpenRestoredIDRejectsMissingSession(t *testing.T) {
	var stderr bytes.Buffer
	code := OpenRestoredID(context.Background(), "", io.Discard, &stderr, nil)
	if code != 2 || stderr.String() != "pfm chat open: restored session ID is required\n" {
		t.Fatalf("OpenRestoredID empty session = %d, %q, want explicit usage error", code, stderr.String())
	}
}

func TestOpenRestoredIDRebindsRecipeWithoutKeys(t *testing.T) {
	for _, history := range []string{"never-bound absent", "unanswered", "answered", "malformed", "protocol mismatch", "unsafe live owner", "unsafe old probe", "agent missing", "agent denied", "pane agent missing", "unknown legacy", "same address legacy", "same address never-bound", "same address live legacy", "unlearned restore", "unrelated caller", "unnamed"} {
		t.Run(history, func(t *testing.T) {
			testjail.Fleet(t)
			notices := make(chan string, 4)
			labels := make(chan string, 4)
			tabLabels := make(chan string, 4)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "pane.list":
					if strings.HasPrefix(history, "same address") || history == "unlearned restore" {
						return map[string]any{
							"type":  "pane_list",
							"panes": []any{testjail.HerdrPaneInfo("w9:p2")["pane"]},
						}, ""
					}
					if history == "never-bound absent" {
						return map[string]any{"type": "pane_list", "panes": []any{}}, ""
					}
					return nil, "permission_denied"
				case "ping":
					protocol := 22

					if history == "protocol mismatch" {
						protocol = 23
					}
					return map[string]any{"type": "pong", "protocol": protocol, "version": "fixture"}, ""
				case "pane.get":
					var pane string
					_ = json.Unmarshal(req.Params["pane_id"], &pane)
					if pane == "w6:p3" && history != "unsafe live owner" && history != "unsafe old probe" {
						return nil, "pane_not_found"
					}
					return testjail.HerdrPaneInfo(pane), ""
				case "pane.process_info":
					var pane string
					_ = json.Unmarshal(req.Params["pane_id"], &pane)
					if pane == "w6:p3" && history == "unsafe old probe" {
						return nil, "permission_denied"
					}
					return map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id":              pane,
							"shell_pid":            700001,
							"foreground_processes": []any{map[string]any{"pid": os.Getpid()}},
						},
					}, ""

				case "tab.get":
					return map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": "w9:t1", "label": "INTEGRATION", "pane_count": 1},
					}, ""
				case "tab.rename":
					var label string
					_ = json.Unmarshal(req.Params["label"], &label)
					tabLabels <- label
					return map[string]any{"type": "ok"}, ""
				case "pane.rename":
					var label string
					_ = json.Unmarshal(req.Params["label"], &label)
					labels <- label
					if history == "pane agent missing" {
						return nil, "agent_not_found"
					}
					return map[string]any{"pane": map[string]any{"pane_id": "w9:p2", "label": "INTEGRATION"}}, ""
				case "agent.rename":
					if history == "agent missing" {
						return nil, "agent_not_found"
					}
					if history == "agent denied" {
						return nil, "permission_denied"
					}
					return map[string]any{"agent": map[string]any{"pane_id": "w9:p2", "name": "integration"}}, ""
				case "notification.show":
					var body string
					_ = json.Unmarshal(req.Params["body"], &body)
					notices <- body
					return map[string]any{"type": "notification_show", "shown": false, "reason": "disabled"}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			if history != "never-bound absent" && history != "unknown legacy" &&
				!strings.HasPrefix(history, "same address") &&
				history != "unlearned restore" {
				seat = nativeChatHost(t, db, seat, history == "unsafe live owner" || history == "unsafe old probe")
			}
			if history == "unnamed" {
				seat.Name = ""
				if err := db.PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(history, "same address") || history == "unlearned restore" {
				if history == "same address never-bound" {
					seat.HerdrTerminalID = "pre-restart-terminal"
				}
				if history == "unlearned restore" {
					seat.SessionID = ""
				}
				seat.HerdrPane = "w9:p2"
				seat.HerdrWorkspace = "w9"
				if err := db.PutSeat(context.Background(), seat); err != nil {
					t.Fatal(err)
				}
			}
			run := fleetdb.SeatRun{
				Binary:     "/fixture/claude",
				CWD:        runtime.Paths.Home,
				CostLedger: true,
				Args: []string{
					"--session-id",
					seat.SessionID,
					"--name",
					"old name",
					"--model",
					"fixture-model",
					"--effort",
					"high",
					"--settings",
					`{"env":{"FIXTURE":"yes"}}`,
				},
				Env: []string{"ACCOUNT=2", "HERDR_SOCKET_PATH=/old/socket", "HERDR_PANE_ID=w6:p3", "TMUX=old"},
			}
			if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "session.jsonl")
			body := `{"type":"user","message":{"content":"private prompt"}}` + "\n"
			if history == "answered" {
				body += `{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}` + "\n"
			}
			if history == "malformed" {
				body += "{"
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			index, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			if err := index.UpsertTranscript(
				context.Background(),
				store.Transcript{UUID: seat.SessionID, Path: path, CWD: runtime.Paths.Home},
			); err != nil {
				t.Fatal(err)
			}
			if err := index.Close(); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w9:p2")
			t.Setenv("TMUX", "")
			nativeChatRestoredCaller(t, history != "unrelated caller")
			if history == "same address live legacy" {
				dir := filepath.Join(runtime.Paths.ProcRoot, "700002")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				for name, body := range map[string]string{"cmdline": "/fixture/pfm\x00internal\x00launch\x00--engine\x00cc\x00", "environ": "HERDR_SOCKET_PATH=" + socket + "\x00HERDR_PANE_ID=w9:p2\x00"} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var stdout, stderr bytes.Buffer
			target := seat.SessionID
			if target == "" {
				target = seat.SeatID
			}
			code := OpenRestoredID(context.Background(), target, &stdout, &stderr, &runtime)
			if history == "unsafe live owner" || history == "unsafe old probe" || history == "unknown legacy" ||
				history == "same address live legacy" ||
				history == "unrelated caller" {
				current, _, err := db.Seat(context.Background(), seat.SeatID)
				recipe, recipeErr := db.ReadSeatRun(seat.SeatID)
				want := "still owns a live supervisor"
				if history == "unrelated caller" {
					want = "outside restored caller pane"
				}
				if history == "unknown legacy" {
					want = "saved supervisor birth"
				}
				if history == "unsafe old probe" {
					want = "still owns a live supervisor"
				}
				if code != 1 || !strings.Contains(stderr.String(), want) || err != nil || recipeErr != nil ||
					current.HerdrPane != seat.HerdrPane ||
					!slices.Equal(recipe.Args, run.Args) {
					t.Fatalf(
						"unsafe recovery code=%d seat=%+v recipe=%+v err=%v/%v stderr=%q",
						code,
						current,
						recipe,
						err,
						recipeErr,
						stderr.String(),
					)
				}
				return
			}
			if history == "protocol mismatch" {
				current, _, err := db.Seat(context.Background(), seat.SeatID)
				if code != 1 || !strings.Contains(stderr.String(), "DIVERGES") || err != nil ||
					current.HerdrPane != seat.HerdrPane {
					t.Fatalf("protocol refusal code=%d seat=%+v err=%v stderr=%q", code, current, err, stderr.String())
				}
				return
			}
			if history == "agent denied" || history == "pane agent missing" {
				want := "permission_denied"
				if history == "pane agent missing" {
					want = "agent_not_found"
				}
				if code != 1 || !strings.Contains(stderr.String(), want) || stdout.Len() != 0 {
					t.Fatalf("rename failure code=%d out=%q err=%q", code, stdout.String(), stderr.String())
				}
				return
			}
			// A named chat's pane takes its name; an unnamed one's tab is named
			// as a born tab is, after its project folder, its pane left unnamed.
			if history == "unnamed" {
				want := filepath.Base(runtime.Paths.Home)
				if len(labels) != 0 || len(tabLabels) != 1 {
					t.Fatalf("pane renames %d, tab renames %d, want only the tab named %q; stderr=%q",
						len(labels), len(tabLabels), want, stderr.String())
				}
				if label := <-tabLabels; label != want {
					t.Fatalf("restored tab label = %q, want %q", label, want)
				}
			} else if len(labels) != 1 || <-labels != seat.Name {
				t.Fatal("restored pane label did not match durable name")
			}
			if code != 0 {
				t.Fatalf("restored code=%d out=%q err=%q", code, stdout.String(), stderr.String())
			}
			if stdout.String() != "exec pfm internal seat-host restore-seat\n" {
				t.Fatalf("restored eval=%q", stdout.String())
			}
			current, found, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || !found || current.SessionID != seat.SessionID || current.HerdrPane != "w9:p2" ||
				current.HerdrWorkspace != "w9" ||
				current.HerdrTerminalID != "fixture-w9:p2" || current.SeatHostPID != 0 || current.SeatHostStart != 0 || current.SeatHostBirth != 0 ||
				current.Name != seat.Name {
				t.Fatalf("rebound seat=%+v found=%t err=%v", current, found, err)
			}
			recipe, err := db.ReadSeatRun(seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			if recipe.Binary != run.Binary || recipe.CWD != run.CWD || !recipe.CostLedger ||
				!slices.Contains(recipe.Env, "ACCOUNT=2") ||
				!slices.Contains(recipe.Env, "HERDR_PANE_ID=w9:p2") {
				t.Fatalf("lost recipe preferences: %+v", recipe)
			}
			if history == "unlearned restore" {
				if !slices.Equal(recipe.Args, run.Args) {
					t.Fatalf("unlearned seat did not preserve birth recipe: %q", recipe.Args)
				}
			} else if !slices.Equal(
				recipe.Args,
				[]string{
					"--model",
					"fixture-model",
					"--effort",
					"high",
					"--settings",
					`{"env":{"FIXTURE":"yes"}}`,
					"--resume",
					seat.SessionID,
				},
			) {
				t.Fatalf("resume argv=%q", recipe.Args)
			}
			switch history {
			case "unanswered":
				want := "your last message was not answered — re-send it"
				if current.LostNote != want || len(notices) != 1 || <-notices != want {
					t.Fatalf("loss note=%q notices=%d", current.LostNote, len(notices))
				}
			case "answered":
				if current.LostNote != "" {
					t.Fatalf("answered note=%q", current.LostNote)
				}
			case "malformed":
				if !strings.Contains(current.LostNote, "restart loss unknown") ||
					!strings.Contains(stderr.String(), "loss unknown") ||
					!strings.Contains(stderr.String(), "malformed") {
					t.Fatalf("hidden loss read error: %s", stderr.String())
				}
			}
			if strings.Contains(stdout.String()+stderr.String()+current.LostNote, "private prompt") {
				t.Fatal("recovery quoted private prompt")
			}
		})
	}
}

func TestRestoredOpenKeepsTmuxBirthEnvironmentWithoutPrompt(t *testing.T) {
	testjail.Fleet(t)
	old := newOpenExecutor
	t.Cleanup(func() { newOpenExecutor = old })
	newOpenExecutor = func(deps action.Dependencies) (*action.Executor, error) {
		if deps.Gate == nil {
			t.Fatal("restored open did not provide a noninteractive gate")
		}
		reboot, err := deps.Gate.Confirm(
			context.Background(),
			action.GateRequest{Name: "INTEGRATION", BirthAccount: 2, PrimaryAccount: 1},
		)
		if err != nil || reboot {
			t.Fatalf("restored gate reboot=%t err=%v", reboot, err)
		}
		return old(deps)
	}
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), restoredOpenKey{}, true)
	_, _, _, err = prepareOpen(
		ctx,
		compose.Row{Kind: compose.LiveClaude, Name: "INTEGRATION"},
		1,
		false,
		"",
		io.Discard,
		config.Runtime{Paths: values},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestResumeNativeRecipePreservesEachRegisteredEngine(t *testing.T) {
	for _, tc := range []struct {
		engine     pfmengine.ID
		args, want []string
	}{
		{pfmengine.Codex, []string{"--model", "custom", "-c", "reasoning_effort=high", "resume", "old"}, []string{"--model", "custom", "-c", "reasoning_effort=high", "resume", "same-session"}},
		{pfmengine.Codex, []string{"--no-daemon", "--dangerously-bypass-approvals-and-sandbox", "resume", "old", "PRIVATE PROMPT"}, []string{"--no-daemon", "--dangerously-bypass-approvals-and-sandbox", "resume", "same-session"}},
		{pfmengine.Codex, []string{"--no-daemon", "--full-auto", "PRIVATE PROMPT"}, []string{"--no-daemon", "--full-auto", "resume", "same-session"}},
		{pfmengine.OpenCode, []string{"--continue", "--session", "old", "/work/project"}, []string{"--session", "same-session", "/work/project"}},
		{pfmengine.OpenCode, []string{"--model", "custom", "--session", "old", "/work/project"}, []string{"--model", "custom", "--session", "same-session", "/work/project"}},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			run, err := resumeNativeRun(
				fleetdb.SeatRun{Binary: "custom", CWD: "/work/project", Args: tc.args, Env: []string{"ACCOUNT=3"}},
				fleetdb.Seat{Engine: string(tc.engine), SessionID: "same-session"}, config.Config{}, io.Discard,
			)
			if err == nil {
				run, err = resumeNativeRun(
					run,
					fleetdb.Seat{Engine: string(tc.engine), SessionID: "same-session"},
					config.Config{}, io.Discard,
				)
			}
			if err != nil || !slices.Equal(run.Args, tc.want) || !slices.Equal(run.Env, []string{"ACCOUNT=3"}) {
				t.Fatalf("recipe=%+v err=%v want=%q", run, err, tc.want)
			}
		})
	}
}

// A recorded recipe pfm cannot read costs only the unreadable words, never the
// resume: the chat restores on its session with a warning naming the loss.
func TestRestoredRecipeKeepsTheResumeOnABadRecord(t *testing.T) {
	for _, tc := range []struct {
		name       string
		engine     pfmengine.ID
		args, want []string
		warn       string
		themeSync  bool
	}{
		{
			"an unparsable Claude recipe resumes with default args", pfmengine.Claude,
			[]string{"--model", "opus", "--settings"},
			[]string{"--resume", "same-session"},
			`pfm WARN: chat "keeper": restored recipe unparsable (--settings requires a value); resuming with default launch args` + "\n",
			false,
		},
		{
			"an unrefreshable theme pin resumes as recorded", pfmengine.Claude,
			[]string{"--settings", "{"},
			[]string{"--settings", "{", "--resume", "same-session"},
			`pfm WARN: chat "keeper": restored recipe --settings: unexpected end of JSON input; ` +
				"left to Claude as written\n" + `pfm WARN: chat "keeper": restored theme pin not refreshed ` +
				`(refresh theme pin: parse settings: unexpected end of JSON input); ` +
				"resuming with the recorded settings\n",
			true,
		},
		{
			"an ambiguous unregistered flag is dropped with its value", pfmengine.Codex,
			[]string{"--mystery", "value", "--model", "custom", "resume", "old"},
			[]string{"--model", "custom", "resume", "same-session"},
			`pfm WARN: chat "keeper": restored recipe flag --mystery has an ambiguous value; dropped` + "\n",
			false,
		},
		{
			"a stripped flag missing its value strips alone", pfmengine.OpenCode,
			[]string{"--model", "custom", "--session"},
			[]string{"--model", "custom", "--session", "same-session", "/work/project"},
			"",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.themeSync {
				home := t.TempDir()
				t.Setenv(paths.EnvHome, home)
				statePath := filepath.Join(home, ".local", "state", "pfm", "theme", "state.json")
				if err := atomicfile.Write(statePath, []byte(`{"enabled":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stderr strings.Builder
			run, err := resumeNativeRun(
				fleetdb.SeatRun{Binary: "fixture", CWD: "/work/project", Args: tc.args},
				fleetdb.Seat{Name: "keeper", Engine: string(tc.engine), SessionID: "same-session"},
				config.Config{}, &stderr,
			)
			if err != nil || !slices.Equal(run.Args, tc.want) || stderr.String() != tc.warn {
				t.Fatalf(
					"args=%q err=%v stderr=%q; want args=%q stderr=%q",
					run.Args,
					err,
					stderr.String(),
					tc.want,
					tc.warn,
				)
			}
		})
	}
}

func TestRestoredRecipeRechecksThemeSync(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		pinned      bool
	}{
		{"enabled", `{"enabled":true}`, false},
		{"disabled", `{"enabled":false}`, true},
		{"malformed", `{`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv(paths.EnvHome, home)
			statePath := filepath.Join(home, ".local", "state", "pfm", "theme", "state.json")
			if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte(tc.state), 0o600); err != nil {
				t.Fatal(err)
			}
			run, err := resumeNativeRun(
				fleetdb.SeatRun{Binary: "fixture", Args: []string{
					claudelaunch.SettingsFlag, `{"theme":"custom:launch-pin","counter":9007199254740993}`,
				}},
				fleetdb.Seat{Engine: string(pfmengine.Claude), SessionID: "same-session"}, config.Config{}, io.Discard,
			)
			if err != nil {
				t.Fatal(err)
			}
			var settings map[string]json.RawMessage
			if err := json.Unmarshal([]byte(run.Args[1]), &settings); err != nil {
				t.Fatal(err)
			}
			if _, pinned := settings["theme"]; pinned != tc.pinned {
				t.Fatalf("restored theme pin=%t, want %t: %s", pinned, tc.pinned, run.Args[1])
			}
			if string(settings["counter"]) != "9007199254740993" ||
				!slices.Equal(run.Args[2:], []string{"--resume", "same-session"}) {
				t.Fatalf("non-theme recipe changed: %q", run.Args)
			}
		})
	}
}

func TestRestoredRecipeNeverResendsPromptAfterBooleanFlags(t *testing.T) {
	run, err := resumeNativeRun(
		fleetdb.SeatRun{
			Binary: "fixture",
			Args: []string{
				"--allow-dangerously-skip-permissions",
				"--dangerously-skip-permissions",
				"PRIVATE PROMPT",
				"--settings",
				"{}",
			},
		},
		fleetdb.Seat{Engine: string(pfmengine.Claude), SessionID: "same-session"}, config.Config{}, io.Discard,
	)
	want := []string{
		"--allow-dangerously-skip-permissions",
		"--dangerously-skip-permissions",
		"--settings",
		"{}",
		"--resume",
		"same-session",
	}
	if err != nil || !slices.Equal(run.Args, want) {
		t.Fatalf("recovery argv=%q err=%v want=%q", run.Args, err, want)
	}
}

// A restored auto-mode chat keeps --permission-mode and its value.
func TestRestoredRecipeKeepsAutoPermissionMode(t *testing.T) {
	run, err := resumeNativeRun(
		fleetdb.SeatRun{
			Binary: "fixture",
			Args: []string{
				"--allow-dangerously-skip-permissions",
				"--permission-mode",
				"auto",
				"PRIVATE PROMPT",
				"--settings",
				"{}",
			},
		},
		fleetdb.Seat{Engine: string(pfmengine.Claude), SessionID: "same-session"}, config.Config{}, io.Discard,
	)
	want := []string{
		"--allow-dangerously-skip-permissions",
		"--permission-mode",
		"auto",
		"--settings",
		"{}",
		"--resume",
		"same-session",
	}
	if err != nil || !slices.Equal(run.Args, want) {
		t.Fatalf("restored argv=%q err=%v want=%q", run.Args, err, want)
	}
}

func TestOpenRestoredIDWithoutSeatUsesCurrentHerdrPane(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "1b67f24e-bbe9-491f-8390-86f50b45a598"
	seedClaudeChat(t, root, id, assistantSaid("done"))
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		switch req.Method {
		case "ping":
			return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
		case "pane.get":
			return testjail.HerdrPaneInfo("w9:p2"), ""
		case "pane.process_info":
			info := map[string]any{
				"pane_id":              "w9:p2",
				"shell_pid":            700001,
				"foreground_processes": []any{map[string]any{"pid": os.Getpid()}},
			}
			return map[string]any{"type": "pane_process_info", "process_info": info}, ""
		case "tab.get":
			return map[string]any{
				"type": "tab_info",
				"tab":  map[string]any{"tab_id": "w9:t1", "label": "INTEGRATION", "pane_count": 1},
			}, ""
		case "tab.rename":
			return map[string]any{"type": "ok"}, ""
		case "pane.rename":
			var label string
			_ = json.Unmarshal(req.Params["label"], &label)
			return map[string]any{"pane": map[string]any{"pane_id": "w9:p2", "label": label}}, ""
		case "agent.rename":
			var name string
			_ = json.Unmarshal(req.Params["name"], &name)
			return map[string]any{"agent": map[string]any{"pane_id": "w9:p2", "name": name}}, ""
		default:
			return nil, "unexpected_method"
		}
	})
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w9:p2")
	t.Setenv("TMUX", "")
	nativeChatRestoredCaller(t, true)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(runtime.Paths.Home, "native-engine")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime.Config.Claude.Binary = binary
	// The restore lands in the fleet home; as a managed root its settings envs ride the seat.
	for path, body := range map[string]string{
		filepath.Join(runtime.Paths.Home, ".professor", "baseline.json"): "{}",
		paths.Settings(runtime.Paths.Home):                               `{"envs":{"TEAM_MODE":"strict"}}`,
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeOpenTmux{alive: map[string]bool{}}
	stubOpenExecutor(t, fake)
	var stdout, stderr bytes.Buffer
	code := OpenRestoredID(context.Background(), id, &stdout, &stderr, &runtime)
	if code != 0 || !strings.HasPrefix(stdout.String(), "exec pfm internal seat-host ") {
		t.Fatalf("missing-seat recovery code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
	db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat, found, err := db.SeatBySession(context.Background(), id)
	if err != nil || !found || seat.HerdrPane != "w9:p2" {
		t.Fatalf("missing-seat recovery seat=%+v found=%t err=%v", seat, found, err)
	}
	// The chat's recorded directory is gone: it restores in the fleet home
	// and says so, the one missing-directory rule every door shares.
	run, err := db.ReadSeatRun(seat.SeatID)
	want := "opened in " + runtime.Paths.Home + ": its own directory /work/project is gone"
	if err != nil || run.CWD != runtime.Paths.Home || !strings.Contains(stderr.String(), want) {
		t.Fatalf(
			"restored cwd=%q err=%v stderr=%q, want the fleet home and %q",
			run.CWD,
			err,
			stderr.String(),
			want,
		)
	}
	if !slices.Contains(run.Env, "TEAM_MODE=strict") {
		t.Fatalf(
			"restored seat env = %q, want TEAM_MODE=strict from %s",
			run.Env,
			paths.Settings(runtime.Paths.Home),
		)
	}
}

func TestOpenRestoredTitlelessCodexWithoutSeat(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "c8b40132-3190-4b56-9368-d2f78930a340"
	seedCodexThread(t, root, id)
	rollout := filepath.Join(root, "codex", "sessions", "2030", "01", "02", "rollout-2030-01-02T03-04-05-"+id+".jsonl")
	body, err := os.ReadFile(rollout)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.ReplaceAll(body, []byte("resume me"), []byte(""))
	if err := os.WriteFile(rollout, body, 0o600); err != nil {
		t.Fatal(err)
	}
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		switch req.Method {
		case "ping":
			return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
		case "pane.get":
			return testjail.HerdrPaneInfo("w9:p2"), ""
		case "pane.process_info":
			return map[string]any{
				"type": "pane_process_info",
				"process_info": map[string]any{
					"pane_id":              "w9:p2",
					"shell_pid":            700001,
					"foreground_processes": []any{map[string]any{"pid": os.Getpid()}},
				},
			}, ""
		case "tab.get":
			return map[string]any{
				"type": "tab_info",
				"tab":  map[string]any{"tab_id": "w9:t1", "label": "INTEGRATION", "pane_count": 1},
			}, ""
		case "tab.rename":
			return map[string]any{"type": "ok"}, ""
		case "pane.rename":
			var label string
			_ = json.Unmarshal(req.Params["label"], &label)
			return map[string]any{"pane": map[string]any{"pane_id": "w9:p2", "label": label}}, ""
		case "agent.rename":
			var name string
			_ = json.Unmarshal(req.Params["name"], &name)
			return map[string]any{"agent": map[string]any{"pane_id": "w9:p2", "name": name}}, ""
		case "notification.show":
			return map[string]any{"type": "notification_show", "shown": false, "reason": "disabled"}, ""
		default:
			return nil, "unexpected_method"
		}
	})
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w9:p2")
	t.Setenv("TMUX", "")
	nativeChatRestoredCaller(t, true)
	codex := filepath.Join(t.TempDir(), "codex")
	if err := testjail.WriteExecutableNeverRun(codex, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	runtime.Config.Codex.Binary = codex
	rows, err := Rows(context.Background(), io.Discard, &runtime)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == id {
			found = true
			if row.Name != "" {
				t.Fatalf("fixture unexpectedly titled %q", row.Name)
			}
		}
	}
	if !found {
		t.Fatal("canonical rollout was not indexed")
	}
	db := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, found, err := db.SeatBySession(context.Background(), id); err != nil || found {
		t.Fatalf("fixture already has seat: %t %v", found, err)
	}
	var stdout, stderr bytes.Buffer
	code := OpenRestoredID(context.Background(), id, &stdout, &stderr, &runtime)
	if code != 0 || !strings.HasPrefix(stdout.String(), "exec pfm internal seat-host ") {
		t.Fatalf("titleless recovery code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
	seat, found, err := db.SeatBySession(context.Background(), id)
	if err != nil || !found || seat.Name != id || seat.HerdrPane != "w9:p2" {
		t.Fatalf("recovered seat=%+v found=%t err=%v", seat, found, err)
	}
	run, err := db.ReadSeatRun(seat.SeatID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for i, arg := range run.Args {
		if arg == "resume" {
			count++
			if i+1 >= len(run.Args) || run.Args[i+1] != id {
				t.Fatalf("resume identity=%q", run.Args)
			}
		}
		if arg == "--model" || strings.Contains(arg, "resume me") {
			t.Fatalf("invented model or prompt replay: %q", run.Args)
		}
	}
	if count != 1 {
		t.Fatalf("resume argv=%q", run.Args)
	}
}

func nativeChatRestoredCaller(t *testing.T, inside bool, parents ...int) {
	t.Helper()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	parent := 700001
	if len(parents) > 0 {
		parent = parents[0]
	}
	if !inside {
		parent = 700002
	}
	for pid, ppid := range map[int]int{os.Getpid(): parent, parent: 1} {
		dir := filepath.Join(values.ProcRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
		fields[1] = fmt.Sprint(ppid)
		if err := os.WriteFile(
			filepath.Join(dir, "stat"),
			[]byte(fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " "))),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResumeTailRefusesAChatAnotherLiveProcessHolds pins the session a resume
// of a continued Claude chat names: the newest segment, and only while no live
// chat but the resuming seat itself answers to it.
func TestResumeTailRefusesAChatAnotherLiveProcessHolds(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	const (
		launch    = "11111111-2222-4333-8444-555555555555"
		continued = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	)
	index, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, transcript := range []store.Transcript{
		{UUID: launch, Path: "/jail/" + launch + ".jsonl", Size: 1, PromptCount: 1, ContinuedIn: continued},
		{UUID: continued, Path: "/jail/" + continued + ".jsonl", Size: 1, PromptCount: 1},
	} {
		if err := index.UpsertTranscript(ctx, transcript); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	own := compose.Row{
		Kind: compose.LiveClaude, Backend: "herdr", SeatID: "seat-1", ID: launch, Path: "/jail/" + continued + ".jsonl",
	}
	job := compose.Row{Kind: compose.Agent, ID: continued, Name: "moved"}
	unreadable := t.TempDir()
	for _, test := range []struct {
		name    string
		id      string
		seatID  string
		rows    []compose.Row
		want    string
		cacheDB string
		warn    string
	}{
		{"nothing holds it", launch, "seat-1", []compose.Row{}, continued, "", ""},
		{"only the resuming seat holds it", launch, "seat-1", []compose.Row{own}, continued, "", ""},
		{"a daemon job holds it", launch, "seat-1", []compose.Row{own, job}, "", "", ""},
		{"another seat holds it", launch, "", []compose.Row{own}, "", "", ""},
		{"an unmoved chat needs no scan", continued, "", nil, continued, "", ""},
		{"an unmoved chat a held scan shows live is refused", continued, "", []compose.Row{job}, "", "", ""},
		{
			"an unreadable index resumes the launch id", launch, "", nil, launch, unreadable,
			"pfm WARN: chat " + launch + ": newest segment unknown (",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.cacheDB != "" {
				t.Setenv(paths.EnvCacheDB, test.cacheDB)
			}
			var stderr strings.Builder
			got, err := resumeTail(ctx, config.Runtime{}, test.id, test.seatID, test.rows, &stderr)
			if test.want == "" {
				if err == nil || !strings.Contains(err.Error(), "open that chat instead") {
					t.Fatalf("resumeTail() = %q, %v; want a refusal naming the live chat", got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("resumeTail() = %q, %v; want %q", got, err, test.want)
			}
			const tail = "); resuming it, which may be an older segment\n"
			if warned := stderr.String(); test.warn == "" && warned != "" ||
				test.warn != "" && (!strings.HasPrefix(warned, test.warn) || !strings.HasSuffix(warned, tail)) {
				t.Fatalf("resumeTail() stderr = %q, want %q…%q", warned, test.warn, tail)
			}
		})
	}
}

// The restored Claude tail is the launch door's own session wording, not a
// second spelling: birth identity words are stripped, then SessionArgs names the id.
func TestRestoredClaudeTailIsTheLaunchSessionWording(t *testing.T) {
	run, err := resumeNativeRun(
		fleetdb.SeatRun{Binary: "fixture", Args: []string{
			"--model", "opus", "--session-id", "birth-id", "--resume", "old-id", "--fork-session",
		}},
		fleetdb.Seat{Engine: string(pfmengine.Claude), SessionID: "same-session"}, config.Config{}, io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := claudelaunch.SessionArgs("", "same-session", false)
	if len(run.Args) < len(want) || !slices.Equal(run.Args[len(run.Args)-len(want):], want) {
		t.Fatalf("restored tail = %q, want suffix %q", run.Args, want)
	}
	for _, gone := range []string{"birth-id", "old-id", "--fork-session"} {
		if slices.Contains(run.Args, gone) {
			t.Fatalf("birth identity %q survived: %q", gone, run.Args)
		}
	}
	if !slices.Contains(run.Args, "opus") {
		t.Fatalf("birth setting lost: %q", run.Args)
	}
}
