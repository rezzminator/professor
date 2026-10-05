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

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/workbench"
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

func TestCodexLaunchWorkbench(t *testing.T) {
	for _, scenario := range []string{"new", "resume", "fork", "explicit", "long model", "equals model", "explicit effort", "disabled", "disabled resume", "invalid", "mirror failure", "outside", "login", "logout", "mcp", "mcp-server", "app-server", "completion", "sandbox", "debug", "apply", "cloud", "features", "help", "--version", "-V", "--help", "-h"} {
		t.Run(scenario, func(t *testing.T) {
			root := jailTest(t)
			scribe, duo := newWorkbenchRunFixture(t, &runJail{root: root})
			if err := os.WriteFile(
				paths.WorkbenchManifest(duo),
				[]byte(`{"prompt":"duo.md","engines":["codex","claude"],"model":"gpt-x","effort":"XHigh"}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			dir := duo
			binary, err := exec.LookPath("sh")
			if err != nil {
				t.Fatal(err)
			}
			runtime := pfmconfig.Runtime{
				Config: pfmconfig.Config{Codex: pfmconfig.CodexPrefs{Binary: binary, Yolo: true}},
				Paths:  paths.Values{Home: filepath.Join(root, "home")},
			}
			args := []string{}
			personaArgs := append([]string{"--model", "gpt-x"}, action.CodexEffortArg("xhigh")...)
			personaArgs = append(personaArgs, action.CodexDeveloperInstructionsArg("You are duo.")...)
			wantError := ""
			mirror := true
			switch scenario {
			case "resume":
				args = []string{"resume", "--last"}
			case "fork":
				args = []string{"fork", "--last"}
			case "explicit":
				args = []string{"-m", "o9", "-c", `developer_instructions="x"`}
				personaArgs = action.CodexEffortArg("xhigh")
			case "long model", "equals model":
				args = []string{"--model", "o9"}
				if scenario == "equals model" {
					args = []string{"--model=o9"}
				}
				personaArgs = append(
					action.CodexEffortArg("xhigh"),
					action.CodexDeveloperInstructionsArg("You are duo.")...)
			case "explicit effort":
				args = []string{"-c", `model_reasoning_effort="low"`}
				personaArgs = append(
					[]string{"--model", "gpt-x"},
					action.CodexDeveloperInstructionsArg("You are duo.")...)
			case "disabled":
				dir = scribe
				wantError = "launch Codex: workbench " + scribe + ` does not enable codex: add "codex" to "engines" in ` + paths.WorkbenchManifest(
					scribe,
				) + "\n"
			case "disabled resume":
				dir = scribe
				args = []string{"resume", "--last"}
				personaArgs = nil
				mirror = false
			case "invalid":
				if err := os.WriteFile(paths.WorkbenchManifest(duo), []byte(`{"prompt":""}`), 0o600); err != nil {
					t.Fatal(err)
				}
				wantError = "launch Codex: " + paths.WorkbenchManifest(duo) + `: "prompt" is required` + "\n"
			case "mirror failure":
				if err := os.Remove(filepath.Join(duo, "CLAUDE.md")); err != nil {
					t.Fatal(err)
				}
				persona, err := workbench.ForLaunch(duo, pfmengine.Codex, workbench.New)
				if err != nil {
					t.Fatal(err)
				}
				err = workbench.EnsureMirror(persona.Bench, pfmengine.Codex, runtime.Paths.Home)
				if err == nil {
					t.Fatal("mirror fixture must fail")
				}
				wantError = "launch Codex: " + err.Error() + "\n"
			case "outside":
				dir = root
				personaArgs = nil
				mirror = false
			case "login",
				"logout",
				"mcp",
				"mcp-server",
				"app-server",
				"completion",
				"sandbox",
				"debug",
				"apply",
				"cloud",
				"features",
				"help",
				"--version",
				"-V",
				"--help",
				"-h":
				dir = scribe
				args = []string{scenario}
				personaArgs = nil
				mirror = false
			}
			t.Chdir(dir)
			previous := hookentry.LaunchExec
			t.Cleanup(func() { hookentry.LaunchExec = previous })
			called := false
			hookentry.LaunchExec = func(path string, argv, _ []string) error {
				called = true
				if wantError != "" {
					t.Fatal("refusal reached exec")
				}
				want := append([]string{binary, "--dangerously-bypass-approvals-and-sandbox"}, personaArgs...)
				want = append(want, args...)
				if path != binary || !reflect.DeepEqual(argv, want) {
					t.Fatalf("persona argv = %q, want %q", argv, want)
				}
				if mirror {
					if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
						t.Fatalf("mirror before exec: %v", err)
					}
				}
				return nil
			}
			var stderr bytes.Buffer
			code := hookentry.CodexLaunch(args, &stderr, runtime)
			if wantError != "" {
				if code != 1 || called || stderr.String() != wantError {
					t.Fatalf("refusal = %d, called %v, %q; want %q", code, called, stderr.String(), wantError)
				}
				return
			}
			if code != 0 || !called || stderr.Len() != 0 {
				t.Fatalf("launch = %d, called %v, stderr %q", code, called, stderr.String())
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
