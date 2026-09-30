package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type layoutJournalRecord struct {
	Row         string        `json:"row"`
	Verdict     LayoutVerdict `json:"verdict"`
	Source      string        `json:"source"`
	Destination string        `json:"destination"`
	Backup      string        `json:"backup"`
	Result      string        `json:"result"`
	// After is the destination's fingerprint right after the change
	// (layoutFingerprint); rollback refuses when it no longer matches.
	After string `json:"after,omitempty"`
}

// Journal is the one journal of an install run: the layout rows and every
// write the installer itself makes record the prior state of their paths here
// before the change, so `pfm install --rollback {id}` restores all of them.
type Journal struct {
	env        LayoutEnv
	dir        string
	records    []layoutJournalRecord
	planned    []string
	dryRun     bool
	ctx        context.Context
	clock      clock.Clock
	writeScope func(dir string, env LayoutEnv) error
}

const (
	// layoutRowInstall is the record row of a write outside the layout rows.
	layoutRowInstall        = "install"
	layoutRecordPending     = "pending"
	layoutRecordApplied     = "applied"
	layoutRolledBackMarker  = "rolled-back"
	layoutFingerprintAbsent = "absent"
	systemdDaemonReloadNote = "  note    run: systemctl --user daemon-reload"
)

// verdictInstallWrite is the verdict every install record carries.
const verdictInstallWrite LayoutVerdict = "write"

// NewJournal returns the run's journal; it writes nothing until the first
// record, which creates {home}/.local/state/pfm/migrations/{id}/.
func NewJournal(ctx context.Context, env LayoutEnv) *Journal {
	return &Journal{env: env, ctx: ctx, clock: clock.Real}
}

// Dir is the journal directory, "" until the first record.
func (journal *Journal) Dir() string {
	if journal == nil {
		return ""
	}
	return journal.dir
}

// Planned lists, sorted and de-duplicated, the paths a dry run would record.
func (journal *Journal) Planned() []string {
	if journal == nil {
		return nil
	}
	planned := append([]string(nil), journal.planned...)
	sort.Strings(planned)
	return slices.Compact(planned)
}

// Write records the prior state of paths as install records, runs action and
// marks the records applied; a failing action leaves them pending. A nil
// journal only runs action; a dry-run journal plans the paths and runs nothing.
func (journal *Journal) Write(targets []string, action func() error) error {
	if journal == nil {
		return action()
	}
	if journal.dryRun {
		journal.plan(targets)
		return nil
	}
	for _, path := range targets {
		if err := journal.before(path); err != nil {
			return err
		}
	}
	if err := action(); err != nil {
		return err
	}
	return journal.markApplied()
}

// before snapshots one path as a pending install record — the first half of
// the two-phase form for a write made inside another package.
func (journal *Journal) before(path string) error {
	if journal == nil {
		return nil
	}
	if journal.dryRun {
		journal.plan([]string{path})
		return nil
	}
	// The journal directory exists before a path resolves, so a missing
	// ancestor is never the parent of the journal itself.
	if err := journal.ensure(); err != nil {
		return err
	}
	resolved := installRecordPath(path)
	for _, record := range journal.records {
		if record.Row == layoutRowInstall && record.Result == layoutRecordPending && record.Destination == resolved {
			return nil
		}
	}
	return journal.snapshot(layoutRowInstall, verdictInstallWrite, resolved)
}

// markApplied marks every pending install record applied and flushes.
func (journal *Journal) markApplied() error {
	if journal == nil || journal.dryRun || journal.dir == "" {
		return nil
	}
	applied := []int{}
	for index := range journal.records {
		if journal.records[index].Row == layoutRowInstall && journal.records[index].Result == layoutRecordPending {
			applied = append(applied, index)
		}
	}
	return journal.markRecordsApplied(applied)
}

// markRecordsApplied marks the records applied with their post-change
// fingerprint and flushes. A destination that cannot be fingerprinted stays
// without one — rollback then counts it as drift — and the error is returned.
func (journal *Journal) markRecordsApplied(indexes []int) error {
	var failures []error
	for _, index := range indexes {
		record := &journal.records[index]
		record.Result = layoutRecordApplied
		after, err := layoutFingerprint(record.Destination)
		if err != nil {
			failures = append(failures, fmt.Errorf("fingerprint %s: %w", record.Destination, err))
		}
		record.After = after
	}
	return errors.Join(append(failures, journal.flush())...)
}

