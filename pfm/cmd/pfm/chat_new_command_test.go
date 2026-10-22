package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// A seat named positionally keeps every flag written after its name: the
// flag parser must not stop at the name and silently birth a default chat.
func TestChatNewReadsFlagsAfterAPositionalName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "seat-x", "--timeout", "-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf(
			"chat new seat-x --timeout -1 = %d, want 2 (usage: the negative timeout after the name was read); stderr=%q",
			code,
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "usage: pfm chat new") {
		t.Fatalf("stderr = %q, want the chat new usage line", stderr.String())
	}
}

func newWorkbenchRunFixture(t *testing.T, jail *runJail) (string, string) {
	t.Helper()
	root := filepath.Join(jail.root, "work", "acme")
	dir := filepath.Join(root, "docs", "scribe")
	duo := filepath.Join(root, "docs", "duo")
	for path, body := range map[string]string{
		filepath.Join(root, ".professor", "baseline.json"): "{}",
		paths.WorkbenchManifest(dir):                       `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(dir, ".professor", "scribe.md"):      "You are scribe.",
		paths.WorkbenchManifest(duo):                       `{"prompt":"duo.md","engines":["codex","claude"]}`,
		filepath.Join(duo, ".professor", "duo.md"):         "You are duo.",
		filepath.Join(duo, "CLAUDE.md"):                    "Duo.\n",
		filepath.Join(root, ".claude", "agents", "r.md"):   "ROLE R",
		filepath.Join(root, ".codex", "agents", "r.toml"):  `developer_instructions = "ROLE R"`,
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, duo
}

// These tests share process environment and spawn timings, so they stay serial.
func TestChatNewWorkbench(t *testing.T) {
	for _, name := range []string{"auto-name", "next number", "explicit effort", "explicit harness", "omitted enabled", "omitted disabled", "omitted skips opencode", "no account", "disabled explicit", "invalid manifest", "outside", "roster unreadable", "Codex", "mirror failure", "role"} {
		t.Run(name, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			dir, duo := newWorkbenchRunFixture(t, jail)
			engine, socket, wantName := "cc", "cc-workbench", "_SCRIBE:1"
			extra := []string{}
			wantEffort, wantChannel := "xhigh", filepath.Join(dir, ".professor", "scribe.md")
			wantCode, wantError := 0, ""
			mirrorRecord := ""
			switch name {
			case "next number":
				t.Setenv(spawn.TestFreshSocketEnv, "cc-first-workbench")
				var stdout, stderr bytes.Buffer
				code := run(
					[]string{"chat", "new", "--engine", "cc", "--name", "_SCRIBE:1", "--cwd", dir},
					&stdout,
					&stderr,
				)
				if code != 0 {
					t.Fatalf("seed launch = %d, %s", code, stderr.String())
				}
				if err := os.WriteFile(
					jail.transcript,
					[]byte(
						`{"type":"user","cwd":"/work/acme/docs/scribe","message":{"content":"ready"}}`+"\n"+`{"type":"custom-title","customTitle":"_SCRIBE:1"}`+"\n",
					),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				wantName = "_SCRIBE:2"
			case "explicit effort":
				extra = []string{"--effort", "high"}
				wantEffort = "high"
			case "explicit harness":
				wantChannel = filepath.Join(jail.root, "work", "alt.md")
				if err := os.WriteFile(wantChannel, []byte("ALT PROMPT"), 0o600); err != nil {
					t.Fatal(err)
				}
				extra = []string{"--harness-prompt", wantChannel}
			case "omitted enabled":
				engine = ""
				dir = duo
				wantName = "DUO:1"
				wantEffort = ""
				wantChannel = filepath.Join(duo, ".professor", "duo.md")
				t.Setenv("CLAUDE_CODE_SESSION_ID", "caller")
			case "omitted disabled":
				engine = ""
				t.Setenv("CODEX_THREAD_ID", "caller")
			case "omitted skips opencode":
				// OpenCode has an account but no headless planner: chat new
				// takes the next enabled engine instead of failing on it.
				engine = ""
				dir = filepath.Join(jail.root, "work", "acme", "docs", "notes")
				for path, body := range map[string]string{
					paths.WorkbenchManifest(dir):                            `{"prompt":"p.md","title":"Notes","engines":["opencode","claude"]}`,
					filepath.Join(dir, ".professor", "p.md"):                "You are notes.",
					filepath.Join(jail.root, "opencode-store", "auth.json"): "{}",
				} {
					if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv(pfmengine.MustLookup(pfmengine.OpenCode).RootEnv, filepath.Join(jail.root, "opencode-store"))
				t.Setenv("CODEX_THREAD_ID", "caller")
				wantName, wantEffort = "NOTES:1", ""
				wantChannel = filepath.Join(dir, ".professor", "p.md")
			case "no account":
				engine = ""
				dir = filepath.Join(jail.root, "work", "acme", "docs", "cx-only")
				if err := atomicfile.Write(
					paths.WorkbenchManifest(dir),
					[]byte(`{"prompt":"p.md","engines":["codex"]}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := atomicfile.Write(
					filepath.Join(dir, ".professor", "p.md"),
					[]byte("You are cx-only."),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(jail.root, "codex", "auth.json")); err != nil {
					t.Fatal(err)
				}
				wantCode, wantError = 2, "pfm chat new: workbench "+dir+" enables codex, and this machine has no account for any of them\n"
			case "disabled explicit":
				engine = "cx"
				extra = []string{"--name", "x"}
				_, err := workbench.ForLaunch(dir, pfmengine.Codex, workbench.New)
				if err == nil {
					t.Fatal("fixture ForLaunch did not fail")
				}
				wantCode, wantError = 2, "pfm chat new: "+err.Error()+"\n"
			case "invalid manifest":
				if err := os.WriteFile(paths.WorkbenchManifest(dir), []byte(`{"prompt":""}`), 0o600); err != nil {
					t.Fatal(err)
				}
				extra = []string{"--name", "x"}
				_, err := workbench.ForLaunch(dir, pfmengine.Claude, workbench.New)
				if err == nil {
					t.Fatal("fixture ForLaunch did not fail")
				}
				wantCode, wantError = 2, "pfm chat new: "+err.Error()+"\n"
			case "outside":
				dir = filepath.Join(jail.root, "work")
				wantCode, wantError = 2, "usage: pfm chat new [--name NAME]"
			case "roster unreadable":
				blocker := filepath.Join(jail.root, "blocker")
				if err := os.WriteFile(blocker, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
				runtime, err := pfmconfig.LoadRuntime("")
				if err != nil {
					t.Fatal(err)
				}
				_, rosterErr := pfmchat.Rows(context.Background(), io.Discard, &runtime)
				if rosterErr == nil {
					t.Fatal("fixture roster did not fail")
				}
				wantCode, wantError = 1, "pfm chat new: "+rosterErr.Error()+"\n"
			case "Codex", "mirror failure":
				engine, socket, dir, wantName = "cx", "cx-workbench", duo, "DUO:1"
				t.Setenv("CX_STUB_ARGV", filepath.Join(jail.root, "cx-argv"))
				reply, code := testjail.CleanMirrorReply, 0
				if name == "mirror failure" {
					reply, code = testjail.FailedMirrorReply, 1
					wantCode, wantError = 2, "pfm chat new: build the codex mirror of workbench "+duo+":"
				}
				mirrorRecord = testjail.FakeEngineSync(t, reply, code)
			case "role":
				extra = []string{"--agent-role", "r"}
			}
			t.Setenv(spawn.TestFreshSocketEnv, socket)
			args := []string{"chat", "new", "--cwd", dir}
			if engine != "" {
				args = append(args, "--engine", engine)
			}
			args = append(args, extra...)
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != wantCode {
				t.Fatalf(
					"chat new exit %d, want %d; stdout=%q stderr=%q",
					code,
					wantCode,
					stdout.String(),
					stderr.String(),
				)
			}
			if wantCode != 0 {
				if !strings.Contains(stderr.String(), wantError) {
					t.Fatalf("error = %q; want %q", stderr.String(), wantError)
				}
				if name != "outside" && name != "mirror failure" && stderr.String() != wantError {
					t.Fatalf("error = %q; want exactly %q", stderr.String(), wantError)
				}
				if stdout.Len() != 0 {
					t.Fatalf("refusal launch output = %s", stdout.String())
				}
				return
			}
			if !strings.Contains(stdout.String(), "\t"+wantName+"\t") {
				t.Fatalf("launch name = %q; want %q", stdout.String(), wantName)
			}
			argvFile := "cc-argv"
			if engine == "cx" {
				argvFile = "cx-argv"
			}
			argv := jail.await(t, argvFile, "--")
			if engine == "cx" {
				if !strings.Contains(argv, "developer_instructions=") || !strings.Contains(argv, "You are duo.") {
					t.Fatalf("Codex argv = %q", argv)
				}
				if !testjail.MirrorBuilt(t, mirrorRecord, "codex", duo) {
					t.Fatalf("the launch built no codex mirror of %s", duo)
				}
			} else {
				if name == "role" {
					var err error
					wantChannel, err = agentrole.SeatPromptPath(filepath.Join(jail.root, "sid"), socket, "")
					if err != nil {
						t.Fatal(err)
					}
					raw, err := os.ReadFile(wantChannel)
					want := "<!-- pfm agent-role: r -->\nYou are scribe.\n\n---\n\nROLE R"
					if err != nil || string(raw) != want {
						t.Fatalf("role prompt = %q, %v; want %q", raw, err, want)
					}
				}
				if !strings.Contains(argv, "--system-prompt-file "+wantChannel) {
					t.Fatalf("Claude argv = %q; want prompt %s", argv, wantChannel)
				}
				if wantEffort != "" && !strings.Contains(argv, "--effort "+wantEffort) {
					t.Fatalf("Claude effort argv = %q; want %s", argv, wantEffort)
				}
			}
			recorded, found, err := agentrole.ReadHarnessPromptRecord(filepath.Join(jail.root, "sid"), socket, "")
			if err != nil || found != (name == "explicit harness") || (found && recorded != wantChannel) {
				t.Fatalf("harness record = %q, %v, %v", recorded, found, err)
			}
		})
	}
}

