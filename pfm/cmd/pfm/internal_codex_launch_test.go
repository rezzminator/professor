package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestCodexLaunchPolicyAndHygiene(t *testing.T) {
	jailTest(t)
	binary, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "yolo-on", false: "yolo-off"}[enabled], func(t *testing.T) {
			previous := hookentry.LaunchExec
			t.Cleanup(func() { hookentry.LaunchExec = previous })
			runtime := pfmconfig.Runtime{
				Config: pfmconfig.Config{Codex: pfmconfig.CodexPrefs{Binary: binary, Yolo: enabled}},
				Paths:  paths.Values{Home: t.TempDir()},
			}
			called := false
			hookentry.LaunchExec = func(path string, args, env []string) error {
				called = true
				want := []string{binary}
				if enabled {
					want = append(want, "--dangerously-bypass-approvals-and-sandbox")
				}
				want = append(want, "--resume", "literal prompt")
				if path != binary || !reflect.DeepEqual(args, want) {
					t.Fatalf("exec = %q %q, want %q", path, args, want)
				}
				for _, name := range claudelaunch.Hygiene() {
					for _, entry := range env {
						if strings.HasPrefix(entry, name+"=") {
							t.Fatalf("inherited %s", name)
						}
					}
				}
				return errors.New("exec intercepted")
			}
			var stderr bytes.Buffer
			code := hookentry.CodexLaunch([]string{"--resume", "literal prompt"}, &stderr, runtime)
			if !called || code != 1 {
				t.Fatalf("called=%v code=%d stderr=%s", called, code, stderr.String())
			}
		})
	}
}

func TestCodexLaunchBinaryAndNoArguments(t *testing.T) {
	jailTest(t)
	binary, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	previous := hookentry.LaunchExec
	t.Cleanup(func() { hookentry.LaunchExec = previous })
	for _, configured := range []bool{true, false} {
		runtime := pfmconfig.Runtime{Paths: paths.Values{Home: t.TempDir()}}
		if configured {
			runtime.Config.Codex.Binary = binary
		} else {
			pathDir := t.TempDir()
			if err := os.Symlink(binary, filepath.Join(pathDir, "codex")); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			binary = filepath.Join(pathDir, "codex")
		}
		hookentry.LaunchExec = func(path string, args, _ []string) error {
			if path != binary || !reflect.DeepEqual(args, []string{binary}) {
				t.Fatalf("exec = %q %q", path, args)
			}
			return errors.New("exec intercepted")
		}
		var stderr bytes.Buffer
		code := hookentry.CodexLaunch(nil, &stderr, runtime)
		if code != 1 || !strings.Contains(stderr.String(), "exec intercepted") {
			t.Fatalf("code=%d stderr=%s", code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	runtime := pfmconfig.Runtime{
		Config: pfmconfig.Config{Codex: pfmconfig.CodexPrefs{Binary: filepath.Join(t.TempDir(), "missing")}},
		Paths:  paths.Values{Home: t.TempDir()},
	}
	code := hookentry.CodexLaunch(nil, &stderr, runtime)
	if code != 1 || !strings.Contains(stderr.String(), "resolve Codex binary:") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestCodexLaunchUsesSelectedPrimaryAccount(t *testing.T) {
	jailTest(t)
	binary, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	values := paths.Values{Home: home, StateDB: filepath.Join(home, ".local", "state", "pfm", "pfm.db")}
	machine := pfmconfig.Defaults(home, nil)
	machine.Accounts = []pfmconfig.Account{
		{ID: 1, ConfigDir: filepath.Join(home, ".cc", "1")},
		{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")},
	}
	machine.Codex = pfmconfig.CodexPrefs{Binary: binary, Yolo: true}
	machine.CodexAccounts = []pfmconfig.CodexAccount{
		{ID: 1, Home: filepath.Join(home, "codex-1")},
		{ID: 2, Home: filepath.Join(home, "codex-2"), Prefs: &pfmconfig.CodexPrefs{Binary: binary, Yolo: false}},
	}
	if err := fleet.SetPrimaryAccount(values, machine, 2); err != nil {
		t.Fatal(err)
	}
	previous := hookentry.LaunchExec
	t.Cleanup(func() { hookentry.LaunchExec = previous })
	hookentry.LaunchExec = func(path string, args, env []string) error {
		if path != binary || !reflect.DeepEqual(args, []string{binary}) {
			t.Fatalf("selected primary argv=%q", args)
		}
		return errors.New("exec intercepted")
	}
	var stderr bytes.Buffer
	code := hookentry.CodexLaunch(nil, &stderr, pfmconfig.Runtime{Config: machine, Paths: values})
	if code != 1 || !strings.Contains(stderr.String(), "exec intercepted") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}
