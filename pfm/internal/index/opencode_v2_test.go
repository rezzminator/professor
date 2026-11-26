package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// openCodeStoreFixtures are the store layouts every reader test runs over: one
// history (sessions, prompts, answers) in each schema, and the part ids each
// gives ses_root's two prompts (2.x keeps no part id, so the door names them).
var openCodeStoreFixtures = []struct {
	schema string
	seed   func(t *testing.T, root string)
	parts  string
}{
	{"1.x", seedOpenCodeStore, "i1,i2"},
	{"2.x", seedOpenCodeStoreV2, "m1/0,m2/0"},
	{"2.x beside emptied 1.x tables", seedOpenCodeStoreV2BesideV1, "m1/0,m2/0"},
}

// seedOpenCodeStoreV2BesideV1 is a store an upgrade migrated: the 2.x history, with
// the 1.x tables still present and holding none of it.
func seedOpenCodeStoreV2BesideV1(t *testing.T, root string) {
	t.Helper()
	seedOpenCodeStoreV2(t, root)
	mutateOpenCodeStore(t, root, `CREATE TABLE session (id TEXT, project_id TEXT, parent_id TEXT, title TEXT,
  directory TEXT, time_created INTEGER, time_updated INTEGER, time_archived INTEGER);
CREATE TABLE message (id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
CREATE TABLE part (id TEXT, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER,
  data TEXT);`)
}

// seedOpenCodeStoreV2 builds the OpenCode 2.x store shape from its published schema
// (anomalyco/opencode packages/core/src/database/schema.gen.ts) holding the history
// seedOpenCodeStore holds: a session's current agent and model on the session row, a
// prompt's text on its message, an answer's model, tokens and cost on its message.
func seedOpenCodeStoreV2(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir opencode root: %v", err)
	}
	db, err := sqlitedb.OpenReadWrite(filepath.Join(root, "opencode.db"), 5*time.Second)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	}()
	script := `
CREATE TABLE project (
  id text PRIMARY KEY, worktree text NOT NULL, vcs text, name text, icon_url text,
  icon_url_override text, icon_color text, time_created integer NOT NULL,
  time_updated integer NOT NULL, time_initialized integer,
  time_active integer DEFAULT 0 NOT NULL, time_archived integer,
  sandboxes text NOT NULL, commands text
);
CREATE TABLE session_v2 (
  id text PRIMARY KEY, project_id text NOT NULL, workspace_id text, parent_id text,
  fork_session_id text, fork_boundary text, slug text NOT NULL, directory text NOT NULL,
  path text, title text, version text NOT NULL, share_url text, summary_additions integer,
  summary_deletions integer, summary_files integer, summary_diffs text, metadata text,
  cost real DEFAULT 0 NOT NULL, tokens_input integer DEFAULT 0 NOT NULL,
  tokens_output integer DEFAULT 0 NOT NULL, tokens_reasoning integer DEFAULT 0 NOT NULL,
  tokens_cache_read integer DEFAULT 0 NOT NULL, tokens_cache_write integer DEFAULT 0 NOT NULL,
  revert text, permission text, agent text, model text, time_created integer NOT NULL,
  time_updated integer NOT NULL, time_busy integer, time_idle integer, time_viewed integer,
  idle_outcome text, time_compacting integer, time_archived integer, time_suspended integer,
  resume_attempts integer DEFAULT 0 NOT NULL,
  CONSTRAINT fk_session_v2_project_id_project_id_fk FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE
);
CREATE TABLE session_message (
  id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL, seq integer NOT NULL,
  time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL,
  CONSTRAINT fk_session_message_session_id_session_v2_id_fk FOREIGN KEY (session_id) REFERENCES session_v2(id) ON DELETE CASCADE
);
CREATE TABLE session_part (
  message_id text NOT NULL, session_id text NOT NULL, ordinal integer NOT NULL, type text NOT NULL,
  call_id text, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL,
  CONSTRAINT session_part_pk PRIMARY KEY(message_id, ordinal),
  CONSTRAINT fk_session_part_message_id_session_message_id_fk FOREIGN KEY (message_id) REFERENCES session_message(id) ON DELETE CASCADE,
  CONSTRAINT fk_session_part_session_id_session_v2_id_fk FOREIGN KEY (session_id) REFERENCES session_v2(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX session_message_session_seq_idx ON session_message (session_id, seq);
CREATE INDEX session_message_session_time_created_id_idx ON session_message (session_id, time_created, id);
CREATE INDEX session_part_session_message_idx ON session_part (session_id, message_id, ordinal);
INSERT INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES ('p1', '/work/nuts', 1, 1, '[]');
INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, title, version, agent, model,
                        time_created, time_updated, time_archived) VALUES
  ('ses_root', 'p1', NULL, 'ses_root', '/work/nuts/03-ramsey', 'Ramsey attack', '2.2.7', 'plan',
   '{"id":"m2","providerID":"openai","variant":"default"}', 10, 20, NULL),
  ('ses_child', 'p1', 'ses_root', 'ses_child', '/work/nuts/03-ramsey', 'subagent', '2.2.7', 'explore',
   '{"id":"claude","providerID":"anthropic"}', 10, 20, NULL),
  ('ses_gone', 'p1', NULL, 'ses_gone', '/work/nuts/03-ramsey', 'old', '2.2.7', NULL, NULL, 10, 20, 500);
INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES
  ('m1', 'ses_root', 'user', 0, 100, 100, '{"text":"prove the bound","time":{"created":100}}'),
  ('a1', 'ses_root', 'assistant', 1, 110, 110, '{"agent":"build","model":{"providerID":"prov","id":"m1","variant":"default"},"content":[],"cost":1.234567,"tokens":{"input":100,"output":25,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":110}}'),
  ('m2', 'ses_root', 'user', 2, 200, 200, '{"text":"now generalize","time":{"created":200}}'),
  ('a2', 'ses_root', 'assistant', 3, 210, 210, '{"agent":"build","model":{"providerID":"prov","id":"m1","variant":"default"},"content":[],"cost":0.265433,"tokens":{"input":50,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},"time":{"created":210}}'),
  ('m3', 'ses_child', 'user', 0, 150, 150, '{"text":"child probe","time":{"created":150}}');`
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("seed 2.x store: %v", err)
	}
}

