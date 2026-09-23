package command

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// lab is a scratch home with two configured Claude config dirs.
type lab struct {
	runtime   pfmconfig.Runtime
	accounts  [2]string // physical config dirs
	storePath string
}

func newLab(t *testing.T) lab {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	fixture := lab{runtime: pfmconfig.Runtime{Paths: paths.Values{Home: home, DB: filepath.Join(home, "pfm.db")}}}
	for index := range fixture.accounts {
		dir := filepath.Join(root, fmt.Sprintf("account-%d", index+1))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fixture.accounts[index] = dir
		fixture.runtime.Config.Accounts = append(fixture.runtime.Config.Accounts,
			pfmconfig.Account{ID: index + 1, ConfigDir: dir})
	}
	fixture.storePath = callmeter.DefaultPath(home)
	return fixture
}

func (fixture lab) run(args ...string) (code int, stdout, stderr string) {
	var out, errs bytes.Buffer
	code = CLI(args, &out, &errs, fixture.runtime)
	return code, out.String(), errs.String()
}

// seedRead records one Read of file in configDir through the store's own API.
func (fixture lab) seedRead(t *testing.T, id, file, configDir string) {
	t.Helper()
	ctx := context.Background()
	store, err := callmeter.OpenDB(ctx, fixture.storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	}()
	call := callmeter.Call{
		ToolUseID: id, SessionID: callmeter.Ptr("sess-1"), AgentID: callmeter.Ptr(""),
		TS: callmeter.Ptr(time.Now().UnixMilli()), Tool: callmeter.Ptr("Read"),
		Cwd: callmeter.Ptr(filepath.Dir(file)), FilePath: callmeter.Ptr(file),
		BytesDelivered: callmeter.Ptr(int64(1234)), Source: callmeter.Ptr("hook"),
		ConfigDir: callmeter.Ptr(configDir),
	}
	if err := store.UpsertCall(ctx, call, callmeter.Overwrite); err != nil {
		t.Fatalf("seed call: %v", err)
	}
}

func TestCallmeterCLIReportWithoutStoreSaysSoAndCreatesNothing(t *testing.T) {
	fixture := newLab(t)
	code, stdout, stderr := fixture.run("report", "files")
	want := "callmeter: no store at " + fixture.storePath + ": nothing recorded yet"
	if code != 0 || !strings.Contains(stdout, want) {
		t.Fatalf("report without store = %d, want 0 and %q\nstdout:\n%s\nstderr:\n%s", code, want, stdout, stderr)
	}
	if _, err := os.Stat(fixture.storePath); !os.IsNotExist(err) {
		t.Fatalf("report created the store %s (stat err %v)", fixture.storePath, err)
	}
}

func TestCallmeterCLIReportUnreadableStoreFails(t *testing.T) {
	for name, stage := range map[string]func(path string) error{
		"directory": func(path string) error { return os.MkdirAll(path, 0o700) },
		"garbage": func(path string) error {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			return os.WriteFile(path, bytes.Repeat([]byte("not a database "), 512), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newLab(t)
			if err := stage(fixture.storePath); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := fixture.run("report", "files")
			if code != 1 || !strings.Contains(stderr, "callmeter: cannot open store "+fixture.storePath) {
				t.Fatalf("report over a %s store = %d\nstdout:\n%s\nstderr:\n%s", name, code, stdout, stderr)
			}
		})
	}
}

func TestCallmeterCLIUnknownTopicOrActionPrintsUsage(t *testing.T) {
	fixture := newLab(t)
	for _, args := range [][]string{{"report", "bogus"}, {"report"}, {"bogus"}, {"report", "files", "--bogus"}, {}} {
		code, _, stderr := fixture.run(args...)
		if code != 2 || !strings.Contains(stderr, "usage: pfm callmeter report") {
			t.Fatalf("callmeter %q = %d, want 2 with usage\nstderr:\n%s", args, code, stderr)
		}
	}
	if code, stdout, _ := fixture.run("help"); code != 0 || !strings.Contains(stdout, "usage: pfm callmeter report") {
		t.Fatalf("callmeter help = %d, want 0 with usage on stdout\nstdout:\n%s", code, stdout)
	}
}

func TestCallmeterCLIConfigDirNarrowsAndRejectsUnknown(t *testing.T) {
	fixture := newLab(t)
	fixture.seedRead(t, "toolu_1", "/work/one.md", fixture.accounts[0])
	fixture.seedRead(t, "toolu_2", "/work/two.md", fixture.accounts[1])
	code, stdout, stderr := fixture.run("report", "files", "--config-dir", fixture.accounts[1])
	if code != 0 || !strings.Contains(stdout, "/work/two.md") || strings.Contains(stdout, "/work/one.md") {
		t.Fatalf("narrowed report = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	nowhere := filepath.Join(fixture.runtime.Paths.Home, "nowhere")
	code, _, stderr = fixture.run("report", "files", "--config-dir", nowhere)
	if code != 2 || !strings.Contains(stderr, "is not a configured Claude config dir") ||
		!strings.Contains(stderr, fixture.accounts[0]) || !strings.Contains(stderr, fixture.accounts[1]) {
		t.Fatalf("unknown --config-dir = %d, want 2 naming the configured dirs\nstderr:\n%s", code, stderr)
	}
}

// TestCallmeterCLIAccountsSharingProjectsAreOneHistory: accounts whose
// projects/ is one directory (a symlink, as on this machine) hold one copy of
// every chat. The hook names that chat's config dir once
// (callmeter.ProjectsHome), and a report narrowed to either account shows the
// same chat: a chat's history stays consistent across accounts.
func TestCallmeterCLIAccountsSharingProjectsAreOneHistory(t *testing.T) {
	fixture := newLab(t)
	shared := filepath.Join(fixture.accounts[1], "projects")
	if err := os.RemoveAll(shared); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.accounts[0], "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.accounts[0], "projects"), shared); err != nil {
		t.Fatal(err)
	}
	fixture.seedRead(t, "toolu_S", "/work/proj/shared.md", callmeter.ProjectsHome(fixture.accounts[0]))
	for _, account := range fixture.accounts {
		code, stdout, stderr := fixture.run("report", "files", "--config-dir", account)
		if code != 0 || !strings.Contains(stdout, "/work/proj/shared.md") {
			t.Fatalf("report narrowed to %s = %d, want the shared chat\nstdout:\n%s\nstderr:\n%s",
				account, code, stdout, stderr)
		}
	}
}
