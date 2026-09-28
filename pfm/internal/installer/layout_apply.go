package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

var (
	errLayoutRefuse   = errors.New("layout row refused")
	errLayoutAdvisory = errors.New("layout row advisory")
	// errLayoutNoSource refuses a row that needs the clone; pfm runs on its
	// defaults without it, so the row never fails the install.
	errLayoutNoSource = fmt.Errorf("%w: no source repo recorded", errLayoutRefuse)
)

// ApplyLayout converges the layout rows, recording into journal (nil: a
// private one); it returns the journal directory. An apply with database work
// stops the running fleet units once, when the loop reaches the first
// database row, and restarts and verifies them once, right after the last
// database row's outcome or at any earlier return.
func ApplyLayout(
	ctx context.Context,
	env LayoutEnv,
	journal *Journal,
	apply bool,
	stdout io.Writer,
) (dir string, err error) {
	if journal == nil {
		journal = NewJournal(ctx, env)
	}
	journal.dryRun = !apply
	layoutStart := len(journal.records)
	findings := ClassifyLayout(env)
	if apply {
		if err := installGate(env, findings); err != nil {
			return "", err
		}
	}
	actionable := false
	configMigrated := false
	var independentFailures []error
	lastDB, dbWork := layoutDatabaseWork(findings)
	servicesArmed, servicesStopped := false, false
	var stoppedUnits []string
	var stopErr error
	restartServices := func() error {
		if !servicesArmed {
			return nil
		}
		servicesArmed = false
		return restartLayoutServices(ctx, env, stoppedUnits)
	}
	defer func() { err = errors.Join(err, restartServices()) }()
	for index, planned := range findings {
		if servicesArmed && index > lastDB {
			independentFailures = append(independentFailures, restartServices())
		}
		if apply && dbWork && !servicesStopped && layoutDatabaseRow(planned.Row) {
			servicesStopped, servicesArmed = true, true
			stoppedUnits, stopErr = stopLayoutServices(ctx, env)
		}
		if apply && !configMigrated && planned.Row == layoutRowStateDB {
			if err := applyLayoutConfigMigration(journal); err != nil {
				return journal.dir, fmt.Errorf("layout config migration: %w", err)
			}
			configMigrated = true
		}
		if planned.Verdict == VerdictOK && planned.Err == nil {
			continue
		}
		if stopErr != nil && layoutDatabaseRow(planned.Row) {
			independentFailures = append(independentFailures,
				fmt.Errorf("layout %s %s: %w", planned.Row, planned.Path, stopErr))
			continue
		}
		current := planned
		if apply {
			var found bool
			for _, candidate := range ClassifyLayout(env) {
				if candidate.Row == planned.Row && candidate.Path == planned.Path {
					current, found = candidate, true
					break
				}
			}
			if !found || current.Verdict == VerdictOK && current.Err == nil {
				continue
			}
		}
		if current.Row == layoutRowStateDB || current.Row == layoutRowCacheDB {
			if apply && current.Err == nil && current.Verdict == VerdictRefuse &&
				strings.HasPrefix(current.Detail, "held by pid ") {
				// The service may be the holder; stop it before deciding refusal.
				current.Verdict = VerdictMove
			}
		}
		if current.Err != nil || current.Verdict == VerdictRefuse {
			detail := current.Detail
			if current.Err != nil {
				detail = current.Err.Error()
			}
			fmt.Fprintf(stdout, "  refuse  layout %s %s — %s\n", current.Row, current.Path, detail)
			if apply && current.Row != layoutRowManagedCleanup {
				independentFailures = append(independentFailures,
					fmt.Errorf("layout %s %s refused: %s", current.Row, current.Path, detail))
			}
			continue
		}
		actionable = true
		fmt.Fprintf(stdout, "  change  layout %s %s %s\n", current.Row, current.Verdict, current.Path)
		if !apply {
			continue
		}
		var err error
		priorRecords := len(journal.records)
		if current.Row == layoutRowStateDB || current.Row == layoutRowCacheDB {
			err = applyLayoutDB(ctx, journal, current)
		} else {
			err = applyLayoutRow(ctx, journal, current, stdout)
		}
		if errors.Is(err, errLayoutRefuse) || errors.Is(err, errLayoutAdvisory) {
			if discardErr := journal.discardPending(priorRecords); discardErr != nil {
				return journal.dir, fmt.Errorf("layout %s discard refused action: %w", current.Row, discardErr)
			}
			if errors.Is(err, errLayoutAdvisory) {
				fmt.Fprintf(
					stdout,
					"  warn    layout managed-cleanup %s — sudo -n needs cached credentials; run: sudo mkdir -p %s && printf '%%s\\n' '{\"cleanupPeriodDays\":%d}' | sudo tee %s >/dev/null\n",
					current.Path,
					shellCommandLine(filepath.Dir(current.Path)),
					env.Config.Claude.CleanupPeriodDays,
					shellCommandLine(current.Path),
				)
			} else {
				detail := strings.TrimPrefix(err.Error(), errLayoutRefuse.Error()+": ")
				fmt.Fprintf(stdout, "  refuse  layout %s %s — %s\n", current.Row, current.Path, detail)
				if !errors.Is(err, errLayoutNoSource) {
					independentFailures = append(independentFailures,
						fmt.Errorf("layout %s %s refused: %s", current.Row, current.Path, detail))
				}
			}
			continue
		}
		if err != nil {
			rowErr := fmt.Errorf("layout %s %s: %w", current.Row, current.Path, err)
			if current.Row == layoutRowStateDB || current.Row == layoutRowCacheDB {
				independentFailures = append(independentFailures, rowErr)
				continue
			}
			return journal.dir, rowErr
		}
		verified := layoutFindingByPath(ClassifyLayout(env), current.Row, current.Path)
		if verified.Err != nil || verified.Verdict != VerdictOK {
			rowErr := fmt.Errorf("layout %s %s did not verify: %+v", current.Row, current.Path, verified)
			if current.Row == layoutRowStateDB || current.Row == layoutRowCacheDB {
				independentFailures = append(independentFailures, rowErr)
				continue
			}
			return journal.dir, rowErr
		}
		fmt.Fprintf(stdout, "  ok      layout %s %s\n", current.Row, current.Path)
	}
	independentFailures = append(independentFailures, restartServices())
	if apply {
		if len(journal.records) == layoutStart {
			fmt.Fprintln(stdout, "layout: nothing to do")
		}
	} else if !actionable {
		fmt.Fprintln(stdout, "layout: nothing to do")
	}
	return journal.dir, errors.Join(independentFailures...)
}

