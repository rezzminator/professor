package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// THE BUG. `pfm kill <member-id>` addressed by a Codex lineage member's
// own id records the kill on that raw id when the member is not yet
// indexed — a live process holding its rollout file open (the normal shape
// right after `codex resume`) is exactly what vouches a non-empty engine
// without the index ever having parsed the file's session_id link back to
// its root. `pfm killed` reads that raw record with no reindex in
// between (unlike `ls`, which always reindexes first and would silently
// self-heal this the moment the member's file gets parsed) — so it is the
// one CLI surface that can observe the wrong id actually landing.
func TestKillCLIResolvesUnindexedLineageMemberToRoot(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)

	socket := "cc-killcli-lineage-" + strconv.Itoa(os.Getpid())
	session := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := session.CombinedOutput(); err != nil {
		t.Fatalf("start background pane: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	paneOutput, err := exec.Command(
		"tmux", "-L", socket, "list-panes", "-F", "#{pane_pid}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(paneOutput)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", paneOutput, err)
	}

	const (
		rootID   = "e6666666-6666-4666-8666-666666666666"
		memberID = "e7777777-7777-4777-8777-777777777777"
	)
	sessions := filepath.Join(jail.root, "codex", "sessions", "2026", "08", "12")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(
		sessions, "rollout-2026-08-12T00-00-00-"+rootID+".jsonl",
	)
	memberPath := filepath.Join(
		sessions, "rollout-2026-08-12T00-05-00-"+memberID+".jsonl",
	)
	writeLineageJSONL(t, rootPath, map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":            rootID,
			"thread_source": "user",
			"cwd":           "/work/lineage",
		},
	}, map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "root prompt"},
			},
		},
	})

	// Seed and index the root ALONE — the member's file does not exist yet,
	// exactly the moment before a `codex resume` writes it.
	var indexOut, indexErr bytes.Buffer
	if code := run([]string{"index", "--full"}, &indexOut, &indexErr); code != 0 {
		t.Fatalf("index --full code=%d stderr=%q", code, indexErr.String())
	}

	// The resumed member: its session_id link back to root lives in the
	// file's own header, unindexed.
	writeLineageJSONL(t, memberPath, map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":            memberID,
			"session_id":    rootID,
			"thread_source": "user",
			"cwd":           "/work/lineage",
		},
	}, map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": "resumed prompt"},
			},
		},
	})

	// A live Codex process holding the member's rollout file open — the FD
	// walk gather.DetectCodexThreads performs for a REAL codex process.
	pid := 90201
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid:       pid,
		parentPID: panePID,
		comm:      "codex",
		cmdline:   []string{"/usr/local/bin/codex"},
		withFD:    true,
	})
	fdDir := filepath.Join(jail.procRoot, strconv.Itoa(pid), "fd")
	if err := os.Symlink(memberPath, filepath.Join(fdDir, "5")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", memberID}, &stdout, &stderr); code != 0 {
		t.Fatalf("kill %s code=%d stdout=%q stderr=%q", memberID, code, stdout.String(), stderr.String())
	}

	var killedOut, killedErr bytes.Buffer
	if code := run([]string{"ls", "--killed"}, &killedOut, &killedErr); code != 0 {
		t.Fatalf("killed code=%d stderr=%q", code, killedErr.String())
	}
	got := killedOut.String()
	if !strings.HasPrefix(got, rootID+"\t") {
		t.Fatalf(
			"kill %s recorded = %q, want the lineage root %q, not the member's own unindexed id",
			memberID,
			got,
			rootID,
		)
	}
}

func writeLineageJSONL(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
