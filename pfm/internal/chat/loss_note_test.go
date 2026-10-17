package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestLossNoteUsesProducerIDsAndNeverQuotesPrompts(t *testing.T) {
	const unanswered = "your last message was not answered — re-send it"
	const agent = "a sub-agent was running and ended"
	cases := []struct {
		name       string
		engine     pfmengine.ID
		body, want string
		wantErr    bool
	}{
		{name: "empty", engine: pfmengine.Claude},
		{
			name:    "truncated trailing line",
			engine:  pfmengine.Claude,
			body:    `{"type":"user","message":{"content":"PRIVATE PROMPT"}}` + "\n" + `{"type":"assistant"`,
			want:    unanswered + "; restart loss unknown",
			wantErr: true,
		},
		{
			name:   "server blocks",
			engine: pfmengine.Claude,
			body:   `{"type":"assistant","message":{"content":[{"type":"server_tool_use","id":"w","name":"web_search"},{"type":"web_search_tool_result","tool_use_id":"w","content":[]}]}}`,
		},
		{
			name:    "future block preserves evidence",
			engine:  pfmengine.Claude,
			body:    `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Agent"},{"type":"future_block"}]}}`,
			want:    agent + "; restart loss unknown",
			wantErr: true,
		},
		{
			name:   "unanswered",
			engine: pfmengine.Claude,
			body:   `{"type":"user","message":{"content":"PRIVATE PROMPT"}}`,
			want:   unanswered,
		},
		{
			name:   "answered",
			engine: pfmengine.Claude,
			body:   `{"type":"user","message":{"content":"PRIVATE PROMPT"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"answer"}]}}`,
		},
		{
			name:   "unfinished Agent",
			engine: pfmengine.Claude,
			body:   `{"type":"assistant","message":{"content":[{"type":"text","text":"starting"},{"type":"tool_use","id":"a","name":"Agent","input":{"description":"PRIVATE PROMPT"}}]}}`,
			want:   agent,
		},
		{
			name:   "completed multiblock",
			engine: pfmengine.Claude,
			body:   `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Agent"},{"type":"tool_use","id":"b","name":"Task"}]}}` + "\n" + `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b","content":"ok"},{"type":"tool_result","tool_use_id":"a","content":"ok"}]}}`,
		},
		{
			name:   "wrong result id",
			engine: pfmengine.Claude,
			body:   `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Task"}]}}` + "\n" + `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"other","content":"ok"}]}}`,
			want:   agent,
		},
		{
			name:   "both",
			engine: pfmengine.Claude,
			body:   `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"Agent"}]}}` + "\n" + `{"type":"user","message":{"content":"PRIVATE PROMPT"}}`,
			want:   unanswered + "; " + agent,
		},
		{
			name:   "sidechain ignored",
			engine: pfmengine.Claude,
			body:   `{"type":"user","isSidechain":true,"message":{"content":"PRIVATE PROMPT"}}`,
		},
		{
			name:    "missing agent id",
			want:    "restart loss unknown",
			engine:  pfmengine.Claude,
			body:    `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Agent"}]}}`,
			wantErr: true,
		},
		{
			name:    "malformed",
			want:    "restart loss unknown",
			engine:  pfmengine.Claude,
			body:    `{"type":"user","message":{"content":"PRIVATE PROMPT"}`,
			wantErr: true,
		},
		{
			name:    "unknown content",
			want:    "restart loss unknown",
			engine:  pfmengine.Claude,
			body:    `{"type":"user","message":{"content":42}}`,
			wantErr: true,
		},
		{
			name:   "codex unanswered",
			engine: pfmengine.Codex,
			body:   `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"PRIVATE PROMPT"}]}}`,
			want:   unanswered,
		},
		{
			name:   "codex answered",
			engine: pfmengine.Codex,
			body:   `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"PRIVATE PROMPT"}]}}` + "\n" + `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}}`,
		},
		{
			name:    "codex malformed payload",
			want:    "restart loss unknown",
			engine:  pfmengine.Codex,
			body:    `{"type":"response_item","payload":42}`,
			wantErr: true,
		},
		{name: "opencode unsupported visible", engine: pfmengine.OpenCode, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.jsonl")
			if err := os.WriteFile(path, []byte(tc.body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			note, err := lossNote(context.Background(), path, tc.engine)
			if note != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("loss note=%q err=%v want=%q error=%v", note, err, tc.want, tc.wantErr)
			}
			if strings.Contains(note, "PRIVATE PROMPT") ||
				(err != nil && strings.Contains(err.Error(), "PRIVATE PROMPT")) {
				t.Fatal("loss output quoted prompt text")
			}
		})
	}
}

func TestLossNoteReadsOpenCodeProducerSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, role, part, want string
		wantErr                bool
	}{
		{name: "unanswered", role: "user", want: "your last message was not answered — re-send it"},
		{name: "message chronology", role: "user", want: "your last message was not answered — re-send it"},
		{name: "answered", role: "assistant"},
		{name: "running subagent", role: "assistant", part: `{"type":"tool","callID":"agent1","tool":"task","state":{"status":"running","input":{"prompt":"PRIVATE PROMPT"}}}`, want: "a sub-agent was running and ended"},
		{name: "completed subagent", role: "assistant", part: `{"type":"tool","callID":"agent1","tool":"task","state":{"status":"completed"}}`},
		{name: "unknown role", role: "mystery", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			db, err := sqlitedb.OpenStore(ctx, index.OpenCodeStorePath(root))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			_, err = db.Exec(
				`CREATE TABLE session(id TEXT,parent_id TEXT,title TEXT,directory TEXT,time_created INT,time_updated INT); CREATE TABLE message(id TEXT,session_id TEXT,time_created INT,time_updated INT,data TEXT); CREATE TABLE part(id TEXT,message_id TEXT,session_id TEXT,time_created INT,time_updated INT,data TEXT); INSERT INTO session VALUES('session',NULL,'fixture','/work',1,1);`,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(
				`INSERT INTO message VALUES('message','session',1,1,?)`,
				`{"role":"`+tc.role+`"}`,
			); err != nil {
				t.Fatal(err)
			}
			if tc.name == "message chronology" {
				if _, err := db.Exec(
					`INSERT INTO message VALUES('earlier','session',0,0,'{"role":"assistant"}')`,
				); err != nil {
					t.Fatal(err)
				}
			}
			if tc.part != "" {
				if _, err := db.Exec(`INSERT INTO part VALUES('part','message','session',1,1,?)`, tc.part); err != nil {
					t.Fatal(err)
				}
			}
			note, err := lossOpenCode(ctx, root, "session")
			if note != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("loss note=%q err=%v want=%q error=%t", note, err, tc.want, tc.wantErr)
			}
			if strings.Contains(note, "PRIVATE PROMPT") {
				t.Fatal("loss note quoted prompt")
			}
		})
	}
}
