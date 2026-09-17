package gather

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeRolloutMeta(t *testing.T, path, source, parent string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(
		map[string]any{
			"type":    "session_meta",
			"payload": map[string]any{"id": CodexRolloutID(path), "thread_source": source, "parent_thread_id": parent},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexHeldRootWinsOverChildFirstDescriptors(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "sessions", "rollout-parent.jsonl")
	child := filepath.Join(root, "sessions", "rollout-child.jsonl")
	writeRolloutMeta(t, parent, "user", "")
	writeRolloutMeta(t, child, "subagent", "parent")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		400: {
			cmdline: []string{"codex"},
			stat:    ProcStat{ParentPID: 100},
			fdLinks: []FDLink{{FD: 3, Target: child}, {FD: 8, Target: parent}},
		},
		401: {cmdline: []string{"codex"}, stat: ProcStat{ParentPID: 400}, fdLinks: []FDLink{{FD: 2, Target: child}}},
	}}
	panes := []ProbePane{{PID: 100, Socket: "cx-1-2-3", PaneID: "%0"}}
	live, err := DetectCodex(proc, root, panes)
	if err != nil || len(live) != 1 || live[0].ThreadID != "parent" {
		t.Fatalf("root identity=%#v err=%v", live, err)
	}
	refreshed, err := RefreshCodexHeldRollouts(proc, live, []string{root})
	if err != nil || len(refreshed) != 1 || refreshed[0].ThreadID != "parent" {
		t.Fatalf("idle root identity=%#v err=%v", refreshed, err)
	}
}

func TestCodexUnknownAndConflictingHeldMetadataCannotClaimPane(t *testing.T) {
	for _, variant := range []string{"missing", "malformed", "no-header", "only-child", "two-roots"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sessions", "rollout-root.jsonl")
			writeRolloutMeta(t, path, "user", "")
			links := []FDLink{{FD: 3, Target: path}}
			switch variant {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte("{broken\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "no-header":
				if err := os.WriteFile(path, []byte("{\"type\":\"response_item\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "only-child":
				writeRolloutMeta(t, path, "subagent", "parent")
			case "two-roots":
				other := filepath.Join(root, "sessions", "rollout-other.jsonl")
				writeRolloutMeta(t, other, "user", "")
				links = append(links, FDLink{FD: 8, Target: other})
			}
			proc := &fakeProcFS{
				processes: map[int]fakeProcess{
					100: {},
					400: {cmdline: []string{"codex"}, stat: ProcStat{ParentPID: 100}, fdLinks: links},
				},
			}
			live, err := DetectCodex(proc, root, []ProbePane{{PID: 100, Socket: "cx-1-2-3", PaneID: "%0"}})
			if err != nil || len(live) != 1 || live[0].RolloutHeld || live[0].ThreadID != "" ||
				live[0].IdentityError == "" {
				t.Fatalf("unproven root=%#v err=%v", live, err)
			}
			refreshed, err := RefreshCodexHeldRollouts(proc, live, []string{root})
			if err != nil || len(refreshed) != 1 || refreshed[0].RolloutHeld || refreshed[0].IdentityError == "" {
				t.Fatalf("idle unproven root=%#v err=%v", refreshed, err)
			}
		})
	}
}

func TestRevertedCodexRolloutKeepsStableThreadIdentity(t *testing.T) {
	root := t.TempDir()
	const id = "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(
		root,
		"sessions",
		"rollout-2026-09-05T12-00-00-"+id+"_22222222-2222-4222-8222-222222222222.jsonl",
	)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(`{"type":"session_meta","payload":{"id":"`+id+`","source":"cli"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	proc := &fakeProcFS{
		processes: map[int]fakeProcess{
			100: {},
			400: {cmdline: []string{"codex"}, stat: ProcStat{ParentPID: 100}, fdLinks: []FDLink{{FD: 3, Target: path}}},
		},
	}
	live, err := DetectCodex(proc, root, []ProbePane{{PID: 100, Socket: "cx-1-2-3", PaneID: "%0"}})
	if err != nil || len(live) != 1 || live[0].ThreadID != id || !live[0].RolloutHeld {
		t.Fatalf("reverted root=%+v err=%v", live, err)
	}
}