func TestChatNewExplicitNamePreservesAnotherProducersReservation(t *testing.T) {
	jail := newRunJail(t)
	dir, _ := newWorkbenchRunFixture(t, jail)
	name, found, err := pfmchat.WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || !found {
		t.Fatal(name, found, err)
	}
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{
			"chat",
			"new",
			"--name",
			name,
			"--cwd",
			dir,
			"--prompt-file",
			filepath.Join(jail.root, "missing-prompt"),
		},
		&stdout,
		&stderr,
	)
	if code == 0 {
		t.Fatal("missing prompt unexpectedly launched")
	}
	next, _, err := pfmchat.WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err != nil || next == name {
		t.Fatalf(
			"explicit failure released another producer's claim: next=%q err=%v stderr=%q",
			next,
			err,
			stderr.String(),
		)
	}
}

// TestFirstReplyVerdict: the reply watch after a delivered launch prompt calls
// silence a chat at work, a refused turn an error naming it, and a watch that
// could not read the chat that failure — never ok.
func TestFirstReplyVerdict(t *testing.T) {
	result := spawn.Result{Socket: "cc-sock", Session: "cc-sess"}
	refused := headless.Turn{Error: "invalid_request", Answer: "API Error: 400\nbad beta"}
	readFailure := errors.New("read transcript: permission denied")
	for _, check := range []struct {
		name     string
		turn     headless.Turn
		err      error
		wantCode int
		want     []string
	}{
		{name: "replied", turn: headless.Turn{Answer: "on it"}, wantCode: 0},
		{name: "still working", err: headless.ErrAwaitTimeout, wantCode: 0},
		{
			name: "refused", turn: refused, wantCode: codeTurnError,
			want: []string{"API error (invalid_request)", "API Error: 400 bad beta", "tmux -L cc-sock attach -t cc-sess"},
		},
		{
			name: "refused then gone", turn: refused, err: headless.ErrChatGone, wantCode: codeTurnError,
			want: []string{"invalid_request"},
		},
		{name: "gone", err: headless.ErrChatGone, wantCode: codeDeadChat, want: []string{"died before replying"}},
		{name: "read failure", err: readFailure, wantCode: 1, want: []string{"could not be read", readFailure.Error()}},
		{name: "cancelled", err: context.Canceled, wantCode: 1, want: []string{context.Canceled.Error()}},
	} {
		t.Run(check.name, func(t *testing.T) {
			code, message := firstReplyVerdict("worker", check.turn, check.err, result)
			if code != check.wantCode {
				t.Fatalf("code = %d, want %d (message %q)", code, check.wantCode, message)
			}
			if check.wantCode == 0 && message != "" {
				t.Fatalf("message = %q, want none for an ok launch", message)
			}
			for _, want := range check.want {
				if !strings.Contains(message, want) {
					t.Fatalf("message = %q, want it to contain %q", message, want)
				}
			}
		})
	}
}

