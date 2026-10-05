package reload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func reloadWorkbenchFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "acme")
	scribe, duo := filepath.Join(root, "docs", "scribe"), filepath.Join(root, "docs", "duo")
	for path, body := range map[string]string{
		filepath.Join(root, ".professor", "baseline.json"): "{}",
		paths.WorkbenchManifest(scribe):                    `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(scribe, ".professor", "scribe.md"):   "You are scribe.",
		paths.WorkbenchManifest(duo):                       `{"prompt":"duo.md","engines":["codex","claude"],"model":"gpt-x","effort":"high"}`,
		filepath.Join(duo, ".professor", "duo.md"):         "You are duo.",
		filepath.Join(duo, "CLAUDE.md"):                    "Duo.\n",
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, scribe, duo
}

func TestReloadWorkbench(t *testing.T) {
	for _, name := range []string{"effort", "explicit", "Codex", "disabled", "outside", "mirror failure"} {
		t.Run(name, func(t *testing.T) {
			root, scribe, duo := reloadWorkbenchFixture(t)
			request := Request{
				Engine:        pfmengine.Claude,
				CWD:           scribe,
				Home:          t.TempDir(),
				PromptChannel: "keep explicit channel",
			}
			want := request
			want.Effort = "xhigh"
			var wantError string
			switch name {
			case "explicit":
				request.Effort, want.Effort = "low", "low"
			case "Codex", "mirror failure":
				request.Engine, request.CWD = pfmengine.Codex, duo
				want = request
				want.Model, want.Effort = "gpt-x", "high"
				if name == "mirror failure" {
					if err := atomicfile.Write(filepath.Join(duo, ".mcp.json"), []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
					persona, err := workbench.ForLaunch(duo, pfmengine.Codex, workbench.Resume)
					if err != nil {
						t.Fatal(err)
					}
					err = workbench.EnsureMirror(persona.Bench, pfmengine.Codex, request.Home)
					if err == nil {
						t.Fatal("mirror fixture did not fail")
					}
					wantError = err.Error()
				}
			case "disabled":
				request.Engine = pfmengine.Codex
				want = request
			case "outside":
				request.CWD = filepath.Join(root, "src")
				want = request
			}
			got, err := applyReloadWorkbench(request)
			if wantError != "" {
				if err == nil || err.Error() != wantError {
					t.Fatalf("error = %v, want %q", err, wantError)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("request = %#v, err = %v; want %#v", got, err, want)
			}
			if name == "Codex" {
				body, err := os.ReadFile(filepath.Join(duo, "AGENTS.md"))
				if err != nil || !strings.Contains(string(body), "Duo.") {
					t.Fatalf("Codex mirror = %q, err = %v", body, err)
				}
				persona, err := workbench.ForLaunch(duo, pfmengine.Codex, workbench.Resume)
				if err != nil {
					t.Fatal(err)
				}
				if stale, err := workbench.CheckMirror(
					persona.Bench,
					pfmengine.Codex,
					request.Home,
				); err != nil ||
					stale != "" {
					t.Fatalf("mirror stale = %q, err = %v", stale, err)
				}
			}
		})
	}
}

type refusedWorkbenchTmux struct {
	fakeReloadTmux
	calls int
}

func (tmux *refusedWorkbenchTmux) SetRemain(context.Context, string, string, bool) error {
	tmux.calls++
	return errors.New("tmux was reached")
}

func TestReloadWorkbenchInvalidBeforePaneLock(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "before tmux", true: "before lock"}[held], func(t *testing.T) {
			_, scribe, _ := reloadWorkbenchFixture(t)
			if err := atomicfile.Write(paths.WorkbenchManifest(scribe), []byte(`{"prompt":""}`), 0o600); err != nil {
				t.Fatal(err)
			}
			sid := t.TempDir()
			request := Request{
				Engine:     pfmengine.Claude,
				CWD:        scribe,
				SocketPath: filepath.Join(sid, "probe"),
				Pane:       "%7",
				Account:    1,
				AccountIDs: []int{1},
				Home:       t.TempDir(),
			}
			if held {
				lock, err := os.OpenFile(LockPath(sid, "probe", "%7"), os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lock.Close() })
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
			tmux := &refusedWorkbenchTmux{}
			_, err := Run(
				context.Background(),
				request,
				Options{SIDDir: sid, Delay: -1, Poll: -1},
				tmux,
				fakeReloadProc{},
				nil,
			)
			want := paths.WorkbenchManifest(scribe) + `: "prompt" is required`
			if err == nil || err.Error() != want || tmux.calls != 0 {
				t.Fatalf("Run error = %v, tmux calls = %d; want %q before any tmux call", err, tmux.calls, want)
			}
		})
	}
}
