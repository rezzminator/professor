package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestCodexStateFilesOrderNewestGenerationFirst(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"state_2.sqlite",
		"state_10.sqlite",
		"state_9.sqlite",
		"logs_2.sqlite",
		"state_9.sqlite-wal",
		"state_x.sqlite",
	} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := CodexStateFiles(root)
	if err != nil {
		t.Fatalf("CodexStateFiles() error = %v", err)
	}
	want := []string{
		filepath.Join(root, "state_10.sqlite"),
		filepath.Join(root, "state_9.sqlite"),
		filepath.Join(root, "state_2.sqlite"),
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("CodexStateFiles() = %#v, want %#v", files, want)
	}

	missing, err := CodexStateFiles(filepath.Join(root, "absent"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("CodexStateFiles(absent) = %#v, %v, want none", missing, err)
	}
}

// A Codex home whose every state generation is unreadable leaves the
// resolver no candidates, and says so: the pane still resolves by its id.
func TestCodexThreadResolverNamesUnreadableThreads(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state_5.sqlite"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, recorder := obs.Test(t)
	resolver := NewCodexThreadResolverRoots(ctx, []string{home}, nil)
	if id, rollout := resolver("exported", "", 0, "", ""); id != "exported" || rollout != "" {
		t.Fatalf("resolver = %q, %q; want the exported id with no rollout", id, rollout)
	}
	want := []string{"WARN: read Codex state: all 1 generation(s) were unreadable"}
	if got := warnings(recorder, "store.codex_threads.unreadable"); !slices.Equal(got, want) {
		t.Fatalf("records = %q, want %q\n%s", got, want, recorder.Raw())
	}
}

// warnings renders each record named message as "LEVEL field… : err", the
// named fields joined by spaces.
func warnings(recorder *obs.Recorder, message string, fields ...string) []string {
	var got []string
	for _, record := range recorder.Records() {
		if record.Message != message {
			continue
		}
		line := record.Level
		for _, field := range fields {
			value, _ := record.Field(field)
			line += fmt.Sprintf(" %v", value)
		}
		text, _ := record.Field(obs.FieldErr)
		got = append(got, fmt.Sprintf("%s: %v", line, text))
	}
	return got
}

