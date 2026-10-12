package installer

import "path/filepath"

// RetiredMigrationJournal is the journal tree the installer's journaled
// migration wrote (internal/config/migration.go and
// installer/journal_inventory.go, both deleted in 53fd07fa): nothing reads it
// and no rollback through it exists.
func RetiredMigrationJournal(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "migrations")
}

// retireMigrationJournal removes the retired migration journal whole on
// pfm install; a dry run names what it would free.
//
// Since v0.80.0. Sunset: delete this step, RetiredMigrationJournal and its
// pfm doctor --stale row (hostcheck "retired-migration-journal") at v0.90.0,
// ten minor versions past Since (docs/dev/pfm-architecture.md Open ruling 6).
func (installer *engine) retireMigrationJournal() {
	installer.removeStatePath(RetiredMigrationJournal(installer.options.Home), "retired migration journal", "")
}