// layoutDatabaseRow reports whether row moves a database the fleet units hold.
func layoutDatabaseRow(row string) bool {
	return row == layoutRowStateDB || row == layoutRowCacheDB
}

// layoutDatabaseWork returns the index of the last database finding and
// whether any database finding has work (not ok, or unreadable).
func layoutDatabaseWork(findings []LayoutFinding) (last int, work bool) {
	last = -1
	for index, finding := range findings {
		if !layoutDatabaseRow(finding.Row) {
			continue
		}
		last = index
		if finding.Verdict != VerdictOK || finding.Err != nil {
			work = true
		}
	}
	return last, work
}

func layoutFindingByPath(findings []LayoutFinding, row, path string) LayoutFinding {
	for _, finding := range findings {
		if finding.Row == row && finding.Path == path {
			return finding
		}
	}
	return LayoutFinding{Row: row, Path: path, Err: errors.New("finding disappeared")}
}

// layoutSnapshotPaths names every path apply journals before it acts on
// finding — the one list apply and the space preflight (layout_space.go) both
// read. The database family's migration backup is added by applyLayoutDB: it
// does not exist before the move, so it has nothing to copy.
func layoutSnapshotPaths(env LayoutEnv, finding LayoutFinding) ([]string, error) {
	switch finding.Row {
	case layoutRowConfig, layoutRowHarvesterConfig:
		if finding.Verdict == VerdictMove {
			return []string{finding.Source, finding.Path}, nil
		}
	case layoutRowSessionStore:
		if finding.Verdict == VerdictMerge {
			return []string{finding.Path, layoutSessionStore(env, finding)}, nil
		}
	case layoutRowMemoryHelpers:
		planner := &engine{options: Options{Home: env.Home, ConfigDirs: accountDirs(env), Stdout: io.Discard}}
		migrations, err := planner.planMemoryHelperMigrations()
		if err != nil {
			return nil, err
		}
		paths := []string{}
		for _, migration := range migrations {
			paths = append(paths, migration.oldPath, migration.newPath)
		}
		for _, dir := range accountDirs(env) {
			for _, name := range []string{"settings.json", "settings.local.json"} {
				paths = append(paths, filepath.Join(dir, name))
			}
		}
		return paths, nil
	case layoutRowAccountSettings:
		// A shared settings.json link is written through: the target is the
		// prior state, and the link stays a link.
		return []string{physicalSettingsPath(finding.Path), settingsHookOwnershipPath(env.ManagedRoot)}, nil
	case layoutRowAccountMCP, layoutRowHomeMCP:
		return []string{finding.Path, filepath.Join(env.ManagedRoot, mcpOwnershipName)}, nil
	case layoutRowStateDB, layoutRowCacheDB:
		paths := []string{}
		for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
			paths = append(paths, finding.Source+suffix, finding.Path+suffix)
		}
		return paths, nil
	}
	return []string{finding.Path}, nil
}

