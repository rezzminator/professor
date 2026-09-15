package kill

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

// THE BUG. A resumed Codex lineage member's rollout file lands on disk the
// moment `codex resume` starts it, but the CLI's own kill path deliberately
// never runs the indexer first (fleet.ResolveRow's comment: "a
// caller resolving one id for a kill has no business reconciling the whole
// filesystem index"). --self identifies the live pane by the rollout file
// its FD holds (identifyCodexSelf), extracts an id from the FILENAME alone
// (dataID) — the member's own — and then asks database.CodexLineage for its
// root, which can only answer from what is ALREADY indexed. An unindexed
// member's session_id link exists only in the file's own header, so
// CodexLineage reports "not found" and the kill lands on the member's own
// id: the id the picker's composed row (keyed on the lineage ROOT) never
// carries, so the kill never suppresses it.
func TestSelfKillOnUnindexedLineageMemberResolvesToRoot(t *testing.T) {
	for _, suffix := range []string{"", "_cccccccc-cccc-4ccc-8ccc-cccccccccccc"} {
		t.Run("rollout"+suffix, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			defer func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			}()
			ctx := context.Background()

			rootID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			memberID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			rootPath := filepath.Join(
				jail.codexRoot, "sessions", "rollout-2026-07-27T10-00-00-"+rootID+".jsonl",
			)
			memberPath := filepath.Join(
				jail.codexRoot, "sessions", "rollout-2026-07-27T10-05-00-"+memberID+suffix+".jsonl",
			)

			// The root: an ordinary already-indexed conversation, exactly what the
			// picker composes its row from today.
			if err := database.UpsertRollout(ctx, store.Rollout{
				ID:          rootID,
				Path:        rootPath,
				Size:        100,
				MTimeNS:     100,
				CWD:         "/work/proja",
				UserThread:  true,
				FirstPrompt: "start",
				PromptCount: 1,
			}); err != nil {
				t.Fatal(err)
			}
			// The member: `codex resume <root>` just wrote this file — its
			// session_id link back to root lives in the file's own header, but
			// nothing has indexed it yet, so the fleet database knows nothing about
			// it at all.
			writeTestFile(
				t,
				memberPath,
				`{"type":"session_meta","payload":{"id":"`+memberID+
					`","thread_source":"user","session_id":"`+rootID+`","cwd":"/work/proja"}}`+"\n"+
					`{"type":"event_msg","payload":{"type":"user_message","message":"resumed"}}`+"\n",
			)

			proc := &fakeProc{
				pids:    []int{40},
				cmdline: map[int][]string{40: {"codex"}},
				links: map[int][]gather.FDLink{
					40: {{FD: 5, Target: memberPath}},
				},
				stats:   map[int]gather.ProcStat{40: {ParentPID: 11}},
				environ: map[int]map[string]string{},
			}
			manager, err := New(database, Dependencies{
				ProcFS:  proc,
				Tmux:    &fakeTmux{panePID: 11},
				Spawner: &captureSpawner{},
				Now:     func() time.Time { return time.Unix(50, 0) },
			})
			if err != nil {
				t.Fatal(err)
			}

			target, err := manager.Kill(ctx, Request{
				Self: true,
				Environment: SelfEnvironment{
					TMUX:     filepath.Join(jail.tmuxDir, "cx-800-1-1") + ",6,0",
					TMUXPane: "%9",
				},
			})
			if err != nil {
				t.Fatalf("self-kill on an unindexed lineage member: %v", err)
			}
			if target.ID != rootID {
				t.Fatalf(
					"self-kill target = %#v, want the lineage root %q, not the member's own unindexed id",
					target,
					rootID,
				)
			}

			// The picker's own source of truth: a compose pass over the root alone
			// (the member is still unindexed, exactly as the picker would see it a
			// moment after the kill) must not list the root row.
			killed, err := database.KilledChats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			output := compose.Compose(compose.Input{
				Rollouts: []store.Rollout{{
					ID: rootID, Path: rootPath, UserThread: true,
					FirstPrompt: "start", PromptCount: 1,
				}},
				Killed:  killed,
				Options: compose.Options{View: compose.DefaultView},
			})
			for _, row := range output.Rows {
				if row.ID == rootID {
					t.Fatalf("root row is still listed after the self-kill: %#v", row)
				}
			}
		})
	}
}
