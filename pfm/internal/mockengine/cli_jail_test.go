//go:build jail

package mockengine

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestEngineDoctorAnswersWithoutStartingASession(t *testing.T) {
	for _, row := range []struct {
		engine string
		args   []string
	}{
		{"claude", []string{"doctor"}},
		{"claude", []string{"doctor", "--help"}},
		{"codex", []string{"doctor", "--help"}},
		{"codex", []string{"doctor", "--summary", "--ascii", "--no-color"}},
		{"claude", []string{"doctor", "--fixture-argument"}},
	} {
		t.Run(row.engine+"/"+strings.Join(row.args, " "), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			code, stdout, stderr := runOnce(fix, row.engine, row.args, "")
			want := "mock-engine: " + row.engine + " doctor — fixture engine, nothing checked\n"
			if code != 0 || stdout != want || stderr != "" {
				t.Fatalf("doctor exit=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
			}
			assertCLIOpensNoSession(t, fix)
		})
	}
}

func TestClaudePluginAnswersWithoutStartingASession(t *testing.T) {
	for _, row := range []struct {
		args []string
		want string
	}{
		{
			[]string{"plugin", "marketplace", "add", "rezzminator/x"},
			"mock-engine: claude plugin marketplace add rezzminator/x — fixture, nothing fetched\n",
		},
		{
			[]string{"plugin", "install", "x@x", "-y"},
			"mock-engine: claude plugin install x@x — fixture, recorded installed and enabled in %s\n",
		},
		{
			[]string{"plugin", "install", "x@x"},
			"mock-engine: claude plugin install x@x — fixture, recorded installed and enabled in %s\n",
		},
	} {
		t.Run(strings.Join(row.args, " "), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			for range 2 {
				var out, errs bytes.Buffer
				code := Main(
					context.Background(),
					"claude",
					row.args,
					strings.NewReader(""),
					&out,
					&errs,
					fix.env(map[string]string{"CLAUDE_CONFIG_DIR": fix.configDir}),
				)
				stdout, stderr := out.String(), errs.String()
				want := row.want
				if row.args[1] == "install" {
					want = fmt.Sprintf(want, fix.configDir)
				}
				if code != 0 || stdout != want || stderr != "" {
					t.Fatalf("plugin exit=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
				}
				assertCLIOpensNoSession(t, fix)
			}
		})
	}
}

func TestClaudeUnscriptedPluginsAreRefusedWithoutStartingASession(t *testing.T) {
	for _, args := range [][]string{
		{"plugin", "list"},
		{"plugin"},
		{"plugin", "marketplace", "add"},
		{"plugin", "marketplace", "add", "source", "extra"},
		{"plugin", "install"},
		{"plugin", "install", "x@x", "--unknown"},
		{"plugin", "install", "x@x", "-y", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.installHooks()
			code, stdout, stderr := runOnce(fix, "claude", args, "")
			want := "mock-engine: claude " + strings.Join(args, " ") + " is not scripted\n"
			if code != ExitUnpinned || stdout != "" || stderr != want {
				t.Fatalf("plugin refusal exit=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
			}
			assertCLIOpensNoSession(t, fix)
		})
	}
}

func TestEngineDoctorPassesPfmsDependencyProbe(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	var engines []deps.Entry
	entries := deps.Registry(deps.Options{
		Home:           fix.home,
		ClaudeBinary:   filepath.Join(fix.bin, "claude"),
		CodexBinary:    filepath.Join(fix.bin, "codex"),
		OpenCodeBinary: filepath.Join(fix.bin, "opencode"),
	})
	for index := range entries {
		if entries[index].Engine != "" {
			engines = append(engines, entries[index])
		}
	}
	results := deps.Probe(context.Background(), engines, deps.ProbeOptions{Runner: deps.RealRunner{}})
	// OpenCode's registry row carries no self-doctor command, so its probe
	// leaves SelfDoctor empty.
	wantSelfDoctor := map[pfmengine.ID]string{pfmengine.Claude: "ok", pfmengine.Codex: "ok", pfmengine.OpenCode: ""}
	if len(results) != len(wantSelfDoctor) {
		t.Fatalf("engine probes=%+v, want the three registered engines %v", results, wantSelfDoctor)
	}
	for index := range results {
		result := &results[index]
		want, registered := wantSelfDoctor[result.Entry.Engine]
		if !registered || result.State != deps.StateOK || result.SelfDoctor != want {
			t.Fatalf("%s probe=%+v, want state ok and self-doctor %q", result.Entry.Engine, result, want)
		}
	}
	assertCLIOpensNoSession(t, fix)
}
