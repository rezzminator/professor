package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLayoutJournalRollbackRestoresPriorBytesAndLinks(t *testing.T) {
	env := layoutFixture(t)
	before, err := os.ReadFile(filepath.Join(env.Home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	journal := &Journal{env: env}
	finding := LayoutFinding{Row: "zshrc", Verdict: VerdictRepoint, Path: filepath.Join(env.Home, ".zshrc")}
	err = journal.mutate(finding, []string{finding.Path}, func() error {
		return os.WriteFile(finding.Path, []byte("changed\n"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	linkFinding := LayoutFinding{Row: "shared-db", Verdict: VerdictCreate, Path: created}
	if err := journal.mutate(linkFinding, []string{created}, func() error {
		return os.Symlink(filepath.Join(env.Home, ".claude"), created)
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(journal.dir, "journal.json"))
	if err != nil || !bytes.Contains(raw, []byte(`"backup"`)) || !bytes.Contains(raw, []byte(`"result": "applied"`)) {
		t.Fatalf("journal=%s err=%v", raw, err)
	}
	if err := RollbackLayout(
		context.Background(),
		env,
		filepath.Base(journal.dir),
		false,
		&bytes.Buffer{},
	); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(finding.Path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("restored zshrc=%q err=%v, want %q", after, err, before)
	}
	if _, err := os.Lstat(created); !os.IsNotExist(err) {
		t.Fatalf("created link survived rollback: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(journal.dir, "rolled-back"))
	if err != nil {
		t.Fatalf("rolled-back journal is not kept and marked: %v", err)
	}
	stamp := strings.TrimSuffix(string(marker), "\n")
	if _, err := time.Parse(time.RFC3339, stamp); err != nil || !strings.HasSuffix(string(marker), "\n") {
		t.Fatalf("rolled-back marker=%q err=%v, want one RFC 3339 line", marker, err)
	}
	for _, force := range []bool{false, true} {
		err = RollbackLayout(context.Background(), env, filepath.Base(journal.dir), force, &bytes.Buffer{})
		want := "rollback " + filepath.Base(journal.dir) + " refused: already rolled back at " + stamp
		if err == nil || err.Error() != want {
			t.Fatalf("second rollback force=%t err=%v, want %q", force, err, want)
		}
	}
}

func TestLayoutJournalUnknownIDNamesMigrationsDir(t *testing.T) {
	env := layoutFixture(t)
	err := RollbackLayout(context.Background(), env, "20260101T000000Z", false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(env.Home, ".local", "state", "pfm", "migrations")) {
		t.Fatalf("unknown journal error=%v", err)
	}
}

func TestLayoutRollbackRefusesDatabaseHolder(t *testing.T) {
	env := layoutFixture(t)
	journal := &Journal{env: env}
	if err := journal.snapshot(layoutRowStateDB, VerdictMove, env.StateDB); err != nil {
		t.Fatal(err)
	}
	fd := filepath.Join(env.ProcRoot, "4242", "fd")
	if err := os.MkdirAll(fd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(env.StateDB, filepath.Join(fd, "3")); err != nil {
		t.Fatal(err)
	}
	err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "held by pid 4242") {
		t.Fatalf("holder rollback error=%v", err)
	}
	if _, err := os.Stat(journal.dir); err != nil {
		t.Fatalf("refused rollback removed its journal: %v", err)
	}
}

func TestLayoutRollbackReportsEveryFailedRecordAndRestoresOthers(t *testing.T) {
	env := layoutFixture(t)
	path := filepath.Join(env.Home, ".zshrc")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := &Journal{env: env}
	if err := journal.mutate(
		LayoutFinding{Row: "zshrc", Verdict: VerdictRepoint, Path: path},
		[]string{path},
		func() error {
			return os.WriteFile(path, []byte("changed"), 0o600)
		},
	); err != nil {
		t.Fatal(err)
	}
	operator := filepath.Join(env.Home, "operator.txt")
	layoutWrite(t, operator, "keep")
	journal.records = append(
		journal.records,
		layoutJournalRecord{Row: "zshrc", Destination: operator, Result: "applied"},
	)
	if err := journal.flush(); err != nil {
		t.Fatal(err)
	}
	// The unsafe record carries no fingerprint; force lets the replay reach it.
	err = RollbackLayout(context.Background(), env, filepath.Base(journal.dir), true, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "record 1 has unsafe path") {
		t.Fatalf("partial rollback error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("healthy record not restored: %q err=%v", after, err)
	}
	if _, err := os.Stat(journal.dir); err != nil {
		t.Fatalf("partial rollback removed recovery journal: %v", err)
	}
	if got, err := os.ReadFile(operator); err != nil || string(got) != "keep" {
		t.Fatalf("rollback touched unlisted operator path: %q err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(journal.dir, "rolled-back")); !os.IsNotExist(err) {
		t.Fatalf("partial rollback marked its journal rolled back: %v", err)
	}
}

// journaledEngine is an applying engine whose writes record into a fresh
// install journal rooted at home.
func journaledEngine(t *testing.T, home string, apply bool) (*engine, *Journal, LayoutEnv) {
	t.Helper()
	env := LayoutEnv{Home: home}
	journal := NewJournal(context.Background(), env)
	journal.dryRun = !apply
	return &engine{
		options: Options{Home: home, Mode: ModeApply, Stdout: io.Discard, Journal: journal},
		apply:   apply, stamp: "test", managedRoot: managedRootForHome(home),
	}, journal, env
}

func installRecordDestinations(t *testing.T, journal *Journal) []string {
	t.Helper()
	var got []string
	for _, record := range journal.records {
		if record.Row != layoutRowInstall || record.Verdict != verdictInstallWrite || record.Result != "applied" {
			t.Fatalf("record=%+v, want an applied install write", record)
		}
		got = append(got, record.Destination)
	}
	return got
}

func rollbackInstallJournal(t *testing.T, env LayoutEnv, journal *Journal) string {
	t.Helper()
	var output bytes.Buffer
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.Dir()), false, &output); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestInstallJournalDryRunPlansAndNilJournalMatchesToday(t *testing.T) {
	home := t.TempDir()
	installer, journal, _ := journaledEngine(t, home, false)
	var planned bytes.Buffer
	installer.options.Stdout = &planned
	target := filepath.Join(home, ".claude", "commands", "x.md")
	if _, err := installer.ensureLink(filepath.Join(home, "new"), target); err != nil {
		t.Fatal(err)
	}
	if err := installer.retire(target, "never there"); err != nil {
		t.Fatal(err)
	}
	if got := journal.Planned(); len(got) != 1 || got[0] != filepath.Join(home, ".claude") {
		t.Fatalf("Planned()=%v, want the highest missing ancestor", got)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local")); !errors.Is(err, os.ErrNotExist) || journal.Dir() != "" {
		t.Fatalf("dry run wrote a journal: dir=%q err=%v", journal.Dir(), err)
	}
	outputs := map[bool]string{}
	for _, withJournal := range []bool{true, false} {
		runHome := t.TempDir()
		runner, _, _ := journaledEngine(t, runHome, true)
		if !withJournal {
			runner.options.Journal = nil
		}
		var output bytes.Buffer
		runner.options.Stdout = &output
		link := filepath.Join(runHome, "link")
		if _, err := runner.ensureLink(filepath.Join(runHome, "new"), link); err != nil {
			t.Fatal(err)
		}
		if target, err := os.Readlink(link); err != nil || target != filepath.Join(runHome, "new") {
			t.Fatalf("journal=%t link=%q err=%v", withJournal, target, err)
		}
		outputs[withJournal] = strings.ReplaceAll(output.String(), runHome, "{home}")
	}
	if outputs[true] == "" || outputs[true] != outputs[false] {
		t.Fatalf("journaled output %q differs from the nil-journal output %q", outputs[true], outputs[false])
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestInstallJournalFailedWriteStaysRecordedForRollback(t *testing.T) {
	home := t.TempDir()
	installer, journal, env := journaledEngine(t, home, true)
	target := filepath.Join(home, ".zshrc")
	writeFile(t, target, "before\n", 0o600)
	failure := errors.New("step failed")
	err := installer.changePaths("write "+target, []string{target}, func() error {
		if err := os.WriteFile(target, []byte("half\n"), 0o600); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || len(journal.records) != 1 || journal.records[0].Result != "pending" {
		t.Fatalf("err=%v records=%+v, want the failure and one pending record", err, journal.records)
	}
	rollbackInstallJournal(t, env, journal)
	if got := readFile(t, target); got != "before\n" {
		t.Fatalf("rollback left %q", got)
	}
}

func TestInstallJournalTwoPhaseRecordAndDryRunPlan(t *testing.T) {
	home := t.TempDir()
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	target := filepath.Join(home, ".codex", "config.toml")
	writeFile(t, target, "model = \"a\"\n", 0o600)
	if err := journal.before(target); err != nil {
		t.Fatal(err)
	}
	results := func() string {
		var records []layoutJournalRecord
		raw, err := os.ReadFile(filepath.Join(journal.Dir(), "journal.json"))
		if err == nil {
			err = json.Unmarshal(raw, &records)
		}
		if err != nil || len(records) != 1 || records[0].Row != "install" || records[0].Destination != target {
			t.Fatalf("records=%+v err=%v, want one install record for %s", records, err, target)
		}
		return records[0].Result
	}
	if got := results(); got != "pending" {
		t.Fatalf("before() result=%q, want pending", got)
	}
	if err := journal.markApplied(); err != nil {
		t.Fatal(err)
	}
	if got := results(); got != "applied" {
		t.Fatalf("markApplied() result=%q, want applied", got)
	}
	preview := NewJournal(context.Background(), LayoutEnv{Home: t.TempDir()})
	preview.dryRun = true
	if err := preview.before(target); err != nil {
		t.Fatal(err)
	}
	if err := preview.markApplied(); err != nil {
		t.Fatal(err)
	}
	if got := preview.Planned(); preview.Dir() != "" || len(got) != 1 || got[0] != target {
		t.Fatalf("dry-run dir=%q Planned()=%v, want no dir and %s", preview.Dir(), got, target)
	}
}

func TestInstallJournalRollbackRefusesAnInstallRecordOutsideTheAllowlist(t *testing.T) {
	env := layoutFixture(t)
	dir := filepath.Join(env.Home, ".local", "state", "pfm", "migrations", "20260101T000000Z")
	outside := filepath.Join(filepath.Dir(env.Home), "outside")
	for index, destination := range []string{outside, filepath.Join(filepath.Dir(dir), "x")} {
		raw, err := json.Marshal([]layoutJournalRecord{{
			Row: "install", Verdict: "write", Source: destination, Destination: destination, Result: "applied",
		}})
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "journal.json"), string(raw), 0o600)
		err = RollbackLayout(context.Background(), env, filepath.Base(dir), false, io.Discard)
		if err == nil || err.Error() != "record 0 has unsafe path" {
			t.Fatalf("case %d rollback err=%v, want record 0 has unsafe path", index, err)
		}
	}
	allowed := layoutJournalRecord{Row: "install", Destination: filepath.Join(env.Home, ".zshrc")}
	if !layoutRecordAllowed(env, allowed) {
		t.Fatalf("install record under home refused: %+v", allowed)
	}
}

// driftFixture applies three journaled changes an install could make — a file
// rewrite, a created link and a created tree — each left with a fixed mtime.
func driftFixture(t *testing.T) (LayoutEnv, *Journal, string, string, string, []byte) {
	t.Helper()
	env := layoutFixture(t)
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	file := filepath.Join(env.Home, ".zshrc")
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	tree := filepath.Join(env.ManagedRoot, "harness-prompts")
	journal := &Journal{env: env}
	for _, step := range []struct {
		finding LayoutFinding
		action  func() error
	}{
		{LayoutFinding{Row: layoutRowZshrc, Verdict: VerdictRepoint, Path: file}, func() error {
			if err := os.WriteFile(file, []byte("changed\n"), 0o600); err != nil {
				return err
			}
			return os.Chtimes(file, old, old)
		}},
		{LayoutFinding{Row: layoutRowSharedDB, Verdict: VerdictCreate, Path: link}, func() error {
			return os.Symlink(filepath.Join(env.Home, ".claude"), link)
		}},
		{LayoutFinding{Row: layoutRowStagedPrompts, Verdict: VerdictCreate, Path: tree}, func() error {
			if err := os.MkdirAll(tree, 0o700); err != nil {
				return err
			}
			child := filepath.Join(tree, "claude.md")
			if err := os.WriteFile(child, []byte("prompt"), 0o600); err != nil {
				return err
			}
			return errors.Join(os.Chtimes(child, old, old), os.Chtimes(tree, old, old))
		}},
	} {
		if err := journal.mutate(step.finding, []string{step.finding.Path}, step.action); err != nil {
			t.Fatal(err)
		}
	}
	return env, journal, file, link, tree, original
}

func TestLayoutRollbackRefusesDriftUnlessForced(t *testing.T) {
	later := time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, test := range []struct {
		name  string
		drift func(t *testing.T, file, link, tree string) []string
	}{
		{"bytes", func(t *testing.T, file, _, _ string) []string {
			layoutWrite(t, file, "chanGed\n")
			return []string{file}
		}},
		{"size", func(t *testing.T, file, _, _ string) []string {
			layoutWrite(t, file, "changed further\n")
			if err := os.Chtimes(file, time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC), time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			return []string{file}
		}},
		{"mode", func(t *testing.T, file, _, _ string) []string {
			if err := os.Chmod(file, 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{file}
		}},
		{"mtime", func(t *testing.T, file, _, _ string) []string {
			if err := os.Chtimes(file, later, later); err != nil {
				t.Fatal(err)
			}
			return []string{file}
		}},
		{"link target", func(t *testing.T, _, link, _ string) []string {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(filepath.Dir(link), "elsewhere"), link); err != nil {
				t.Fatal(err)
			}
			return []string{link}
		}},
		{"added child", func(t *testing.T, _, _, tree string) []string {
			layoutWrite(t, filepath.Join(tree, "newer.md"), "newer work")
			return []string{tree}
		}},
		{"removed child", func(t *testing.T, _, _, tree string) []string {
			if err := os.Remove(filepath.Join(tree, "claude.md")); err != nil {
				t.Fatal(err)
			}
			return []string{tree}
		}},
		{"every drifted destination", func(t *testing.T, file, _, tree string) []string {
			if err := os.Chtimes(file, later, later); err != nil {
				t.Fatal(err)
			}
			layoutWrite(t, filepath.Join(tree, "newer.md"), "newer work")
			return []string{file, tree}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			env, journal, file, link, tree, original := driftFixture(t)
			drifted := test.drift(t, file, link, tree)
			id := filepath.Base(journal.dir)
			current, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			err = RollbackLayout(context.Background(), env, id, false, io.Discard)
			want := "rollback " + id + " refused: drift at " + strings.Join(drifted, ", ") +
				" — rerun with --force to overwrite them"
			if err == nil || err.Error() != want {
				t.Fatalf("drifted rollback err=%v, want %q", err, want)
			}
			if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, current) {
				t.Fatalf("refused rollback touched %s: %q err=%v", file, got, err)
			}
			for _, path := range []string{link, tree} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("refused rollback removed %s: %v", path, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(journal.dir, "rolled-back")); !os.IsNotExist(err) {
				t.Fatalf("refused rollback marked the journal: %v", err)
			}
			if err := RollbackLayout(context.Background(), env, id, true, io.Discard); err != nil {
				t.Fatalf("forced rollback: %v", err)
			}
			if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, original) {
				t.Fatalf("forced rollback left %s=%q err=%v, want %q", file, got, err, original)
			}
			for _, path := range []string{link, tree} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("forced rollback kept created %s: %v", path, err)
				}
			}
		})
	}
}

func TestLayoutRollbackChecksOnlyTheLastRecordOfADestination(t *testing.T) {
	env := layoutFixture(t)
	path := filepath.Join(env.Home, ".zshrc")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	journal := &Journal{env: env}
	for _, content := range []string{"first rewrite, longer\n", "second\n"} {
		if err := journal.mutate(
			LayoutFinding{Row: layoutRowZshrc, Verdict: VerdictRepoint, Path: path},
			[]string{path},
			func() error { return os.WriteFile(path, []byte(content), 0o600) },
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, io.Discard); err != nil {
		t.Fatalf("rollback of a twice-recorded destination: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("restored %s=%q err=%v, want %q", path, got, err, original)
	}
}

func TestLayoutRollbackExemptsPendingCacheAndSidecarRecords(t *testing.T) {
	env := layoutFixture(t)
	journal := &Journal{env: env}
	wal := env.StateDB + layoutDBWAL
	for _, change := range []struct {
		finding LayoutFinding
	}{
		{LayoutFinding{Row: layoutRowCacheDB, Verdict: VerdictMove, Path: env.CacheDB}},
		{LayoutFinding{Row: layoutRowStateDB, Verdict: VerdictMove, Path: wal}},
	} {
		if err := journal.mutate(change.finding, []string{change.finding.Path}, func() error {
			return os.WriteFile(change.finding.Path, []byte("moved"), 0o600)
		}); err != nil {
			t.Fatal(err)
		}
	}
	zshrc := filepath.Join(env.Home, ".zshrc")
	if err := journal.before(zshrc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{env.CacheDB, wal, zshrc} {
		layoutWrite(t, path, "written after the install, longer")
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, io.Discard); err != nil {
		t.Fatalf("exempt records refused rollback: %v", err)
	}
	if got := readFile(t, env.CacheDB); got != "cache" {
		t.Fatalf("cache db=%q, want restored", got)
	}
}

func TestLayoutRollbackCountsAnUnfingerprintedRecordAsDrift(t *testing.T) {
	env := layoutFixture(t)
	path := filepath.Join(env.Home, ".zshrc")
	journal := &Journal{env: env}
	if err := journal.mutate(
		LayoutFinding{Row: layoutRowZshrc, Verdict: VerdictRepoint, Path: path},
		[]string{path},
		func() error { return os.WriteFile(path, []byte("changed\n"), 0o600) },
	); err != nil {
		t.Fatal(err)
	}
	journal.records[0].After = ""
	if err := journal.flush(); err != nil {
		t.Fatal(err)
	}
	id := filepath.Base(journal.dir)
	err := RollbackLayout(context.Background(), env, id, false, io.Discard)
	want := "rollback " + id + " refused: drift at " + path + " (no fingerprint) — rerun with --force to overwrite them"
	if err == nil || err.Error() != want {
		t.Fatalf("unfingerprinted rollback err=%v, want %q", err, want)
	}
}

func TestLayoutRollbackAcceptsTheStateDBAfterRefingerprint(t *testing.T) {
	env := layoutFixture(t)
	journal := &Journal{env: env}
	if err := journal.mutate(
		LayoutFinding{Row: layoutRowStateDB, Verdict: VerdictMove, Path: env.StateDB},
		[]string{env.StateDB},
		func() error { return os.WriteFile(env.StateDB, []byte("moved"), 0o600) },
	); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, env.StateDB, "moved and migrated by the install")
	if err := journal.Refingerprint(env.StateDB); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(journal.dir), false, io.Discard); err != nil {
		t.Fatalf("rollback after the install's own migration: %v", err)
	}
	if got := readFile(t, env.StateDB); got != "state" {
		t.Fatalf("state db=%q, want restored", got)
	}
}

func TestLayoutRollbackRefusesSessionStoreWhileAChatIsLive(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	layoutWrite(t, filepath.Join(account, "sessions", "4242.json"), `{}`)
	if err := os.Mkdir(filepath.Join(env.ProcRoot, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry := SessionPaths[0]
	for _, destination := range []string{filepath.Join(account, entry), filepath.Join(env.Home, ".claude", entry)} {
		journal := &Journal{env: env}
		if err := journal.mutate(
			LayoutFinding{Row: layoutRowSessionStore, Verdict: VerdictRepoint, Path: destination},
			[]string{destination},
			func() error { return nil },
		); err != nil {
			t.Fatal(err)
		}
		id := filepath.Base(journal.dir)
		for _, force := range []bool{false, true} {
			err := RollbackLayout(context.Background(), env, id, force, io.Discard)
			want := "rollback " + id + " refused: live chats on " + account + ": 4242"
			if err == nil || err.Error() != want {
				t.Fatalf("%s force=%t err=%v, want %q", destination, force, err, want)
			}
		}
	}
}
