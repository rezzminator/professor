package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// assertNoLeakedTempFiles is Ask's cleanup pin (see the package doc comment
// on Ask): there are seven returns between file creation and cleanup, and a
// leaked file under directory is the failure this catches.
func assertNoLeakedTempFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read prepared temp dir %s: %v", directory, err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		t.Fatalf("leaked prepared temp files in %s: %v", directory, names)
	}
}

// TestAskPaysRunnerEveryCallNeverCaches pins Ask's central invariant (see the
// doc comment on AskOptions): unlike Summarize, Ask never consults or writes
// a cache, because a pane moves every second and a cached answer would be a
// confidently stale claim about a chat that has moved on.
func TestAskCapturesHerdrSeatThroughNativeHost(t *testing.T) {
	root, _ := summaryTestStore(t)
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	// Keep the native socket short enough for the Unix sun_path limit.
	socketRoot, err := os.MkdirTemp(paths.TempRoot(), "headless-herdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketRoot); err != nil {
			t.Error(err)
		}
	})
	socket := filepath.Join(socketRoot, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			func() {
				defer func() {
					if err := conn.Close(); err != nil {
						t.Errorf("close native fixture: %v", err)
					}
				}()
				var request struct {
					ID     string `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					t.Errorf("decode native request: %v", err)
					return
				}
				var result any
				switch request.Method {
				case "pane.list":
					result = map[string]any{"type": "pane_list", "panes": []any{map[string]any{
						"pane_id": "w1:p2", "workspace_id": "w1", "terminal_id": "stable",
					}}}
				case "pane.process_info":
					result = map[string]any{"type": "pane_process_info", "process_info": map[string]any{
						"pane_id": "w1:p2", "shell_pid": 41,
					}}
				case "pane.read":
					result = map[string]any{"type": "pane_read", "read": map[string]any{
						"pane_id": "w1:p2", "text": "native capture is running checks", "truncated": false,
					}}
				default:
					t.Errorf("unexpected native request %q", request.Method)
				}
				if err := json.NewEncoder(conn).Encode(map[string]any{"id": request.ID, "result": result}); err != nil {
					t.Errorf("reply to native request: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})

	t.Setenv(paths.EnvProcRoot, filepath.Join(root, "proc"))
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := db.PutSeat(context.Background(), fleetdb.Seat{
		SeatID: "capture-seat", Engine: "cc", Backend: "herdr", State: "live",
		HerdrSocket: socket, HerdrPane: "w1:p2", HerdrWorkspace: "w1", HerdrTerminalID: "stable",
		SeatHostPID: 42, SeatHostStart: 100,
	}); err != nil {
		t.Fatal(err)
	}
	for pid, parent := range map[int]int{41: 1, 42: 41} {
		dir := filepath.Join(values.ProcRoot, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
		fields[1] = strconv.Itoa(parent)
		argv := "/bin/sh\x00"
		if pid == 42 {
			argv = "/fixture/pfm\x00internal\x00seat-host\x00capture-seat\x00"
		}
		for name, body := range map[string]string{
			"stat": fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " ")), "cmdline": argv,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(values.ProcRoot, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "claude")
	runner := fakeEngine(deps.StartOutput{Stdout: `{"result":"native capture is running checks"}` + "\n"})
	t.Setenv("PATH", bin)
	options := AskOptions{Config: summaryMachine(t, "claude"), TempDir: filepath.Join(root, "prepared"), Runner: runner}
	chat := Chat{
		Name: "native seat", Engine: pfmengine.Claude, Backend: "herdr", SeatID: "capture-seat",
		HerdrWorkspace: "w1", Socket: socket, Pane: "w1:p2", Live: true,
	}
	result := Ask(context.Background(), chat, options)
	if got, want := result.Text, "PANE-ONLY (no human exchange recorded yet): native capture is running checks"; result.Warning != nil ||
		got != want {
		t.Fatalf("native status ask = %q, want %q (warning %v)", got, want, result.Warning)
	}
	if raw := startedStdin(t, runner, 0); !strings.Contains(raw, "native capture is running checks") {
		t.Fatalf("runner missing native capture: %q", raw)
	}
	assertNoLeakedTempFiles(t, options.TempDir)
}

func TestAskPaysRunnerEveryCallNeverCaches(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what is happening"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"steady state"}}`,
	)
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "claude")
	runner := fakeEngine(deps.StartOutput{Stdout: `{"result":"nothing has changed"}` + "\n"})
	t.Setenv("PATH", bin)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	options := AskOptions{Config: summaryMachine(t, "claude"), TempDir: tempDir, Runner: runner}
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath, Live: false}

	first := Ask(context.Background(), chat, options)
	if !strings.HasPrefix(first.Text, "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): ") ||
		first.Warning != nil {
		t.Fatalf("first ask=%+v", first)
	}
	assertNoLeakedTempFiles(t, tempDir)

	second := Ask(context.Background(), chat, options)
	if second.Text != first.Text || second.Warning != nil {
		t.Fatalf("second ask text drifted: first=%q second=%q", first.Text, second.Text)
	}
	assertNoLeakedTempFiles(t, tempDir)

	if calls := len(runner.Starts()); calls != 2 {
		t.Fatalf("runner calls=%d, want both calls paid (Ask never caches)", calls)
	}
}

