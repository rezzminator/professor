package hookentry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runRRDir(t *testing.T, payload, home string) (context, stderr string) {
	t.Helper()
	var stdout, errOut bytes.Buffer
	if code := RRDir(strings.NewReader(payload), &stdout, &errOut, home); code != 0 {
		t.Fatalf("exit code = %d, want 0 on every path (fail-open)", code)
	}
	var response struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("stdout is not the hook response: %v\n%s", err, stdout.String())
	}
	if response.HookSpecificOutput.HookEventName != "SubagentStart" {
		t.Fatalf("hookEventName = %q", response.HookSpecificOutput.HookEventName)
	}
	return response.HookSpecificOutput.AdditionalContext, errOut.String()
}

func rrDirPayload(t *testing.T, cwd string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"cwd": cwd, "agent_type": "rr"})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRRDirNamesTheNearestAncestorLedger(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "outer", "project")
	cwd := filepath.Join(project, "sub", "dir")
	for _, dir := range []string{cwd, filepath.Join(project, ".professor"), filepath.Join(root, "outer", ".professor")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, stderr := runRRDir(t, rrDirPayload(t, cwd), filepath.Join(root, "home"))
	if want := "RR-DIR: " + filepath.Join(project, ".professor", "RR"); got != want {
		t.Fatalf("context = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr on the found path: %q", stderr)
	}
	if _, err := os.Lstat(filepath.Join(project, ".professor", "RR")); !os.IsNotExist(err) {
		t.Fatalf("the hook created the RR directory (err=%v)", err)
	}
}

func TestRRDirSkipsAProfessorFileAndFallsBackToTheCloneLedger(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "unmanaged")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// A FILE named .professor is not a ledger.
	if err := os.WriteFile(filepath.Join(cwd, ".professor"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	got, _ := runRRDir(t, rrDirPayload(t, cwd), home)
	want := "RR-DIR: " + filepath.Join(home, ".professor", ".professor", "RR") +
		" (fallback: no .professor/ above " + cwd + ")"
	if got != want {
		t.Fatalf("context = %q, want %q", got, want)
	}
}

func TestRRDirReportsAFailedLookAsAnErrorLineNeverSilence(t *testing.T) {
	for name, tc := range map[string]struct{ payload, home, want string }{
		"undecodable payload": {"{not json", "/home/x", "RR-DIR-ERROR: decode hook payload"},
		"empty payload":       {"", "/home/x", "RR-DIR-ERROR: decode hook payload"},
		"missing cwd":         {`{"agent_type":"rr"}`, "/home/x", `RR-DIR-ERROR: hook payload cwd "" is not an absolute path`},
		"relative cwd":        {`{"cwd":"some/dir"}`, "/home/x", "is not an absolute path"},
	} {
		t.Run(name, func(t *testing.T) {
			got, stderr := runRRDir(t, tc.payload, tc.home)
			if !strings.HasPrefix(got, "RR-DIR-ERROR: ") || !strings.Contains(got, tc.want) {
				t.Fatalf("context = %q, want an RR-DIR-ERROR line containing %q", got, tc.want)
			}
			if !strings.Contains(stderr, "pfm internal rr-dir:") {
				t.Fatalf("the failure was not logged to stderr: %q", stderr)
			}
		})
	}
	t.Run("no home for the fallback", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		got, _ := runRRDir(t, rrDirPayload(t, root), "")
		if !strings.HasPrefix(got, "RR-DIR-ERROR: ") || !strings.Contains(got, "no home directory") {
			t.Fatalf("context = %q", got)
		}
	})
}