// Refingerprint recomputes the fingerprint of the last applied record of each
// path — for a change the install itself makes after the record, such as the
// schema migration of a moved database. A path with no applied record is left
// alone.
func (journal *Journal) Refingerprint(targets ...string) error {
	if journal == nil || journal.dryRun || journal.dir == "" {
		return nil
	}
	indexes := []int{}
	for _, path := range targets {
		for index := len(journal.records) - 1; index >= 0; index-- {
			record := journal.records[index]
			if record.Result == layoutRecordApplied && filepath.Clean(record.Destination) == filepath.Clean(path) {
				indexes = append(indexes, index)
				break
			}
		}
	}
	if len(indexes) == 0 {
		return nil
	}
	return journal.markRecordsApplied(indexes)
}

func (journal *Journal) plan(targets []string) {
	for _, path := range targets {
		journal.planned = append(journal.planned, installRecordPath(path))
	}
}

// installRecordPath is the path an install record names: the highest missing
// ancestor when the parent chain is absent, and the physical location of the
// parent directory, so a write through a symlinked directory records the
// path it replaces. The last component is never resolved: a link the write
// replaces is itself the prior state.
func installRecordPath(path string) string {
	path = filepath.Clean(path)
	existing, missing := path, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return path
		}
		missing, existing = existing, parent
	}
	if missing == "" {
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return path
		}
		return filepath.Join(parent, filepath.Base(path))
	}
	physical, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return missing
	}
	return filepath.Join(physical, filepath.Base(missing))
}

// changePaths is the one door for an install write outside the layout rows:
// change, with every path the action changes journaled before it runs.
func (installer *engine) changePaths(message string, targets []string, action func() error) error {
	journal := installer.options.Journal
	if journal == nil || action == nil {
		return installer.change(message, action)
	}
	if !installer.apply {
		journal.plan(targets)
		return installer.change(message, action)
	}
	return installer.change(message, func() error { return journal.Write(targets, action) })
}

var layoutJournalID = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

func (journal *Journal) ensure() error {
	if journal.dir != "" {
		return nil
	}
	root := filepath.Join(journal.env.Home, ".local", "state", "pfm", "migrations")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	currentClock := journal.clock
	if currentClock == nil {
		currentClock = clock.Real
	}
	ctx := journal.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		now := currentClock.Now()
		id := now.UTC().Format("20060102T150405Z")
		dir := filepath.Join(root, id)
		err := os.Mkdir(dir, 0o700)
		if errors.Is(err, fs.ErrExist) {
			if sleepErr := currentClock.Sleep(
				ctx,
				now.Truncate(time.Second).Add(time.Second).Sub(now),
			); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(dir, "backup"), 0o700); err != nil {
			return err
		}
		writeScope := journal.writeScope
		if writeScope == nil {
			writeScope = writeLayoutJournalScope
		}
		if err := writeScope(dir, journal.env); err != nil {
			return err
		}
		journal.dir = dir
		return nil
	}
}

func (journal *Journal) flush() error {
	raw, err := json.MarshalIndent(journal.records, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(journal.dir, "journal.json"), append(raw, '\n'), 0o600)
}