func TestAskPreservesAnswerWhenPreparedExchangeCleanupFails(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"status?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"steady state"}}`,
	)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "claude")
	runner := fakeEngine(deps.StartOutput{
		AtStart: removePreparedExchange(t, tempDir),
		Stdout:  `{"result":"cleanup-resistant answer"}` + "\n",
	})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := Ask(context.Background(), Chat{Name: "seat", Engine: "cc", Path: transcriptPath}, AskOptions{
		Config: summaryMachine(t, "claude"), TempDir: tempDir, Runner: runner,
	})
	if result.Text != "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): cleanup-resistant answer" ||
		strings.HasPrefix(result.Text, "failed (") {
		t.Fatalf("ask lost its computed answer after cleanup failure: %+v", result)
	}
	if !errors.Is(result.Warning, fs.ErrNotExist) {
		t.Fatalf("ask cleanup warning=%v, want fs.ErrNotExist", result.Warning)
	}
}

// TestAskUnavailableNamesBothReasons pins the fourth outcome: when NEITHER
// probe produced content, the text names both reasons instead of silently
// returning an empty answer indistinguishable from a quiet chat.
func TestAskUnavailableNamesBothReasons(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// An empty PATH: if Ask ever reached the runner on this path, the
	// runner would fail loudly (no such binary) rather than silently
	// succeed, so a mistaken engine call cannot masquerade as this case.
	t.Setenv("PATH", bin)
	tempDir := filepath.Join(root, "tmp")
	options := AskOptions{Config: summaryMachine(t, "claude"), TempDir: tempDir}
	chat := Chat{Name: "seat", Engine: "cc", Path: "", Live: false}

	result := Ask(context.Background(), chat, options)
	want := "unavailable (chat is not live: there is no pane to capture; no human exchange recorded yet)"
	if result.Text != want {
		t.Fatalf("unavailable ask=%+v, want text=%q", result, want)
	}
	assertNoLeakedTempFiles(t, tempDir)
}

// TestAskReportsEngineBinaryMissingAndCleansUpOnEveryErrorReturn pins the
// fifth outcome (a configured engine binary absent from PATH reads as
// MISSING, not as a generic failure) and, across its four calls, exercises
// four of the seven returns between file creation and cleanup: engine
// binary MISSING, Config.DefaultEngine() failure, a crashed runner, and the
// happy path. Every one of them must leave TempDir empty.
func TestAskReportsEngineBinaryMissingAndCleansUpOnEveryErrorReturn(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"status?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"answer"}}`,
	)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath, Live: false}

	machine := summaryMachine(t, "codex")
	machine.Codex.Binary = "absent-codex"
	missing := Ask(context.Background(), chat, AskOptions{Config: machine, TempDir: tempDir})
	if missing.Text != "unavailable (codex binary MISSING)" {
		t.Fatalf("missing ask=%+v", missing)
	}
	assertNoLeakedTempFiles(t, tempDir)

	noEngine := Ask(context.Background(), chat, AskOptions{Config: pfmconfig.Config{}, TempDir: tempDir})
	if !strings.HasPrefix(noEngine.Text, "failed (") || !strings.Contains(noEngine.Text, "no engines configured") {
		t.Fatalf("no-engine ask=%+v", noEngine)
	}
	assertNoLeakedTempFiles(t, tempDir)

	writeEngine(t, bin, "claude")
	failed := Ask(context.Background(), chat, AskOptions{
		Config: summaryMachine(t, "claude"), TempDir: tempDir,
		Runner: fakeEngine(deps.StartOutput{Stderr: "provider crashed\n", ExitCode: 9}),
	})
	if !strings.HasPrefix(failed.Text, "failed (") || !strings.Contains(failed.Text, "provider crashed") {
		t.Fatalf("failed ask=%+v", failed)
	}
	assertNoLeakedTempFiles(t, tempDir)

	ok := Ask(context.Background(), chat, AskOptions{
		Config: summaryMachine(t, "claude"), TempDir: tempDir,
		Runner: fakeEngine(deps.StartOutput{Stdout: `{"result":"all clear"}` + "\n"}),
	})
	if !strings.HasPrefix(ok.Text, "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): ") {
		t.Fatalf("ok ask=%+v", ok)
	}
	assertNoLeakedTempFiles(t, tempDir)
}