// TestReadOpenCodeSessionRowsMapsV2ContentToV1Parts: a 2.x answer's content, split
// into session_part rows or inline in its message, a prompt's text and an answer's
// error read as the 1.x message and part JSON every decoder reads; a message kind
// 1.x never had is not read, and a 2.x row the producer never writes is malformed.
func TestReadOpenCodeSessionRowsMapsV2ContentToV1Parts(t *testing.T) {
	content := `
INSERT INTO session_part (message_id, session_id, ordinal, type, call_id, time_created, time_updated, data) VALUES
  ('a1', 'ses_root', 0, 'tool', 'call_1', 111, 112, '{"type":"tool","id":"call_1","name":"subagent","state":{"status":"running","input":{"agent":"explore","description":"map the proof"}},"time":{"created":111}}'),
  ('a1', 'ses_root', 1, 'tool', 'call_2', 113, 114, '{"type":"tool","id":"call_2","name":"shell","state":{"status":"completed","input":{"command":"make proof"},"content":[{"type":"text","text":"ok"}]},"time":{"created":113,"completed":114}}'),
  ('a1', 'ses_root', 2, 'reasoning', NULL, 115, 116, '{"type":"reasoning","text":"bound by parity","time":{"created":115,"completed":116}}');
UPDATE session_message SET data = json_set(data,
  '$.content', json('[{"type":"text","text":"the bound holds"}]'), '$.finish', 'stop',
  '$.error', json('{"type":"provider.error","message":"rate limited"}')) WHERE id = 'a2';
INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
  VALUES ('x1', 'ses_root', 'synthetic', 4, 220, 220, '{"text":"tools changed"}');`
	root := t.TempDir()
	seedOpenCodeStoreV2(t, root)
	mutateOpenCodeStore(t, root, content)
	got, err := ReadOpenCodeSessionRows(context.Background(), root, "ses_root")
	if err != nil {
		t.Fatalf("ReadOpenCodeSessionRows: %v", err)
	}
	if ids := rowIDs(got.Messages); ids != "m1,a1,m2,a2" {
		t.Errorf("messages = %s, want m1,a1,m2,a2 (the synthetic x1 is no 1.x message)", ids)
	}
	wantMessages := map[string]string{
		"m1": `{"role":"user","agent":"plan","model":{"providerID":"openai","modelID":"m2"}}`,
		"a2": `{"role":"assistant","agent":"build","providerID":"prov","modelID":"m1","finish":"stop",` +
			`"cost":0.265433,"tokens":{"input":50,"output":10,"reasoning":0,"cache":{"read":0,"write":0}},` +
			`"error":{"name":"provider.error","data":{"message":"rate limited"}}}`,
	}
	for _, message := range got.Messages {
		if want, held := wantMessages[message.ID]; held && message.Data != want {
			t.Errorf("message %s data =\n%s\nwant\n%s", message.ID, message.Data, want)
		}
	}
	wantParts := []OpenCodeRow{
		{"a1/p00000000", "a1", 111, `{"type":"tool","callID":"call_1","tool":"task","state":{"status":"running",` +
			`"input":{"agent":"explore","description":"map the proof","subagent_type":"explore"},"time":{"start":111}}}`},
		{"a1/p00000001", "a1", 113, `{"type":"tool","callID":"call_2","tool":"bash","state":{"status":"completed",` +
			`"input":{"command":"make proof"},"output":"ok","time":{"start":113,"end":114}}}`},
		{"a1/p00000002", "a1", 115, `{"type":"reasoning","text":"bound by parity","time":{"start":115,"end":116}}`},
		{"a2/q00000000", "a2", 210, `{"type":"text","text":"the bound holds"}`},
		{"m1/0", "m1", 100, `{"type":"text","text":"prove the bound"}`},
		{"m2/0", "m2", 200, `{"type":"text","text":"now generalize"}`},
	}
	if fmt.Sprint(got.Parts) != fmt.Sprint(wantParts) {
		t.Errorf("parts =\n%v\nwant\n%v", got.Parts, wantParts)
	}
	if err := ProbeOpenCodeStore(context.Background(), root); err != nil {
		t.Errorf("ProbeOpenCodeStore() = %v, want a healthy 2.x store", err)
	}
	mutateOpenCodeStore(t, root, `UPDATE session_part SET data = 'not json' WHERE ordinal = 2`)
	wantProbe := "validate opencode rows: opencode session ses_root kept with malformed rows (part JSON 1)"
	if err := ProbeOpenCodeStore(context.Background(), root); err == nil || err.Error() != wantProbe {
		t.Errorf("ProbeOpenCodeStore() = %v, want %q", err, wantProbe)
	}
}