func TestChatNewBackendFlag(t *testing.T) {
	for _, tc := range []struct {
		name, backend, want string
		code                int
	}{
		{name: "invalid", backend: "other", want: "--backend must be auto, tmux or herdr", code: 2},
		{name: "forced unreachable", backend: "herdr", want: "pane w6:p3 unreachable", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			t.Setenv("TMUX", "")
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			t.Setenv("HERDR_SOCKET_PATH", filepath.Join(t.TempDir(), "missing.sock"))
			var stdout, stderr bytes.Buffer
			code := run(
				[]string{
					"chat",
					"new",
					"--name",
					"BACKEND",
					"--engine",
					"cc",
					"--cwd",
					jail.root,
					"--backend",
					tc.backend,
				},
				&stdout,
				&stderr,
			)
			if code != tc.code || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q, want code=%d %q",
					code,
					stdout.String(),
					stderr.String(),
					tc.code,
					tc.want,
				)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed birth printed success: %q", stdout.String())
			}
		})
	}
}

func TestInternalSeatHostDispatch(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "seat-host"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "pfm internal seat-host: expected one seat id") {
		t.Fatalf("seat-host code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestChatNewNativeBackendBirth(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	socket := filepath.Join(testjail.ShortRoot(t), "native.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	requests := make(chan string, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var request struct {
					ID     string `json:"id"`
					Method string `json:"method"`
					Params struct {
						Pane string `json:"pane_id"`
					} `json:"params"`
				}
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					return
				}
				requests <- request.Method
				pane := request.Params.Pane
				if pane == "" {
					pane = "w6:p3"
				}
				body, err := json.Marshal(testjail.HerdrPaneInfo(pane))
				if err != nil {
					t.Errorf("encode native pane fixture: %v", err)
					return
				}
				result := string(body)
				if request.Method == "ping" {
					result = `{"type":"pong","version":"0.9.5","protocol":22}`
				}
				if request.Method == "layout.apply" {
					result = `{"layout":{"workspace_id":"w6","root":{"type":"pane","pane_id":"w6:p9"}}}`
				}
				_, _ = fmt.Fprintf(conn, "{\"id\":%q,\"result\":%s}\n", request.ID, result)
			}()
		}
	}()
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"chat", "new", "--name", "NATIVE", "--engine", "cc", "--cwd", jail.root, "--backend", "herdr"},
		&stdout,
		&stderr,
	)
	if code != 0 || !strings.Contains(stdout.String(), "herdr") || strings.Contains(stdout.String(), "tmux -L") {
		t.Fatalf("native birth code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	close(requests)
	var layout int
	for method := range requests {
		if method == "layout.apply" {
			layout++
		}
	}
	if layout != 1 {
		t.Fatalf("native birth layout.apply=%d, want one", layout)
	}
}

func TestChatNameNativeDeliveryConvergesWithoutTmuxWindow(t *testing.T) {
	jailTest(t)
	var stderr bytes.Buffer
	delivered := false
	code := applyChatName(
		context.Background(),
		headless.Chat{Backend: "herdr", Socket: "/not-a-tmux-socket", Pane: "w6:p3", Engine: pfmengine.Claude},
		"INTEGRATION",
		func(_ context.Context, target headless.Chat, name string) (int, string, error) {
			delivered = target.Backend == "herdr" && name == "INTEGRATION"
			return 0, "", nil
		},
		&stderr,
	)
	if code != 0 || !delivered {
		t.Fatalf("native name code=%d delivered=%t stderr=%q", code, delivered, stderr.String())
	}
}

func TestChatNewNativeRawEngineBirth(t *testing.T) {
	for _, engine := range []string{"codex", "opencode", "opencode-workbench"} {
		t.Run(engine, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			socket := filepath.Join(testjail.ShortRoot(t), "unsupported.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			layouts := make(chan struct{}, 4)
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
								Pane string `json:"pane_id"`
							} `json:"params"`
						}
						if json.NewDecoder(conn).Decode(&req) != nil {
							return
						}
						pane := req.Params.Pane
						if pane == "" {
							pane = "w6:p3"
						}
						body, err := json.Marshal(testjail.HerdrPaneInfo(pane))
						if err != nil {
							t.Errorf("encode native pane fixture: %v", err)
							return
						}
						result := string(body)
						if req.Method == "ping" {
							result = `{"type":"pong","version":"0.9.5","protocol":22}`
						}
						if req.Method == "layout.apply" {
							layouts <- struct{}{}
							result = `{"layout":{"workspace_id":"w6","root":{"type":"pane","pane_id":"w6:p9"}}}`
						}
						_, _ = fmt.Fprintf(conn, "{\"id\":%q,\"result\":%s}\n", req.ID, result)
					}()
				}
			}()
			store := filepath.Join(jail.root, "opencode-store")
			if err := atomicfile.Write(filepath.Join(store, "auth.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(pfmengine.MustLookup(pfmengine.OpenCode).RootEnv, store)
			binary := filepath.Join(jail.root, pfmengine.MustLookup(pfmengine.OpenCode).Binary)
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", jail.root+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx := pfmchat.WithResolvedSelf(
				context.Background(),
				headless.Chat{Backend: "herdr", Socket: socket, Pane: "w6:p3", HerdrWorkspace: "w6"},
			)
			var stdout, stderr bytes.Buffer
			runtime, err := pfmconfig.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			directory, selected := jail.root, engine
			if engine == "opencode-workbench" {
				directory, _ = newWorkbenchRunFixture(t, jail)
				if err := atomicfile.Write(paths.WorkbenchManifest(directory), []byte(
					`{"prompt":"scribe.md","engines":["opencode"],"model":"openai/other","effort":"medium"}`,
				), 0o600); err != nil {
					t.Fatal(err)
				}
				testjail.FakeEngineSync(t, testjail.CleanMirrorReply, 0)
				selected = "ox"
			}
			args := []string{"new", "--engine", selected, "--name", "RAW", "--cwd", directory}
			prompt := "First task 'quoted'\nsecond line"
			if engine != "codex" {
				args = append(args, "--model", "openai/gpt-6.1-sol", "--effort", "high", prompt)
				runtime.Config.OpenCode.Binary = binary
			}
			code := runChatWithRuntime(
				args,
				nil,
				&stdout,
				&stderr,
				runtime,
				ctx,
			)
			state := fleetdb.OpenSharedState(ctx, runtime.Paths)
			defer func() { _ = state.Close() }()
			seats, seatErr := state.Seats(ctx)
			recipes, recipeErr := filepath.Glob(filepath.Join(filepath.Dir(runtime.Paths.StateDB), "seats", "*.json"))
			if code != 0 || !strings.Contains(stdout.String(), "herdr") || len(layouts) != 1 ||
				seatErr != nil || len(seats) != 1 || recipeErr != nil || len(recipes) != 1 {
				t.Fatalf(
					"native %s code=%d stdout=%q stderr=%q layouts=%d seats=%+v err=%v recipes=%v err=%v",
					engine,
					code,
					stdout.String(),
					stderr.String(),
					len(layouts),
					seats,
					seatErr,
					recipes,
					recipeErr,
				)
			}
			if engine != "codex" {
				recipe, err := state.ReadSeatRun(seats[0].SeatID)
				if err != nil {
					t.Fatal(err)
				}
				for _, word := range []string{"--model", "openai/gpt-6.1-sol", "--prompt", prompt} {
					if !slices.Contains(recipe.Args, word) {
						t.Errorf("native recipe lacks %q: %+v", word, recipe)
					}
				}
				if recipe.Binary != binary || slices.Contains(recipe.Args, "--variant") ||
					!slices.Contains(recipe.Env, "PFM_OPENCODE_MODEL=openai/gpt-6.1-sol") ||
					!slices.Contains(recipe.Env, "PFM_OPENCODE_VARIANT=high") {
					t.Errorf("native model/effort/binary recipe=%+v", recipe)
				}
				if engine == "opencode-workbench" &&
					(!slices.Contains(recipe.Env, "PFM_OPENCODE_SYSTEM_FILE="+filepath.Join(directory, ".professor", "scribe.md")) ||
						strings.Contains(strings.Join(recipe.Env, "\n"), "PFM_OPENCODE_POLICY=You are scribe.")) {
					t.Errorf("canonical persona missing or duplicated: %+v", recipe)
				}
			}
		})
	}
}

func TestReservedSeatLabel(t *testing.T) {
	for _, label := range []string{"seat:child", "SeAt:child"} {
		for _, args := range [][]string{
			{"chat", "new", "--name", label, "--backend", "invalid"},
			{"chat", "name", "missing", label},
		} {
			var out, stderr bytes.Buffer
			code := run(args, &out, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), "reserved for durable seat handles") {
				t.Errorf("%v: code=%d stderr=%s", args, code, &stderr)
			}
		}
	}
}