// L1-F4: skipping every generation is not the same as there being nothing to
// read — an all-unusable state store must be reported as an error, never as
// an empty Codex fleet, and each skip is visible on stderr.
func TestReadCodexThreadsErrorsWhenEveryGenerationIsUnreadable(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"state_1.sqlite", "state_2.sqlite"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("not a database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := CodexStateFiles(root)
	if err != nil {
		t.Fatalf("CodexStateFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("CodexStateFiles() = %v, want 2 generations", files)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStderr := os.Stderr
	os.Stderr = writer
	threads, err := ReadCodexThreads(context.Background(), files)
	os.Stderr = previousStderr
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	logged, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	if got, want := strings.ReplaceAll(
		fmt.Sprintf("%#v | %v | %s", threads, err, logged),
		root,
		"{root}",
	), "[]store.CodexThread(nil) | read Codex state: all 2 generation(s) were unreadable | store: skip unusable Codex state generation {root}/state_2.sqlite: open Codex state store \"{root}/state_2.sqlite\": sqlite database {root}/state_2.sqlite is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles\nstore: skip unusable Codex state generation {root}/state_1.sqlite: open Codex state store \"{root}/state_1.sqlite\": sqlite database {root}/state_1.sqlite is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles\n"; got != want {
		t.Fatalf("threads | error | stderr = %q, want %q", got, want)
	}
}

// A store-only conversation is killed by its lineage root like any other, and
// the kill is permanent: new prompts and a rollout file that finally appears
// never lift it, and nothing writes a baseline to lift it with.
func TestKillStoreOnlyCodexLineageIsPermanent(t *testing.T) {
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	storeOnly := Rollout{
		ID:          "store-only",
		Path:        "/codex/sessions/rollout-store-only.jsonl",
		MTimeNS:     900 * 1_000_000_000,
		CWD:         "/work/paginated",
		UserThread:  true,
		LineageRoot: "store-only",
		FirstPrompt: "paginated conversation",
		PromptCount: 1,
	}
	neighbor := Rollout{
		ID:          "with-file",
		Path:        "/codex/sessions/rollout-with-file.jsonl",
		Size:        4096,
		MTimeNS:     800 * 1_000_000_000,
		CWD:         "/work/paginated",
		UserThread:  true,
		LineageRoot: "with-file",
		FirstPrompt: "ordinary conversation",
		PromptCount: 2,
	}
	for _, rollout := range []Rollout{storeOnly, neighbor} {
		if err := database.UpsertRollout(ctx, rollout); err != nil {
			t.Fatalf("UpsertRollout(%q) error = %v", rollout.ID, err)
		}
	}
	if err := database.Kill(ctx, Killed{
		ID:       storeOnly.ID,
		Engine:   "cx",
		KilledAt: 1,
	}); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	assertStoreOnlyKilled := func(stage string) {
		t.Helper()
		_, rollouts, counts, err := database.DefaultCandidates(ctx, 30, 15)
		if err != nil {
			t.Fatalf("%s DefaultCandidates() error = %v", stage, err)
		}
		for _, rollout := range rollouts {
			if rollout.ID == storeOnly.ID {
				t.Fatalf("%s: killed store-only lineage reappeared", stage)
			}
		}
		if counts.Killed != 1 {
			t.Fatalf("%s: killed count = %d, want 1", stage, counts.Killed)
		}
		killed, found, err := database.Killed(ctx, storeOnly.ID)
		if err != nil || !found {
			t.Fatalf("%s Killed() found = %v, error = %v", stage, found, err)
		}
		if killed.BaselinePrompts != nil {
			t.Fatalf(
				"%s: kill carries baseline %d; kills are permanent and write none",
				stage,
				*killed.BaselinePrompts,
			)
		}
	}
	assertStoreOnlyKilled("after kill")

	// The conversation gains a prompt and finally writes its rollout file.
	grown := storeOnly
	grown.PromptCount = 9
	grown.Size = 2048
	grown.MTimeNS = 1000 * 1_000_000_000
	if err := database.UpsertRollout(ctx, grown); err != nil {
		t.Fatalf("UpsertRollout(grown) error = %v", err)
	}
	assertStoreOnlyKilled("after a new prompt")
}

func TestReadCodexThreadIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, schema, values, id, source, agent, parent string
		found, bad                                      bool
	}{
		{"root", "id TEXT, thread_source TEXT, agent_path TEXT", "'root','user',NULL", "root", "user", "", "", true, false},
		{"child", "id TEXT, thread_source TEXT, agent_path TEXT", "'child','subagent','/root/worker'", "child", "subagent", "/root/worker", "", true, false},
		{"parent", "id TEXT, parent_thread_id TEXT", "'child','root'", "child", "", "", "root", true, false},
		{"old schema", "id TEXT", "'root'", "root", "", "", "", true, false},
		{"absent", "id TEXT", "'other'", "root", "", "", "", false, false},
		{"no id", "thread_source TEXT", "'subagent'", "child", "", "", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "state_5.sqlite")
			db, err := sqlitedb.OpenReadWrite(file, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec("CREATE TABLE threads (" + tc.schema + "); INSERT INTO threads VALUES (" + tc.values + ")")
			if err = errors.Join(err, db.Close()); err != nil {
				t.Fatal(err)
			}
			identity, found, err := ReadCodexThreadIdentity(context.Background(), file, tc.id)
			if (err != nil) != tc.bad || found != tc.found || identity.ThreadSource != tc.source ||
				identity.AgentPath != tc.agent || identity.ParentThreadID != tc.parent || found && identity.ID != tc.id {
				t.Fatalf("identity=%+v found=%v err=%v", identity, found, err)
			}
		})
	}
}

func TestReadCodexThreadIdentityReadFailureIsNotAbsence(t *testing.T) {
	for raw, want := range map[string]string{"not sqlite": "open Codex thread identity root in {dir}/state_5.sqlite: sqlite database {dir}/state_5.sqlite is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles", "": "read Codex thread identity schema {dir}/state_5.sqlite: unable to open database file (14)"} {
		file := filepath.Join(t.TempDir(), "state_5.sqlite")
		if raw != "" {
			if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		_, found, err := ReadCodexThreadIdentity(context.Background(), file, "root")
		if got := strings.ReplaceAll(fmt.Sprint(err), filepath.Dir(file), "{dir}"); found || got != want {
			t.Fatalf("read error = %q, want %q (found %v)", got, want, found)
		}
	}
}
