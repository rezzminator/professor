package deps_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// probeHomeJail is a target HOME with an empty Claude store, a SID dir outside
// it (production's sits in /tmp), and a logged-out fake claude whose runs are
// recorded outside both. It returns the registry's Claude entry for the fake.
func probeHomeJail(t *testing.T, doctor string) (home, sid, record string, entry deps.Entry) {
	t.Helper()
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sid = filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	scratch := t.TempDir()
	record = filepath.Join(scratch, "config-dirs.log")
	binary := filepath.Join(scratch, "claude")
	if err := testjail.WriteLoggedOutClaude(binary, record, doctor); err != nil {
		t.Fatal(err)
	}
	entries := deps.Registry(deps.Options{Home: home, ClaudeBinary: binary})
	for index := range entries {
		if entries[index].Engine == pfmengine.Claude {
			return home, sid, record, entries[index]
		}
	}
	t.Fatal("the registry has no Claude entry")
	return "", "", "", deps.Entry{}
}

// TestClaudeProbeRunsInAThrowawayHomeOnEveryPath: the version and self-doctor
// probes judge the binary, never an account. A logged-out claude run with the
// ambient env recreated ~/.claude/backups and $HOME/.claude.json, and the host
// check then blocked the machine. Each probe runs in a throwaway home, an
// inherited config dir included, removed on success, failure and timeout,
// and the verdict is the one the binary earned.
func TestClaudeProbeRunsInAThrowawayHomeOnEveryPath(t *testing.T) {
	for _, testcase := range []struct {
		name, doctor, inherit string
		want                  func(deps.Result) bool
	}{
		{name: "healthy", want: func(result deps.Result) bool {
			return result.State == deps.StateOK && result.Version == "2.1.238" && result.SelfDoctor == "ok"
		}},
		{name: "inherited config dir", inherit: ".claude", want: func(result deps.Result) bool {
			return result.State == deps.StateOK && result.SelfDoctor == "ok"
		}},
		{name: "failing self-doctor", doctor: "printf 'boom\\n'; exit 3", want: func(result deps.Result) bool {
			return result.State == deps.StateBroken && strings.Contains(result.Error, "boom")
		}},
		{name: "self-doctor timeout", doctor: "exec /bin/sleep 30", want: func(result deps.Result) bool {
			return result.State == deps.StateOK && strings.HasPrefix(result.SelfDoctor, "timeout")
		}},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			home, sid, record, entry := probeHomeJail(t, testcase.doctor)
			if testcase.inherit != "" {
				t.Setenv(pfmengine.MustLookup(pfmengine.Claude).HomeEnv, filepath.Join(home, testcase.inherit))
			}
			result := deps.Probe(context.Background(), []deps.Entry{entry}, deps.ProbeOptions{
				GOOS: runtime.GOOS, Timeout: 10 * time.Second, SelfDoctorTimeout: time.Second,
			})[0]
			if !testcase.want(result) {
				t.Errorf("verdict = %#v", result)
			}
			if result.ProbeHomeErr != "" {
				t.Errorf("probe home residue: %s", result.ProbeHomeErr)
			}
			if runs := testjail.AssertClaudeRanInThrowawayHomes(t, home, sid, record); runs != 3 {
				t.Errorf("the fake recorded %d runs, want 3 (--version, doctor --help, doctor)", runs)
			}
		})
	}
}

// TestClaudeProbeThatCannotCreateItsHomeDoesNotRun: with no throwaway home the
// probe could not look. It runs nothing and says so, never ok and never absent.
func TestClaudeProbeThatCannotCreateItsHomeDoesNotRun(t *testing.T) {
	_, _, record, entry := probeHomeJail(t, "")
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvSIDDir, filepath.Join(blocker, "sid"))
	result := deps.Probe(context.Background(), []deps.Entry{entry}, deps.ProbeOptions{GOOS: runtime.GOOS})[0]
	if result.State != deps.StateBroken || !strings.Contains(result.Error, "could not look") {
		t.Fatalf("result = %#v, want broken with could not look", result)
	}
	if _, err := os.Lstat(record); !os.IsNotExist(err) {
		t.Fatalf("the fake ran although its home could not be created (lstat err=%v)", err)
	}
}

// codexProbeHomeJail is probeHomeJail for Codex: a target HOME with no
// ~/.codex, a SID dir outside it, and a fake codex that writes its launcher
// dir and log database the way the real one does on every run.
func codexProbeHomeJail(t *testing.T, doctor string) (home, sid, record string, entry deps.Entry) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	sid = filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	scratch := t.TempDir()
	record = filepath.Join(scratch, "codex-homes.log")
	binary := filepath.Join(scratch, "codex")
	if err := testjail.WriteLoggedOutCodex(binary, record, doctor); err != nil {
		t.Fatal(err)
	}
	entries := deps.Registry(deps.Options{Home: home, CodexBinary: binary})
	for index := range entries {
		if entries[index].Engine == pfmengine.Codex {
			return home, sid, record, entries[index]
		}
	}
	t.Fatal("the registry has no Codex entry")
	return "", "", "", deps.Entry{}
}

// TestCodexProbeRunsInAThrowawayHome: every codex run writes its launcher dir
// and log database into its home, and a full self-doctor in the real home
// outran the probe bound (171 s against 30 s), was killed and left its dir
// behind. Both probes run in a throwaway home instead. That home has no login,
// so the self-doctor's auth row fails there by construction: it is the
// account's row, which pfm's own codex-login check judges, never the binary's.
// Any other failing row is still a broken binary.
func TestCodexProbeRunsInAThrowawayHome(t *testing.T) {
	for _, testcase := range []struct {
		name, doctor, inherit string
		want                  func(deps.Result) bool
	}{
		{name: "logged-out throwaway home", want: func(result deps.Result) bool {
			return result.State == deps.StateOK && result.Version == "0.159.0" &&
				strings.HasPrefix(result.SelfDoctor, "ok") && strings.Contains(result.SelfDoctor, "auth")
		}},
		{name: "inherited codex home", inherit: ".codex", want: func(result deps.Result) bool {
			return result.State == deps.StateOK && strings.HasPrefix(result.SelfDoctor, "ok")
		}},
		{
			name: "binary failure beside auth",
			doctor: "printf '  [XX] install      inconsistent package\\n  [XX] auth         no Codex credentials\\n'; " +
				"exit 1",
			want: func(result deps.Result) bool {
				return result.State == deps.StateBroken && strings.Contains(result.Error, "install")
			},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			home, sid, record, entry := codexProbeHomeJail(t, testcase.doctor)
			if testcase.inherit != "" {
				t.Setenv(pfmengine.MustLookup(pfmengine.Codex).HomeEnv, filepath.Join(home, testcase.inherit))
			}
			result := deps.Probe(context.Background(), []deps.Entry{entry}, deps.ProbeOptions{
				GOOS: runtime.GOOS, Timeout: 10 * time.Second, SelfDoctorTimeout: 5 * time.Second,
			})[0]
			if !testcase.want(result) {
				t.Errorf("verdict = %#v", result)
			}
			if result.ProbeHomeErr != "" {
				t.Errorf("probe home residue: %s", result.ProbeHomeErr)
			}
			if runs := testjail.AssertCodexRanInThrowawayHomes(t, home, sid, record); runs != 3 {
				t.Errorf("the fake recorded %d runs, want 3 (--version, doctor --help, doctor --summary)", runs)
			}
		})
	}
}
