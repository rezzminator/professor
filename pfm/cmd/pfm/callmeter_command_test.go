package main

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
)

// callmeterLab is a jailed home with two configured Claude config dirs.
type callmeterLab struct {
	config    string
	home      string
	accounts  [2]string // physical config dirs
	storePath string
}

func newCallmeterLab(t *testing.T) callmeterLab {
	t.Helper()
	root := jailTest(t)
	lab := callmeterLab{home: jailPaths(t).Home}
	for index := range lab.accounts {
		dir := filepath.Join(root, fmt.Sprintf("account-%d", index+1))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		physical, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		lab.accounts[index] = physical
	}
	lab.config = writeConfigFixture(t, root, fmt.Sprintf(
		`{"version":1,"accounts":[{"id":1,"configDir":%q},{"id":2,"configDir":%q}]}`,
		lab.accounts[0], lab.accounts[1],
	))
	lab.storePath = callmeter.DefaultPath(lab.home)
	return lab
}

func (lab callmeterLab) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code = run(append([]string{"--config", lab.config, "callmeter"}, args...), &out, &errs)
	return code, out.String(), errs.String()
}

// seedRead records one Read of file in configDir through the store's own API.
func (lab callmeterLab) seedRead(t *testing.T, id, file, configDir string) {
	t.Helper()
	ctx := context.Background()
	store, err := callmeter.OpenDB(ctx, lab.storePath)
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

// TestCallmeterReportFilesPrintsSeededRead proves `pfm callmeter` dispatches to
// internal/callmeter/command with the runtime it resolved.
func TestCallmeterReportFilesPrintsSeededRead(t *testing.T) {
	lab := newCallmeterLab(t)
	lab.seedRead(t, "toolu_1", "/work/proj/notes.md", lab.accounts[0])
	code, stdout, stderr := lab.run(t, "report", "files")
	if code != 0 || !strings.Contains(stdout, "/work/proj/notes.md") {
		t.Fatalf("report files = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}
