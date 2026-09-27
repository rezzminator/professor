package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
