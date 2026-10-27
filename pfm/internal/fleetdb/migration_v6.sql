CREATE TABLE IF NOT EXISTS seat(
 seat_id TEXT PRIMARY KEY,
 session_id TEXT,
 engine TEXT,
 backend TEXT CHECK(backend IN ('tmux','herdr')),
 tmux_socket TEXT,
 tmux_pane TEXT,
 herdr_socket TEXT,
 herdr_pane TEXT,
 herdr_workspace TEXT,
 name TEXT,
 state TEXT CHECK(state IN ('live','lost','failed')),
 reload_pending INTEGER,
 lost_note TEXT,
 updated_at INTEGER
);
CREATE INDEX IF NOT EXISTS seat_session ON seat(session_id);
CREATE INDEX IF NOT EXISTS seat_herdr_pane ON seat(herdr_socket,herdr_pane);
