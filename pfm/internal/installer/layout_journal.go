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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

type layoutJournalRecord struct {
	Row         string        `json:"row"`
	Verdict     LayoutVerdict `json:"verdict"`
	Source      string        `json:"source"`
	Destination string        `json:"destination"`
	Backup      string        `json:"backup"`
	Result      string        `json:"result"`
}

type layoutJournal struct {
	env     LayoutEnv
	dir     string
	records []layoutJournalRecord
	ctx     context.Context
	clock   clock.Clock
}

var layoutJournalID = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

func (journal *layoutJournal) ensure() error {
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
		journal.dir = dir
		return os.Mkdir(filepath.Join(dir, "backup"), 0o700)
	}
}

func (journal *layoutJournal) flush() error {
	raw, err := json.MarshalIndent(journal.records, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(journal.dir, "journal.json"), append(raw, '\n'), 0o600)
}

// snapshot flushes the recovery instruction before the corresponding mutation.
func (journal *layoutJournal) snapshot(row string, verdict LayoutVerdict, path string) error {
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
		Row: row, Verdict: verdict, Source: path, Destination: path, Backup: backup, Result: "pending",
	})
	return journal.flush()
}

func (journal *layoutJournal) mutate(finding LayoutFinding, paths []string, action func() error) error {
	for _, path := range paths {
		if err := journal.snapshot(finding.Row, finding.Verdict, path); err != nil {
			return err
		}
	}
	if err := action(); err != nil {
		return err
	}
	for index := len(journal.records) - len(paths); index < len(journal.records); index++ {
		journal.records[index].Result = "applied"
	}
	return journal.flush()
}

func (journal *layoutJournal) discardPending(start int) error {
	if start >= len(journal.records) {
		return nil
	}
	for _, record := range journal.records[start:] {
		if record.Result != "pending" {
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
		return nil
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

func RollbackLayout(ctx context.Context, env LayoutEnv, id string, stdout io.Writer) error {
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	if !layoutJournalID.MatchString(id) {
		return fmt.Errorf("unknown layout journal %q in %s", id, root)
	}
	dir := filepath.Join(root, id)
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unknown layout journal %q in %s", id, root)
	}
	if err != nil {
		return err
	}
	var records []layoutJournalRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return fmt.Errorf("decode layout journal %s: %w", dir, err)
	}
	for _, record := range records {
		if record.Row != layoutRowStateDB && record.Row != layoutRowCacheDB {
			continue
		}
		for _, path := range []string{record.Destination, env.StateDB, env.CacheDB} {
			pids, err := dbHolderPIDs(
				env.ProcRoot,
				strings.TrimSuffix(strings.TrimSuffix(path, layoutDBWAL), layoutDBSHM),
			)
			if err != nil {
				return err
			}
			if len(pids) > 0 {
				return fmt.Errorf("rollback %s refused: database held by pid %s", id, strings.Join(pids, ","))
			}
		}
	}
	var failures []error
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if !layoutRecordAllowed(env, record) ||
			(record.Backup != "" && !strings.HasPrefix(record.Backup, filepath.Join(dir, "backup")+string(os.PathSeparator))) {
			failures = append(failures, fmt.Errorf("record %d has unsafe path", index))
			continue
		}
		if err := restoreLayoutRecord(ctx, record); err != nil {
			failures = append(failures, fmt.Errorf("record %d restore %s: %w", index, record.Destination, err))
			continue
		}
		fmt.Fprintf(stdout, "  rollback layout %s %s\n", record.Row, record.Destination)
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	_ = os.Remove(root)
	return nil
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
	return runner.Run(ctx, "sudo", "install", "-D", "-m", "0644", record.Backup, record.Destination)
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
		legacy := filepath.Join(env.Home, ".cc", legacyDBName)
		target := env.StateDB
		if record.Row == layoutRowCacheDB {
			legacy = filepath.Join(env.Home, ".local", "state", "pfm", legacyDBName)
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
			if allows(filepath.Join(dir, "settings.json")) {
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
	case layoutRowZshrc:
		return allows(filepath.Join(env.Home, ".zshrc"))
	case layoutRowStagedPrompts:
		return allows(filepath.Join(env.ManagedRoot, "harness-prompts"))
	case layoutRowSharedDB:
		return allows(filepath.Join(env.Home, ".local", "state", "pfm", "shared.db"))
	case layoutRowStrayDir:
		return allows(
			filepath.Join(env.Home, ".cc", ".git"),
			filepath.Join(env.Home, ".cc", ".codex"),
			filepath.Join(env.Home, ".cc", ".agents"),
		)
	}
	return false
}
