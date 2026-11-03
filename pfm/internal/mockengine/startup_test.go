package mockengine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func waitStartupBirth(t *testing.T, path string) processBirth {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		var birth processBirth
		if err == nil && json.Unmarshal(raw, &birth) == nil {
			return birth
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: birth receipt unavailable bytes=%q err=%v", path, raw, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReadyHoldPrecedesNativeSessionAndTrust(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "ready")
	started := filepath.Join(fix.recordDir, "session-start")
	writeHookSettings(
		t,
		filepath.Join(fix.configDir, "settings.json"),
		map[string]any{hookSessionStart: commandHook(`printf started > "` + started + `"`)},
	)
	fix.write(Scenario{SessionID: fixtureSession, ReadyHold: gate, TrustDialogUnnumbered: true})
	session := fix.startTUI(engineClaude, nil, nil)
	birthPath := filepath.Join(fix.recordDir, "birth.json")
	birth := waitStartupBirth(t, birthPath)
	stat, err := gather.NewProcFS("").Stat(os.Getpid())
	if err != nil || birth.PID != os.Getpid() || birth.Start != stat.StartTime || birth.CWD != fix.work {
		t.Fatalf("%s: birth=%+v stat=%+v err=%v", birthPath, birth, stat, err)
	}
	if frame := session.out.frame(); frame != "" {
		t.Fatalf("held frame=%q, want no readiness render", frame)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("held SessionStart file=%v, want absent", err)
	}
	presencePath := filepath.Join(fix.configDir, "sessions", strconv.Itoa(birth.PID)+".json")
	if _, err := os.Stat(presencePath); !os.IsNotExist(err) {
		t.Fatalf("%s: held native session stat=%v, want absent", presencePath, err)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	frame := session.waitFrame("unnumbered trust after readiness", pfmengine.ClaudeTrustDialog)
	if got := readFile(t, started); got != "started" {
		t.Fatalf("%s: hook=%q want started", started, got)
	}
	var presence struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal([]byte(readFile(t, presencePath)), &presence); err != nil {
		t.Fatal(err)
	}
	if presence.SessionID != fixtureSession {
		t.Fatalf("%s: released session=%q want=%q", presencePath, presence.SessionID, fixtureSession)
	}
	path := filepath.Join(filepath.Dir(hookCapturePath(t)), "shapes", "claude", "trust-unnumbered.txt")
	want := readFile(t, path)
	actual := strings.ReplaceAll(strings.ReplaceAll(frame, fix.work, "/fixture/atlas"), "\r\n", "\n")
	if actual != want {
		t.Fatalf("%s: trust frame=%q want=%q", path, actual, want)
	}
	session.typeRaw("\x1b")
	if code := session.waitExit(); code != trustDialogDeclined {
		t.Fatalf("exit=%d want=%d", code, trustDialogDeclined)
	}
	recorded := readFile(t, filepath.Join(fix.recordDir, "trust-frame.txt"))
	if got := strings.ReplaceAll(
		strings.ReplaceAll(recorded, fix.work, "/fixture/atlas"),
		"\r\n",
		"\n",
	); got != "\x1b[2J\x1b[H"+want {
		t.Fatalf("recorded trust frame=%q, want ANSI prefix plus golden=%q", got, want)
	}
	order := readFile(t, filepath.Join(fix.recordDir, "startup.jsonl"))
	wantOrder := fmt.Sprintf(
		"{\"pid\":%d,\"event\":\"SessionStart-completed\"}\n{\"pid\":%d,\"event\":\"trust-frame-written\"}\n",
		birth.PID,
		birth.PID,
	)
	if order != wantOrder {
		t.Fatalf("startup ordering=%q, want %q", order, wantOrder)
	}
}

func TestReadyHoldTimeoutAndBootExit(t *testing.T) {
	for _, row := range []struct {
		name    string
		release bool
		exit    *int
		want    int
	}{
		{"timeout", false, nil, ExitUsage}, {"boot-exit", true, intPtr(17), 17},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			gate := filepath.Join(fix.root, "ready")
			if row.release {
				if err := os.WriteFile(gate, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fix.write(
				Scenario{SessionID: fixtureSession, ReadyHold: gate, ReadyTimeoutMS: 30, StartupExitCode: row.exit},
			)
			code, stdout, stderr := runOnce(fix, engineClaude, nil, "")
			if code != row.want || stdout != "" {
				t.Fatalf("startup exit=%d stdout=%q stderr=%q want=%d/no output", code, stdout, stderr, row.want)
			}
			wantStderr := ""
			if !row.release {
				wantStderr = "mock-engine: wait startup gate " + gate + ": context deadline exceeded\n"
			}
			if stderr != wantStderr {
				t.Fatalf("startup stderr=%q want=%q", stderr, wantStderr)
			}
			birth := waitStartupBirth(t, filepath.Join(fix.recordDir, "birth.json"))
			if birth.PID != os.Getpid() {
				t.Fatalf("birth pid=%d want=%d", birth.PID, os.Getpid())
			}
			if _, err := os.Stat(fix.claudeTranscript(fixtureSession)); !os.IsNotExist(err) {
				t.Fatalf("startup transcript stat=%v want absent", err)
			}
		})
	}
}

func TestReadyHoldContextCancellationNamesGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "gate")
	want := "wait startup gate " + path + ": context canceled"
	if err := waitStartupPath(ctx, path, time.Second); err == nil || err.Error() != want {
		t.Fatalf("cancel error=%v want=%q", err, want)
	}
}