// snapshot flushes the recovery instruction before the corresponding mutation.
func (journal *Journal) snapshot(row string, verdict LayoutVerdict, path string) error {
	if err := journal.ensure(); err != nil {
		return err
	}
	backup := ""
	if _, err := os.Lstat(path); err == nil {
		backup = filepath.Join(journal.dir, "backup", fmt.Sprintf("%06d", len(journal.records)))
		if err := copyLayoutTree(path, backup); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	journal.records = append(journal.records, layoutJournalRecord{
		Row: row, Verdict: verdict, Source: path, Destination: path, Backup: backup, Result: layoutRecordPending,
	})
	return journal.flush()
}

func (journal *Journal) mutate(finding LayoutFinding, targets []string, action func() error) error {
	for _, path := range targets {
		if err := journal.snapshot(finding.Row, finding.Verdict, path); err != nil {
			return err
		}
	}
	if err := action(); err != nil {
		return err
	}
	applied := []int{}
	for index := len(journal.records) - len(targets); index < len(journal.records); index++ {
		applied = append(applied, index)
	}
	return journal.markRecordsApplied(applied)
}

func (journal *Journal) discardPending(start int) error {
	if start >= len(journal.records) {
		return nil
	}
	for _, record := range journal.records[start:] {
		if record.Result != layoutRecordPending {
			return errors.New("cannot discard applied journal records")
		}
		if record.Backup != "" {
			if err := os.RemoveAll(record.Backup); err != nil {
				return err
			}
		}
	}
	journal.records = journal.records[:start]
	if len(journal.records) == 0 {
		if err := os.RemoveAll(journal.dir); err != nil {
			return err
		}
		journal.dir = ""
		return nil
	}
	return journal.flush()
}

func copyLayoutTree(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(link, target)
	case info.IsDir():
		if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyLayoutTree(
				filepath.Join(source, entry.Name()),
				filepath.Join(target, entry.Name()),
			); err != nil {
				return err
			}
		}
		// Mkdir applies the umask; the copy keeps the source's mode, set
		// after the children so a read-only directory still fills.
		return os.Chmod(target, info.Mode().Perm())
	case info.Mode().IsRegular():
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		_, writeErr := atomicfile.WriteFrom(target, input, info.Mode().Perm(), 0)
		return errors.Join(writeErr, input.Close())
	default:
		return fmt.Errorf("unsupported file mode %s at %s", info.Mode(), source)
	}
}

// layoutRecordSafe reports whether a record may be replayed: its destination
// is on the allowlist and its backup lies inside the journal.
func layoutRecordSafe(env LayoutEnv, dir string, record layoutJournalRecord) bool {
	return layoutRecordAllowed(env, record) &&
		(record.Backup == "" || strings.HasPrefix(record.Backup, filepath.Join(dir, "backup")+string(os.PathSeparator)))
}

// layoutCreatedSessionStores names each session store (~/.claude/{entry}) this
// install created: the journal's first record for that path is a session-store
// row with no backup, because nothing stood there. A later row of the same
// install (a second account's merge) journals the store again with a backup
// of what the first row made; the store is still the install's own.
func layoutCreatedSessionStores(env LayoutEnv, records []layoutJournalRecord) map[string]bool {
	parent := filepath.Clean(filepath.Join(env.Home, ".claude"))
	created := map[string]bool{}
	seen := map[string]bool{}
	for index := range records {
		record := &records[index]
		destination := filepath.Clean(record.Destination)
		if seen[destination] {
			continue
		}
		seen[destination] = true
		if record.Row == layoutRowSessionStore && record.Backup == "" && filepath.Dir(destination) == parent {
			created[destination] = true
		}
	}
	return created
}

var layoutStoreReadDir = os.ReadDir

func layoutStoreHoldsData(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return true, nil
	}
	entries, err := layoutStoreReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

// layoutRollbackLiveChats refuses a journal with a session-store record while
// a chat is live on its account: {account}/{entry} checks that account, the
// shared store {home}/.claude/{entry} checks every account.
func layoutRollbackLiveChats(env LayoutEnv, id string, records []layoutJournalRecord) error {
	store := filepath.Join(env.Home, ".claude")
	checked := map[string]bool{}
	for _, record := range records {
		if record.Result == layoutRecordRestored || record.Row != layoutRowSessionStore {
			continue
		}
		dirs := []string{filepath.Dir(record.Destination)}
		if filepath.Clean(dirs[0]) == filepath.Clean(store) {
			dirs = accountDirs(env)
		}
		for _, dir := range dirs {
			if checked[dir] {
				continue
			}
			checked[dir] = true
			pids, err := liveChatPIDs(env.ProcRoot, dir)
			if err != nil {
				return fmt.Errorf("rollback %s: read live chats on %s: %w", id, dir, err)
			}
			if len(pids) > 0 {
				return fmt.Errorf("rollback %s refused: live chats on %s: %s", id, dir, strings.Join(pids, ","))
			}
		}
	}
	return nil
}

