CREATE TABLE IF NOT EXISTS chat_space(
 session_id TEXT PRIMARY KEY,
 space TEXT NOT NULL CHECK(space<>''),
 updated_at INTEGER NOT NULL
);
