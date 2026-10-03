CREATE TABLE IF NOT EXISTS reminders(
  id            INTEGER PRIMARY KEY,
  session_id    TEXT NOT NULL CHECK(session_id <> ''),
  engine        TEXT NOT NULL CHECK(engine <> ''),
  label         TEXT NOT NULL DEFAULT '',
  prompt        TEXT NOT NULL CHECK(prompt <> ''),
  interval_ns   INTEGER NOT NULL CHECK(interval_ns >= 60000000000),
  next_fire_ns  INTEGER NOT NULL,
  created_ns    INTEGER NOT NULL,
  last_fired_ns INTEGER NOT NULL DEFAULT 0,
  unseen        INTEGER NOT NULL DEFAULT 0 CHECK(unseen IN (0,1)),
  set_by_id     TEXT NOT NULL DEFAULT '',
  set_by_label  TEXT NOT NULL DEFAULT '',
  last_error    TEXT NOT NULL DEFAULT '',
  last_error_ns INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS reminders_next_fire ON reminders(next_fire_ns);
CREATE INDEX IF NOT EXISTS reminders_session ON reminders(session_id);