// layoutSessionStore is the shared store entry a session-store finding links to.
func layoutSessionStore(env LayoutEnv, finding LayoutFinding) string {
	return filepath.Join(env.Home, ".claude", filepath.Base(finding.Path))
}

// layoutConfigMigrationPaths names what the config migration before the
// state-db row journals.
func layoutConfigMigrationPaths(env LayoutEnv) []string {
	return []string{env.ConfigPath, filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json")}
}

func applyLayoutRow(ctx context.Context, journal *Journal, finding LayoutFinding, stdout io.Writer) error {
	env := journal.env
	paths, err := layoutSnapshotPaths(env, finding)
	if err != nil {
		return err
	}
	switch finding.Row {
	case layoutRowManagedCleanup:
		content := []byte(fmt.Sprintf("{\"cleanupPeriodDays\":%d}\n", env.Config.Claude.CleanupPeriodDays))
		return journal.mutate(finding, paths, func() error {
			writeManaged := env.writeManaged
			if writeManaged == nil {
				writeManaged = func(path string, content []byte) error { return atomicfile.Write(path, content, 0o644) }
			}
			if err := writeManaged(finding.Path, content); err == nil {
				return nil
			}
			tmp := filepath.Join(journal.dir, "managed-cleanup.tmp")
			if err := atomicfile.Write(tmp, content, 0o600); err != nil {
				return err
			}
			defer func() { _ = os.Remove(tmp) }()
			for _, args := range managedInstallArgs(tmp, finding.Path) {
				sudoArgs := append([]string{"-n"}, args...)
				fmt.Fprintln(stdout, "sudo "+shellCommandLine(sudoArgs...))
				if err := env.commandRunner().Run(ctx, "sudo", sudoArgs...); err != nil {
					return fmt.Errorf("%w: sudo -n %s declined: %v", errLayoutAdvisory, args[0], err)
				}
			}
			return nil
		})
	case layoutRowConfig, layoutRowHarvesterConfig:
		if finding.Verdict == VerdictMove {
			return journal.mutate(finding, paths, func() error {
				if finding.Detail == "identical legacy copy" {
					return os.Remove(finding.Source)
				}
				return moveLayoutPath(env, finding.Source, finding.Path)
			})
		}
		if env.Clone == "" {
			return errLayoutNoSource
		}
		source := filepath.Join(env.Clone, "example.pfm.config.json")
		if _, err := os.Stat(source); err != nil {
			return fmt.Errorf("%w: %v", errLayoutNoSource, err)
		}
		return journal.mutate(finding, paths, func() error { return copyLayoutTree(source, finding.Path) })
	case layoutRowSessionStore:
		store := layoutSessionStore(env, finding)
		switch finding.Verdict {
		case VerdictMerge:
			conflicts, err := mergeLayoutSession(journal, finding, paths, store)
			for _, conflict := range conflicts {
				fmt.Fprintln(stdout, "  conflict layout session-store "+conflict)
			}
			return err
		case VerdictCreate, VerdictRepoint:
			return journal.mutate(finding, paths, func() error {
				if err := os.MkdirAll(filepath.Dir(finding.Path), 0o700); err != nil {
					return err
				}
				if finding.Verdict == VerdictRepoint {
					if err := os.Remove(finding.Path); err != nil {
						return err
					}
				}
				return os.Symlink(store, finding.Path)
			})
		}
	case layoutRowMemoryHelpers:
		installer := &engine{
			options:       Options{Home: env.Home, ConfigDirs: accountDirs(env), Stdout: io.Discard},
			apply:         true,
			stamp:         clock.Real.Now().UTC().Format("20060102-150405"),
			layoutJournal: journal,
		}
		preview := &engine{options: installer.options, apply: false}
		if err := preview.migrateMemoryHelpers(); err != nil {
			return err
		}
		return journal.mutate(finding, paths, installer.migrateMemoryHelpers)
	case layoutRowAccountSettings, layoutRowAccountMCP:
		return applyLayoutAccount(journal, finding, paths)
	case layoutRowHomeMCP:
		return applyLayoutHomeMCP(journal, finding, paths)
	case layoutRowZshrc:
		if env.Clone == "" {
			return errLayoutNoSource
		}
		return journal.mutate(finding, paths, func() error {
			raw, err := os.ReadFile(finding.Path)
			if errors.Is(err, fs.ErrNotExist) {
				raw = nil
			} else if err != nil {
				return err
			}
			wanted := sourceLine(filepath.Join(env.Clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh"))
			return atomicfile.Write(finding.Path, []byte(rewriteZshrc(string(raw), wanted, false)), 0o600)
		})
	case layoutRowStagedPrompts, layoutRowSharedDB, layoutRowStrayDir:
		return journal.mutate(finding, paths, func() error { return os.RemoveAll(finding.Path) })
	}
	return fmt.Errorf("unsupported verdict %s for row %s", finding.Verdict, finding.Row)
}

func applyLayoutConfigMigration(journal *Journal) error {
	env := &journal.env
	if _, err := os.Stat(env.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	loaded, err := pfmconfig.Load(env.ConfigPath, env.Home, nil)
	if err != nil {
		return err
	}
	migration, err := pfmconfig.PlanMigration(loaded)
	if err != nil || migration.Empty() {
		return err
	}
	if migration.LegacyPath != "" || migration.StrayLegacyPath != "" {
		return fmt.Errorf("%w: pre-split config path is outside HostLayout", errLayoutRefuse)
	}
	change := LayoutFinding{Row: layoutRowConfig, Verdict: VerdictRepoint, Path: env.ConfigPath}
	if err := journal.mutate(
		change,
		layoutConfigMigrationPaths(*env),
		func() error { return pfmconfig.ApplyMigration(migration) },
	); err != nil {
		return err
	}
	env.Config = migration.Preview(loaded)
	return nil
}

func applyLayoutAccount(journal *Journal, finding LayoutFinding, paths []string) error {
	env := journal.env
	raw, err := os.ReadFile(finding.Path)
	if err != nil {
		return err
	}
	if finding.Row == layoutRowAccountSettings {
		ledger := settingsHookOwnershipPath(env.ManagedRoot)
		ownership, _, err := readSettingsHookOwnership(ledger)
		if err != nil {
			return err
		}
		physical := physicalSettingsPath(finding.Path)
		updated, _, err := stripAccountSettings(raw, env.Home, ownership[physical])
		if err != nil {
			return err
		}
		delete(ownership, physical)
		encoded, err := encodeSettingsHookOwnership(ownership)
		if err != nil {
			return err
		}
		return journal.mutate(finding, paths, func() error {
			if err := atomicfile.Write(physical, updated, 0o600); err != nil {
				return err
			}
			if _, err := os.Stat(ledger); errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return atomicfile.Write(ledger, encoded, 0o600)
		})
	}
	ledger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	ownership, err := readMCPOwnership(ledger)
	if err != nil {
		return err
	}
	owned, keys := ledgerOwnedMCP(ownership.Registrations, physicalSettingsPath(finding.Path))
	updated, _, err := stripAccountMCP(raw, owned, layoutMCPShaped(env))
	if err != nil {
		return err
	}
	for _, key := range keys {
		delete(ownership.Registrations, key)
	}
	encoded, err := json.MarshalIndent(ownership, "", "  ")
	if err != nil {
		return err
	}
	return journal.mutate(finding, paths, func() error {
		if err := atomicfile.Write(finding.Path, updated, 0o600); err != nil {
			return err
		}
		if _, err := os.Stat(ledger); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return atomicfile.Write(ledger, append(encoded, '\n'), 0o600)
	})
}

// applyLayoutHomeMCP strips pfm's entries from {home}/.mcp.json and retires the
// ledger's clients list in one journaled change; an absent file stays absent.
func applyLayoutHomeMCP(journal *Journal, finding LayoutFinding, paths []string) error {
	env := journal.env
	ledger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	ownership, err := readMCPOwnership(ledger)
	if err != nil {
		return err
	}
	var updated []byte
	var removed []string
	mode := fs.FileMode(0o600)
	info, err := os.Stat(finding.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		mode = info.Mode().Perm()
		raw, err := os.ReadFile(finding.Path)
		if err != nil {
			return err
		}
		if updated, removed, err = stripAccountMCP(raw, ownership.Clients, layoutMCPShaped(env)); err != nil {
			return err
		}
	}
	retire := len(ownership.Clients) > 0
	ownership.Clients = nil
	encoded, err := json.MarshalIndent(ownership, "", "  ")
	if err != nil {
		return err
	}
	return journal.mutate(finding, paths, func() error {
		if len(removed) > 0 {
			if err := atomicfile.Write(finding.Path, updated, mode); err != nil {
				return err
			}
		}
		if !retire {
			return nil
		}
		return atomicfile.Write(ledger, append(encoded, '\n'), 0o600)
	})
}

// applyLayoutDB moves one database row; ApplyLayout has stopped the fleet
// units before the first database row and restarts them after the last.
func applyLayoutDB(ctx context.Context, journal *Journal, finding LayoutFinding) error {
	env := journal.env
	current := layoutFindingByPath(ClassifyLayout(env), finding.Row, finding.Path)
	if current.Err != nil {
		return current.Err
	}
	if current.Verdict == VerdictRefuse {
		return fmt.Errorf("%w: %s", errLayoutRefuse, current.Detail)
	}
	if current.Verdict == VerdictOK {
		return nil
	}
	paths, err := layoutSnapshotPaths(env, current)
	if err != nil {
		return err
	}
	backup, err := layoutMigrationBackupPath(ctx, current.Source, current.Path)
	if err != nil {
		return err
	}
	paths = append(paths, backup)
	return journal.mutate(current, paths, func() error {
		before, err := layoutDBCounts(ctx, current.Source)
		if err != nil {
			return err
		}
		db, err := sqlitedb.OpenReadWrite(current.Source, 10*time.Second)
		if err != nil {
			return err
		}
		var busy, log, checkpointed int
		err = db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed)
		closeErr := db.Close()
		if err != nil {
			return fmt.Errorf("checkpoint %s: %w", current.Source, err)
		}
		if closeErr != nil || busy != 0 {
			return fmt.Errorf("checkpoint %s: busy=%d close=%v", current.Source, busy, closeErr)
		}
		for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
			old := current.Source + suffix
			if _, err := os.Lstat(old); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return err
			}
			if err := moveLayoutPath(env, old, current.Path+suffix); err != nil {
				return err
			}
		}
		after, err := layoutDBCounts(ctx, current.Path)
		if err != nil {
			return err
		}
		for name, count := range before {
			if after[name] != count {
				return fmt.Errorf("database table %s row count %d -> %d", name, count, after[name])
			}
		}
		return nil
	})
}

func layoutMigrationBackupPath(ctx context.Context, source, target string) (string, error) {
	db, err := sqlitedb.OpenReadOnly(source, 10*time.Second)
	if err != nil {
		return "", err
	}
	var version int
	readErr := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	closeErr := db.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", fmt.Errorf("read database schema version %s: %w", source, err)
	}
	base := fmt.Sprintf("%s.bak-before-v%d", target, version+1)
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			candidate = fmt.Sprintf("%s.%d", base, suffix)
		}
		info, err := os.Stat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect migration backup %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("migration backup %s is not a regular file", candidate)
		}
	}
}

func layoutDBCounts(ctx context.Context, path string) (map[string]int64, error) {
	db, err := sqlitedb.OpenReadOnly(path, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, name := range names {
		var count int64
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoted).Scan(&count); err != nil {
			return nil, err
		}
		counts[name] = count
	}
	return counts, nil
}

func (env LayoutEnv) commandRunner() CommandRunner {
	if env.runner != nil {
		return env.runner
	}
	return execCommandRunner{}
}

// installProgram is install(1), which managedInstallArgs runs under sudo on
// both kernels.
const installProgram = "install"

// shellCommandLine joins words into one POSIX shell line a person can paste:
// a word of only safe characters stays bare, any other is single-quoted, so a
// path holding a space (the macOS managed dir) stays one word.
func shellCommandLine(words ...string) string {
	quoted := make([]string, len(words))
	for index, word := range words {
		if word != "" && strings.IndexFunc(word, func(r rune) bool { return !shellSafeRune(r) }) < 0 {
			quoted[index] = word
		} else {
			quoted[index] = action.Quote(word)
		}
	}
	return strings.Join(quoted, " ")
}

// shellSafeRune reports whether r needs no shell quoting.
func shellSafeRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)
}
