package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

func newWorkbenchRunFixture(t *testing.T, jail *runJail) (string, string) {
	t.Helper()
	root := filepath.Join(jail.root, "work", "acme")
	dir := filepath.Join(root, "docs", "scribe")
	duo := filepath.Join(root, "docs", "duo")
	for path, body := range map[string]string{
		filepath.Join(root, ".professor", "baseline.json"): "{}",
		paths.WorkbenchManifest(dir):                       `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(dir, ".professor", "scribe.md"):      "You are scribe.",
		paths.WorkbenchManifest(duo):                       `{"prompt":"duo.md","engines":["codex","claude"]}`,
		filepath.Join(duo, ".professor", "duo.md"):         "You are duo.",
		filepath.Join(duo, "CLAUDE.md"):                    "Duo.\n",
		filepath.Join(root, ".claude", "agents", "r.md"):   "ROLE R",
		filepath.Join(root, ".codex", "agents", "r.toml"):  `developer_instructions = "ROLE R"`,
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, duo
}

// These tests share process environment and spawn timings, so they stay serial.
func TestChatNewWorkbench(t *testing.T) {
	for _, name := range []string{"auto-name", "next number", "explicit effort", "explicit harness", "omitted enabled", "omitted disabled", "omitted skips opencode", "no account", "disabled explicit", "invalid manifest", "outside", "roster unreadable", "Codex", "mirror failure", "role"} {
		t.Run(name, func(t *testing.T) {
			jail := newRunJail(t)
			defer jail.killSockets(t)
			dir, duo := newWorkbenchRunFixture(t, jail)
			engine, socket, wantName := "cc", "cc-workbench", "_SCRIBE:1"
			extra := []string{}
			wantEffort, wantChannel := "xhigh", filepath.Join(dir, ".professor", "scribe.md")
			wantCode, wantError := 0, ""
			switch name {
			case "next number":
				t.Setenv(spawn.TestFreshSocketEnv, "cc-first-workbench")
				var stdout, stderr bytes.Buffer
				code := run(
					[]string{"chat", "new", "--engine", "cc", "--name", "_SCRIBE:1", "--cwd", dir},
					&stdout,
					&stderr,
				)
				if code != 0 {
					t.Fatalf("seed launch = %d, %s", code, stderr.String())
				}
				if err := os.WriteFile(
					jail.transcript,
					[]byte(
						`{"type":"user","cwd":"/work/acme/docs/scribe","message":{"content":"ready"}}`+"\n"+`{"type":"custom-title","customTitle":"_SCRIBE:1"}`+"\n",
					),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				wantName = "_SCRIBE:2"
			case "explicit effort":
				extra = []string{"--effort", "high"}
				wantEffort = "high"
			case "explicit harness":
				wantChannel = filepath.Join(jail.root, "work", "alt.md")
				if err := os.WriteFile(wantChannel, []byte("ALT PROMPT"), 0o600); err != nil {
					t.Fatal(err)
				}
				extra = []string{"--harness-prompt", wantChannel}
			case "omitted enabled":
				engine = ""
				dir = duo
				wantName = "DUO:1"
				wantEffort = ""
				wantChannel = filepath.Join(duo, ".professor", "duo.md")
				t.Setenv("CLAUDE_CODE_SESSION_ID", "caller")
			case "omitted disabled":
				engine = ""
				t.Setenv("CODEX_THREAD_ID", "caller")
			case "omitted skips opencode":
				// OpenCode has an account but no headless planner: chat new
				// takes the next enabled engine instead of failing on it.
				engine = ""
				dir = filepath.Join(jail.root, "work", "acme", "docs", "notes")
				for path, body := range map[string]string{
					paths.WorkbenchManifest(dir):                            `{"prompt":"p.md","title":"Notes","engines":["opencode","claude"]}`,
					filepath.Join(dir, ".professor", "p.md"):                "You are notes.",
					filepath.Join(jail.root, "opencode-store", "auth.json"): "{}",
				} {
					if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv(pfmengine.MustLookup(pfmengine.OpenCode).RootEnv, filepath.Join(jail.root, "opencode-store"))
				t.Setenv("CODEX_THREAD_ID", "caller")
				wantName, wantEffort = "NOTES:1", ""
				wantChannel = filepath.Join(dir, ".professor", "p.md")
			case "no account":
				engine = ""
				dir = filepath.Join(jail.root, "work", "acme", "docs", "cx-only")
				if err := atomicfile.Write(
					paths.WorkbenchManifest(dir),
					[]byte(`{"prompt":"p.md","engines":["codex"]}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := atomicfile.Write(
					filepath.Join(dir, ".professor", "p.md"),
					[]byte("You are cx-only."),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(jail.root, "codex", "auth.json")); err != nil {
					t.Fatal(err)
				}
				wantCode, wantError = 2, "pfm chat new: workbench "+dir+" enables codex, and this machine has no account for any of them\n"
			case "disabled explicit":
				engine = "cx"
				extra = []string{"--name", "x"}
				wantCode, wantError = 2, "pfm chat new: workbench "+dir+` does not enable codex: add "codex" to "engines" in `+paths.WorkbenchManifest(
					dir,
				)+"\n"
			case "invalid manifest":
				if err := os.WriteFile(paths.WorkbenchManifest(dir), []byte(`{"prompt":""}`), 0o600); err != nil {
					t.Fatal(err)
				}
				extra = []string{"--name", "x"}
				wantCode, wantError = 2, "pfm chat new: "+paths.WorkbenchManifest(dir)+`: "prompt" is required`+"\n"
			case "outside":
				dir = filepath.Join(jail.root, "work")
				wantCode, wantError = 2, "usage: pfm chat new [--name NAME]"
			case "roster unreadable":
				blocker := filepath.Join(jail.root, "blocker")
				if err := os.WriteFile(blocker, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
				runtime, err := pfmconfig.LoadRuntime("")
				if err != nil {
					t.Fatal(err)
				}
				_, rosterErr := pfmchat.Rows(context.Background(), io.Discard, &runtime)
				if rosterErr == nil {
					t.Fatal("fixture roster did not fail")
				}
				wantCode, wantError = 1, "pfm chat new: "+rosterErr.Error()+"\n"
			case "Codex", "mirror failure":
				engine, socket, dir, wantName = "cx", "cx-workbench", duo, "DUO:1"
				t.Setenv("CX_STUB_ARGV", filepath.Join(jail.root, "cx-argv"))
				if name == "mirror failure" {
					if err := os.WriteFile(filepath.Join(duo, ".mcp.json"), []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
					wantCode, wantError = 2, "pfm chat new: build the codex mirror of workbench "+duo+":"
				}
			case "role":
				extra = []string{"--agent-role", "r"}
			}
			t.Setenv(spawn.TestFreshSocketEnv, socket)
			args := []string{"chat", "new", "--cwd", dir}
			if engine != "" {
				args = append(args, "--engine", engine)
			}
			args = append(args, extra...)
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != wantCode {
				t.Fatalf(
					"chat new exit %d, want %d; stdout=%q stderr=%q",
					code,
					wantCode,
					stdout.String(),
					stderr.String(),
				)
			}
			if wantCode != 0 {
				if !strings.Contains(stderr.String(), wantError) {
					t.Fatalf("error = %q; want %q", stderr.String(), wantError)
				}
				if name != "outside" && name != "mirror failure" && stderr.String() != wantError {
					t.Fatalf("error = %q; want exactly %q", stderr.String(), wantError)
				}
				if stdout.Len() != 0 {
					t.Fatalf("refusal launch output = %s", stdout.String())
				}
				return
			}
			if !strings.Contains(stdout.String(), "\t"+wantName+"\t") {
				t.Fatalf("launch name = %q; want %q", stdout.String(), wantName)
			}
			argvFile := "cc-argv"
			if engine == "cx" {
				argvFile = "cx-argv"
			}
			argv := jail.await(t, argvFile, "--")
			if engine == "cx" {
				if !strings.Contains(argv, "developer_instructions=") || !strings.Contains(argv, "You are duo.") {
					t.Fatalf("Codex argv = %q", argv)
				}
				if _, err := os.Stat(filepath.Join(duo, "AGENTS.md")); err != nil {
					t.Fatalf("mirror: %v", err)
				}
			} else {
				if name == "role" {
					var err error
					wantChannel, err = agentrole.SeatPromptPath(filepath.Join(jail.root, "sid"), socket, "")
					if err != nil {
						t.Fatal(err)
					}
					raw, err := os.ReadFile(wantChannel)
					want := "<!-- pfm agent-role: r -->\nYou are scribe.\n\n---\n\nROLE R"
					if err != nil || string(raw) != want {
						t.Fatalf("role prompt = %q, %v; want %q", raw, err, want)
					}
				}
				if !strings.Contains(argv, "--system-prompt-file "+wantChannel) {
					t.Fatalf("Claude argv = %q; want prompt %s", argv, wantChannel)
				}
				if wantEffort != "" && !strings.Contains(argv, "--effort "+wantEffort) {
					t.Fatalf("Claude effort argv = %q; want %s", argv, wantEffort)
				}
			}
			recorded, found, err := agentrole.ReadHarnessPromptRecord(filepath.Join(jail.root, "sid"), socket, "")
			if err != nil || found != (name == "explicit harness") || (found && recorded != wantChannel) {
				t.Fatalf("harness record = %q, %v, %v", recorded, found, err)
			}
		})
	}
}