// layoutRollbackDrift names, in journal order, every destination whose last
// applied record no longer matches the destination on disk. Pending records,
// the derived cache database and SQLite sidecars are never checked; a record
// the replay would refuse as unsafe is left to that refusal.
func layoutRollbackDrift(env LayoutEnv, dir string, records []layoutJournalRecord) ([]string, error) {
	last := map[string]int{}
	for index, record := range records {
		if record.Result == layoutRecordApplied {
			last[filepath.Clean(record.Destination)] = index
		}
	}
	createdStores := layoutCreatedSessionStores(env, records)
	drifted := []string{}
	for index, record := range records {
		destination := filepath.Clean(record.Destination)
		if last[destination] != index || record.Result != layoutRecordApplied || record.Row == layoutRowCacheDB ||
			strings.HasSuffix(destination, layoutDBWAL) || strings.HasSuffix(destination, layoutDBSHM) ||
			!layoutRecordSafe(env, dir, record) || createdStores[destination] {
			continue
		}
		if record.After == "" {
			drifted = append(drifted, record.Destination+" (no fingerprint)")
			continue
		}
		now, err := layoutFingerprint(record.Destination)
		if err != nil {
			return nil, fmt.Errorf("fingerprint %s: %w", record.Destination, err)
		}
		if now != record.After {
			drifted = append(drifted, record.Destination)
		}
	}
	return drifted, nil
}

// layoutFingerprint is the identity of path and everything under it, walked
// with Lstat (links never followed): hex sha256 over the sorted lines, one per
// entry, of relative path, type, size, mtime in Unix nanoseconds, mode bits
// and link target. An absent path is "absent".
func layoutFingerprint(path string) (string, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return layoutFingerprintAbsent, nil
	} else if err != nil {
		return "", err
	}
	lines := []string{}
	err := filepath.WalkDir(path, func(entryPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(path, entryPath)
		if err != nil {
			return err
		}
		kind, target := "file", ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			kind = "link"
			if target, err = os.Readlink(entryPath); err != nil {
				return err
			}
		case info.IsDir():
			kind = "dir"
		}
		lines = append(lines, fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%o\x00%s",
			relative, kind, info.Size(), info.ModTime().UnixNano(), uint32(info.Mode()), target))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func restoreLayoutRecord(ctx context.Context, record layoutJournalRecord) error {
	removeErr := os.RemoveAll(record.Destination)
	if removeErr == nil && record.Backup == "" {
		return nil
	}
	if removeErr == nil {
		if err := copyLayoutTree(record.Backup, record.Destination); err == nil {
			return nil
		} else if record.Row != layoutRowManagedCleanup {
			return err
		}
	} else if record.Row != layoutRowManagedCleanup {
		return removeErr
	}
	runner := execCommandRunner{}
	if record.Backup == "" {
		return runner.Run(ctx, "sudo", "rm", "-f", record.Destination)
	}
	for _, args := range managedInstallArgs(record.Backup, record.Destination) {
		if err := runner.Run(ctx, "sudo", args...); err != nil {
			return fmt.Errorf("restore %s through sudo %s: %w", record.Destination, args[0], err)
		}
	}
	return nil
}

