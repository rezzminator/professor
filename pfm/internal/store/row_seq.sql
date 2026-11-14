-- row_seq counts every write to transcripts and rollouts; each written row
-- carries the count it was written at (seq), so a held Store that read the
-- tables before rereads only the rows written since (rowcache.go). A delete
-- moves the count too, so an unchanged count means both tables are as last
-- read. Ensured on every open rather than migrated (store.go migrate): it is
-- additive, the seq columns come from ensureColumn before this runs, and the
-- triggers live in the file, so a binary at schema 9 that never heard of them
-- still opens the cache and its writes still move the count.
CREATE TABLE IF NOT EXISTS row_seq (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  value INTEGER NOT NULL
);
INSERT OR IGNORE INTO row_seq (id, value) VALUES (1, 0);

CREATE INDEX IF NOT EXISTS transcripts_seq ON transcripts(seq);
CREATE INDEX IF NOT EXISTS rollouts_seq ON rollouts(seq);

CREATE TRIGGER IF NOT EXISTS transcripts_seq_insert AFTER INSERT ON transcripts BEGIN
  UPDATE row_seq SET value = value + 1 WHERE id = 1;
  UPDATE transcripts SET seq = (SELECT value FROM row_seq WHERE id = 1) WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER IF NOT EXISTS transcripts_seq_update AFTER UPDATE ON transcripts WHEN NEW.seq = OLD.seq BEGIN
  UPDATE row_seq SET value = value + 1 WHERE id = 1;
  UPDATE transcripts SET seq = (SELECT value FROM row_seq WHERE id = 1) WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER IF NOT EXISTS transcripts_seq_delete AFTER DELETE ON transcripts BEGIN
  UPDATE row_seq SET value = value + 1 WHERE id = 1;
END;

CREATE TRIGGER IF NOT EXISTS rollouts_seq_insert AFTER INSERT ON rollouts BEGIN
  UPDATE row_seq SET value = value + 1 WHERE id = 1;
  UPDATE rollouts SET seq = (SELECT value FROM row_seq WHERE id = 1) WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER IF NOT EXISTS rollouts_seq_update AFTER UPDATE ON rollouts WHEN NEW.seq = OLD.seq BEGIN
  UPDATE row_seq SET value = value + 1 WHERE id = 1;
  UPDATE rollouts SET seq = (SELECT value FROM row_seq WHERE id = 1) WHERE rowid = NEW.rowid;
END;
CREATE TRIGGER IF NOT EXISTS rollouts_seq_delete AFTER DELETE ON rollouts BEGIN
  UPDATE row_seq SET value = value + 1 WHERE id = 1;
END;
