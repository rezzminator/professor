package hookentry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type clearNameFailWriter struct{}

func (clearNameFailWriter) Write([]byte) (int, error) { return 0, errors.New("name output failed") }
func TestClearNameFailureIsVisibleAndRetryable(t *testing.T) {
	root := testjail.Fleet(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	fresh, err := naming.ClearSuccessorName("Desk", id)
	if err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() { _ = state.Close() }()
	if err := state.PutClearName(
		context.Background(),
		id,
		naming.ClearName{Inherited: "Desk", Name: fresh},
		1,
	); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"UserPromptSubmit","session_id":"` + id + `"}`
	var stderr bytes.Buffer
	if code := ClearName(
		nil,
		strings.NewReader(payload),
		clearNameFailWriter{},
		&stderr,
		runtime,
	); code != 0 ||
		!strings.Contains(stderr.String(), "name output failed") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	got, _, err := state.ClearName(context.Background(), id)
	if err != nil || got.Emitted {
		t.Fatalf("failed write consumed pending title: %+v,%v", got, err)
	}
	runtime.Paths.StateDB = filepath.Join(root, "missing", "state.db")
	for _, payload := range []string{`{`, `{"hook_event_name":"SessionStart","source":"clear","session_id":"bad"}`} {
		stderr.Reset()
		ClearName(nil, strings.NewReader(payload), &bytes.Buffer{}, &stderr, runtime)
		if stderr.Len() == 0 {
			t.Fatalf("failure silent: %s", payload)
		}
	}
}

func TestClearNameStartIdempotentAndUserRenameWins(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "manual rename"}[manual], func(t *testing.T) {
			root := testjail.Fleet(t)
			runtime, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			const id = "22222222-2222-4222-8222-222222222222"
			path := filepath.Join(root, "claude", id+".jsonl")
			start := clearNamePayload{
				Event:     clearNameStartEvent,
				Source:    "clear",
				SessionID: id,
				Title:     "Desk",
				Path:      path,
			}
			var out bytes.Buffer
			for range 2 {
				if err := clearNameEvent(context.Background(), start, &out, runtime); err != nil {
					t.Fatal(err)
				}
			}
			if manual {
				if err := os.WriteFile(
					path,
					[]byte(`{"type":"agent-name","agentName":"Manual"}`+"\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			prompt := clearNamePayload{Event: "UserPromptSubmit", SessionID: id, Path: path}
			if err := clearNameEvent(context.Background(), prompt, &out, runtime); err != nil {
				t.Fatal(err)
			}
			if (out.Len() == 0) != manual {
				t.Fatalf("manual=%v output=%q", manual, out.String())
			}
			out.Reset()
			if err := clearNameEvent(context.Background(), prompt, &out, runtime); err != nil || out.Len() != 0 {
				t.Fatalf("retitled: %s err=%v", &out, err)
			}
		})
	}
}

func TestClearNameIgnoresUnmanagedLifecycleEvents(t *testing.T) {
	testjail.Fleet(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	for _, fields := range []string{
		`"hook_event_name":"SessionStart","source":"startup"`,
		`"hook_event_name":"SessionStart","source":"resume"`,
		`"hook_event_name":"SessionStart","source":"clear","agent_id":"child"`,
		`"hook_event_name":"UserPromptSubmit"`,
		`"hook_event_name":"UserPromptSubmit","agent_id":"child"`,
		`"hook_event_name":"SessionEnd","source":"clear"`,
	} {
		var out, stderr bytes.Buffer
		payload := `{` + fields + `,"session_id":"` + id + `","session_title":"Desk"}`
		if code := ClearName(nil, strings.NewReader(payload), &out, &stderr, runtime); code != 0 ||
			out.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("unmanaged payload %s: code=%d out=%s stderr=%s", payload, code, &out, &stderr)
		}
	}
	state := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() { _ = state.Close() }()
	if name, found, err := state.ClearName(context.Background(), id); err != nil || found {
		t.Fatalf("unmanaged lifecycle reserved %+v found=%v err=%v", name, found, err)
	}
}

func TestClearNamePromptBoundsHeldConversationLock(t *testing.T) {
	oldDeadline := promptHookDeadline
	promptHookDeadline = 500 * time.Millisecond
	t.Cleanup(func() { promptHookDeadline = oldDeadline })
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "pending"}[pending], func(t *testing.T) {
			testjail.Fleet(t)
			runtime, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			const id = "22222222-2222-4222-8222-222222222222"
			ctx := context.Background()
			state := fleetdb.OpenSharedState(ctx, runtime.Paths)
			defer func() { _ = state.Close() }()
			if pending {
				name := naming.ClearName{Inherited: "Desk", Name: "Desk #2"}
				if err := state.PutClearName(ctx, id, name, 1); err != nil {
					t.Fatal(err)
				}
			}
			unlock, err := fleetdb.LockSeat(ctx, runtime.Paths, id)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unlock() }()
			var out, stderr bytes.Buffer
			done := make(chan int, 1)
			go func() {
				payload := `{"hook_event_name":"UserPromptSubmit","session_id":"` + id + `"}`
				done <- ClearName(nil, strings.NewReader(payload), &out, &stderr, runtime)
			}()
			bound, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			select {
			case code := <-done:
				if code != 0 || out.Len() != 0 || (stderr.Len() != 0) != pending {
					t.Fatalf("pending=%v code=%d out=%s stderr=%s", pending, code, &out, &stderr)
				}
				if pending && !strings.Contains(stderr.String(), "context deadline exceeded") {
					t.Fatalf("lock deadline was not visible: %s", &stderr)
				}
			case <-bound.Done():
				t.Fatal("prompt waited on the conversation lock beyond its own deadline")
			}
		})
	}
}

func TestClearNameStateWriteFailureIsVisible(t *testing.T) {
	root := testjail.Fleet(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.Paths.StateDB = filepath.Join(blocker, "state.db")
	var out, stderr bytes.Buffer
	payload := `{"hook_event_name":"SessionStart","source":"clear","session_id":"22222222-2222-4222-8222-222222222222","session_title":"Desk"}`
	if code := ClearName(
		nil,
		strings.NewReader(payload),
		&out,
		&stderr,
		runtime,
	); code != 0 || out.Len() != 0 ||
		!strings.Contains(stderr.String(), "NOT named/titled") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestClearNameLookupFailureIsVisibleWithoutGuessing(t *testing.T) {
	testjail.Fleet(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	cache, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sqlitedb.OpenStore(context.Background(), runtime.Paths.CacheDB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec("ALTER TABLE cx_names RENAME TO unavailable_names"); err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	payload := `{"hook_event_name":"SessionStart","source":"clear","session_id":"` + id + `","session_title":"Desk"}`
	var out, stderr bytes.Buffer
	code := ClearName(nil, strings.NewReader(payload), &out, &stderr, runtime)
	if code != 0 || out.Len() != 0 || !strings.Contains(stderr.String(), "look up occupied clear names") ||
		!strings.Contains(stderr.String(), "NOT named/titled") {
		t.Fatalf("lookup failure code=%d out=%s stderr=%s", code, &out, &stderr)
	}
	state := fleetdb.OpenSharedState(context.Background(), runtime.Paths)
	defer func() { _ = state.Close() }()
	if name, found, err := state.ClearName(context.Background(), id); err != nil || found {
		t.Fatalf("failed lookup guessed name=%+v found=%v err=%v", name, found, err)
	}
}