func layoutRecordAllowed(env LayoutEnv, record layoutJournalRecord) bool {
	if record.Destination == "" || !filepath.IsAbs(record.Destination) {
		return false
	}
	path := filepath.Clean(record.Destination)
	allows := func(candidates ...string) bool {
		for _, candidate := range candidates {
			if candidate != "" && path == filepath.Clean(candidate) {
				return true
			}
		}
		return false
	}
	switch record.Row {
	case layoutRowManagedCleanup:
		return allows(filepath.Join(env.ManagedDir, "pfm.json"))
	case layoutRowConfig:
		return allows(env.ConfigPath,
			filepath.Join(env.LegacyConfigDir, "pfm.config.json"),
			filepath.Join(env.LegacyConfigDir, "config.json"),
			filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json"))
	case layoutRowHarvesterConfig:
		return allows(filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json"),
			filepath.Join(env.LegacyConfigDir, "harvester.config.json"))
	case layoutRowStateDB, layoutRowCacheDB:
		legacy := paths.LegacyStateDB(env.Home)
		target := env.StateDB
		if record.Row == layoutRowCacheDB {
			legacy = paths.LegacyCacheDB(env.Home)
			target = env.CacheDB
		}
		for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
			if allows(legacy+suffix, target+suffix) {
				return true
			}
		}
		backup, ok := strings.CutPrefix(path, target+".bak-before-v")
		if !ok {
			return false
		}
		version, numbered, hasNumber := strings.Cut(backup, ".")
		parsedVersion, err := strconv.Atoi(version)
		if err != nil || parsedVersion < 1 {
			return false
		}
		if hasNumber {
			parsedNumber, err := strconv.Atoi(numbered)
			return err == nil && parsedNumber > 0
		}
		return true
	case layoutRowSessionStore:
		for _, entry := range SessionPaths {
			if allows(filepath.Join(env.Home, ".claude", entry)) {
				return true
			}
			for _, dir := range accountDirs(env) {
				if allows(filepath.Join(dir, entry)) {
					return true
				}
			}
		}
	case layoutRowMemoryHelpers:
		for _, dir := range accountDirs(env) {
			if allows(filepath.Join(dir, "settings.json"), filepath.Join(dir, "settings.local.json")) {
				return true
			}
			for _, helper := range retiredMemoryHelpers {
				if allows(
					filepath.Join(dir, "scripts", helper.oldName),
					filepath.Join(dir, "scripts", helper.newName),
				) {
					return true
				}
			}
		}
	case layoutRowAccountSettings:
		if allows(settingsHookOwnershipPath(env.ManagedRoot)) {
			return true
		}
		for _, dir := range accountDirs(env) {
			settings := filepath.Join(dir, "settings.json")
			if allows(settings, physicalSettingsPath(settings)) {
				return true
			}
		}
	case layoutRowAccountMCP:
		if allows(filepath.Join(env.ManagedRoot, mcpOwnershipName)) {
			return true
		}
		for _, registry := range ClaudeUserRegistries(env.Home, env.Config.Accounts, "") {
			if allows(registry.Path) {
				return true
			}
		}
	case layoutRowHomeMCP:
		return allows(filepath.Join(env.Home, ".mcp.json"), filepath.Join(env.ManagedRoot, mcpOwnershipName))
	case layoutRowZshrc:
		return allows(filepath.Join(env.Home, ".zshrc"))
	case layoutRowStagedPrompts:
		return allows(filepath.Join(env.ManagedRoot, "harness-prompts"))
	case layoutRowSharedDB:
		return allows(filepath.Join(env.Home, ".local", "state", "pfm", "shared.db"))
	case layoutRowInstall:
		for _, home := range layoutHomes(env) {
			migrations := filepath.Join(home, ".local", "state", "pfm", "migrations")
			if path == migrations || pathWithin(path, migrations) {
				return false
			}
		}
		if allows(env.ConfigPath) ||
			env.ConfigPath != "" && allows(filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json")) {
			return true
		}
		for _, root := range append(layoutHomes(env), env.Config.CodexHomes()...) {
			if pathWithin(path, root) {
				return true
			}
		}
	case layoutRowStrayDir:
		return allows(
			filepath.Join(env.Home, ".cc", ".git"),
			filepath.Join(env.Home, ".cc", ".codex"),
			filepath.Join(env.Home, ".cc", ".agents"),
		)
	}
	return false
}

// layoutHomes is the home as configured and, when it differs, its physical
// location: install records name physical parents.
func layoutHomes(env LayoutEnv) []string {
	homes := []string{filepath.Clean(env.Home)}
	if physical, err := filepath.EvalSymlinks(env.Home); err == nil && physical != homes[0] {
		homes = append(homes, physical)
	}
	return homes
}

// pathWithin reports whether path lies strictly below root.
func pathWithin(path, root string) bool {
	root = filepath.Clean(root)
	return root != "" && root != "." && strings.HasPrefix(filepath.Clean(path), root+string(os.PathSeparator))
}
