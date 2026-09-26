DROP TABLE IF EXISTS swap_event;

CREATE TABLE IF NOT EXISTS launch(
  session_id  TEXT PRIMARY KEY,
  engine      TEXT NOT NULL,
  account     INTEGER NOT NULL,
  cache1h     INTEGER NOT NULL CHECK(cache1h IN (0,1)),
  launched_at INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
