-- Each permanent kill write owns a fresh revision, independent of its timestamp.
ALTER TABLE hidden ADD COLUMN revision TEXT NOT NULL DEFAULT '';
