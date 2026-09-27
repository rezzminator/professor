package installer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestParseLsofHolders(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, stderr, wantErr string
		exit                          int
		want                          []string
	}{
		{"holders", "456\n123\n456\n", "", "", 0, []string{"123", "456"}},
		{"none", "", "", "", 1, nil},
		{"status error", "", "lsof: status error on db\n", "database holder probe: lsof exited 1: lsof: status error on db", 1, nil},
		{"other exit", "", "failure\n", "database holder probe: lsof exited 2: failure", 2, nil},
		{"bad output", "abc", "", "database holder probe: lsof printed \"abc\"", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLsofHolders(tc.stdout, tc.stderr, tc.exit)
			if !reflect.DeepEqual(got, tc.want) ||
				(err != nil && err.Error() != tc.wantErr) || (err == nil && tc.wantErr != "") {
				t.Fatalf("parseLsofHolders = %v, %v; want %v, %q", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestLsofHolderTargets(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	for _, suffix := range []string{"", layoutDBWAL} {
		if err := os.WriteFile(db+suffix, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := lsofHolderTargets(db)
	if err != nil || !reflect.DeepEqual(got, []string{db, db + layoutDBWAL}) {
		t.Fatalf("targets = %v, %v", got, err)
	}
	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(db + layoutDBWAL); err != nil {
		t.Fatal(err)
	}
	got, err = lsofHolderTargets(db)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing targets = %v, %v", got, err)
	}
	if err := os.Symlink(strings.Repeat("x", 1), db); err != nil {
		t.Fatal(err)
	}
	got, err = lsofHolderTargets(db)
	if err != nil || !reflect.DeepEqual(got, []string{db}) {
		t.Fatalf("symlink target = %v, %v", got, err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lsofHolderTargets(filepath.Join(blocked, "db")); err == nil {
		t.Fatal("Lstat error was treated as no targets")
	}
}

func TestLayoutLiveChatsRefuseAndUnreadableSessionsFail(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	layoutWrite(t, filepath.Join(account, "sessions", "123.json"), `{}`)
	if err := os.Mkdir(filepath.Join(env.ProcRoot, "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	findings := ClassifyLayout(env)
	for _, check := range []struct{ row, path string }{{"session-store", filepath.Join(account, "projects")}, {"account-settings", filepath.Join(account, "settings.json")}, {"account-mcp", filepath.Join(account, ".claude.json")}} {
		finding := requireLayoutVerdict(t, findings, check.row, check.path, VerdictRefuse)
		if finding.Detail != "live chats: 123" {
			t.Errorf("%s detail=%q", check.row, finding.Detail)
		}
	}
	if err := os.Remove(filepath.Join(account, "sessions", "123.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(account, "sessions")); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(account, "sessions"), "not a directory")
	finding := layoutFinding(t, ClassifyLayout(env), "session-store", filepath.Join(account, "projects"))
	if finding.Err == nil {
		t.Fatal("unreadable sessions directory looked clean")
	}
}

func TestLayoutDatabaseHolderRefusesAndUnreadableProcFails(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.StateDB); err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyStateDB(env.Home)
	layoutWrite(t, legacy, "state")
	fd := filepath.Join(env.ProcRoot, "456", "fd")
	if err := os.MkdirAll(fd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, filepath.Join(fd, "0")); err != nil {
		t.Fatal(err)
	}
	finding := requireLayoutVerdict(t, ClassifyLayout(env), "state-db", env.StateDB, VerdictRefuse)
	if finding.Detail != "held by pid 456" {
		t.Fatalf("holder detail=%q", finding.Detail)
	}
	env.ProcRoot = filepath.Join(env.Home, "missing-proc")
	if finding := layoutFinding(t, ClassifyLayout(env), "state-db", env.StateDB); finding.Err == nil {
		t.Fatal("missing /proc looked clean")
	}
}

func TestLayoutStagedPromptsInUseRefuse(t *testing.T) {
	env := layoutFixture(t)
	staged := filepath.Join(env.ManagedRoot, "harness-prompts")
	layoutWrite(t, filepath.Join(staged, "claude.md"), "old")
	layoutWrite(
		t,
		filepath.Join(env.ProcRoot, "789", "cmdline"),
		"claude\x00"+filepath.Join(staged, "claude.md")+"\x00",
	)
	finding := requireLayoutVerdict(t, ClassifyLayout(env), "staged-prompts", staged, VerdictRefuse)
	if finding.Detail != "in use by 1 live chats" {
		t.Fatalf("staged detail=%q", finding.Detail)
	}
}

// A normal user cannot read another user's /proc/{pid}/fd (EACCES): the holder
// scan skips that process instead of failing every database row.
func TestDBHolderScanSkipsAnotherUsersProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the fence runs as root, where permission bits deny nothing")
	}
	procRoot := t.TempDir()
	db := filepath.Join(t.TempDir(), "fleet.db")
	foreign := filepath.Join(procRoot, "1", "fd")
	holder := filepath.Join(procRoot, "4242", "fd")
	for _, dir := range []string{foreign, holder} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(db, filepath.Join(holder, "3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(foreign, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(foreign, 0o700) })
	pids, err := dbHolderPIDs(procRoot, db)
	if err != nil || len(pids) != 1 || pids[0] != "4242" {
		t.Fatalf("dbHolderPIDs = %v, %v; want [4242] with the unreadable process skipped", pids, err)
	}
}
