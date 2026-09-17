package doctor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

func TestClassifySpawnSeparatesInjectedOldAndBypassed(t *testing.T) {
	const layer = int64(1_700_000_000)
	for _, testCase := range []struct {
		name        string
		observation spawnObservation
		want        spawnVerdict
		wantReason  string
	}{
		{
			name: "professor prompt file in argv",
			observation: spawnObservation{
				Argv: []string{
					"claude", "--resume", "abc", "--system-prompt-file", "/p.md",
					"--settings", `{"outputStyle":"default"}`,
				},
				Environ:     map[string]string{},
				StartedUnix: layer + 60,
			},
			want:       spawnInjected,
			wantReason: "--system-prompt-file",
		},
		{
			name: "lean arm in the environment",
			observation: spawnObservation{
				Argv:        []string{"claude", "--settings", `{"outputStyle":"default"}`},
				Environ:     map[string]string{"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT": "1"},
				StartedUnix: layer + 60,
			},
			want:       spawnInjected,
			wantReason: "lean prompt armed",
		},
		{
			// The staged prompt without the settings flag double-applies a
			// persona — Claude Code's own output style still runs on top of
			// it — so this is its own violation, distinct from injecting
			// nothing at all.
			name: "professor prompt file without the settings flag",
			observation: spawnObservation{
				Argv:        []string{"claude", "--resume", "abc", "--system-prompt-file", "/p.md"},
				Environ:     map[string]string{},
				StartedUnix: layer + 60,
			},
			want:       spawnViolation,
			wantReason: "missing --settings",
		},
		{
			// Same missing-settings argv, but this seat was born well BEFORE
			// the current spawn door went live: it carries the argv of the pfm
			// that launched it and predates the --settings flag exactly as it
			// predates the prompt itself. A reload fixes it, not a bug hunt —
			// this is the exact defect the fix closes (it used to return
			// VIOLATION unconditionally here regardless of age).
			name: "professor prompt file without the settings flag, seat older than the layer",
			observation: spawnObservation{
				Argv:        []string{"claude", "--resume", "abc", "--system-prompt-file", "/p.md"},
				Environ:     map[string]string{},
				StartedUnix: layer - 3600,
			},
			want:       spawnPredatesLayer,
			wantReason: "reload to carry it",
		},
		{
			// Missing settings AND no usable age signal (StartedUnix unknown):
			// an unreadable age must never read as "old and therefore
			// forgiven" — it must still be a violation.
			name: "professor prompt file without the settings flag, unknown start time",
			observation: spawnObservation{
				Argv:    []string{"claude", "--resume", "abc", "--system-prompt-file", "/p.md"},
				Environ: map[string]string{},
			},
			want:       spawnViolation,
			wantReason: "missing --settings",
		},
		{
			// A correctly-flagged seat (prompt file AND --settings) is never
			// downgraded by age: even one born well before the layer stamp
			// still classifies as INJECTED, never predates-layer or a
			// violation.
			name: "professor prompt file with the settings flag, seat older than the layer",
			observation: spawnObservation{
				Argv: []string{
					"claude", "--resume", "abc", "--system-prompt-file", "/p.md",
					"--settings", `{"outputStyle":"default"}`,
				},
				Environ:     map[string]string{},
				StartedUnix: layer - 3600,
			},
			want:       spawnInjected,
			wantReason: "--system-prompt-file",
		},
		{
			// Missing settings, a real (old) start time, but the layer stamp
			// itself is unavailable on this host (0): an unusable stamp must
			// also never read as "old and therefore forgiven".
			name: "professor prompt file without the settings flag, no layer stamp available",
			observation: spawnObservation{
				Argv:        []string{"claude", "--resume", "abc", "--system-prompt-file", "/p.md"},
				Environ:     map[string]string{},
				StartedUnix: layer - 3600,
			},
			want:       spawnViolation,
			wantReason: "missing --settings",
		},
		{
			name: "flagless chat older than the layer",
			observation: spawnObservation{
				Argv:        []string{"claude"},
				Environ:     map[string]string{},
				StartedUnix: layer - 3600,
			},
			want:       spawnPredatesLayer,
			wantReason: "before this host's current spawn door was installed",
		},
		{
			name: "flagless resume with no usable age signal",
			observation: spawnObservation{
				Argv:    []string{"claude", "--resume", "abc"},
				Environ: map[string]string{},
			},
			want:       spawnPredatesLayer,
			wantReason: "reborn before the door",
		},
		{
			name: "fresh flagless launch",
			observation: spawnObservation{
				Argv:        []string{"claude", "--name", "seat"},
				Environ:     map[string]string{},
				StartedUnix: layer + 3600,
			},
			want:       spawnViolation,
			wantReason: "bypassed the door",
		},
		{
			// The lean arm lives only in the environment, so an unreadable
			// environment cannot clear a seat. It must not read as clean.
			name: "fresh flagless launch with an unreadable environment",
			observation: spawnObservation{
				Argv:        []string{"claude", "--name", "seat"},
				EnvironErr:  errors.New("permission denied"),
				StartedUnix: layer + 3600,
			},
			want:       spawnViolation,
			wantReason: "verdict unproven",
		},
		{
			// An absent stamp must never turn an ordinary fresh chat into a
			// "predates the layer" excuse.
			name: "no layer stamp leaves the argv verdict standing",
			observation: spawnObservation{
				Argv:        []string{"claude", "--name", "seat"},
				Environ:     map[string]string{},
				StartedUnix: layer - 3600,
			},
			want:       spawnViolation,
			wantReason: "bypassed the door",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stamp := layer
			if testCase.name == "no layer stamp leaves the argv verdict standing" ||
				testCase.name == "professor prompt file without the settings flag, no layer stamp available" {
				stamp = 0
			}
			verdict, reason := classifySpawn(testCase.observation, stamp)
			if verdict != testCase.want {
				t.Fatalf("classifySpawn = %s (%s), want %s", verdict, reason, testCase.want)
			}
			if !strings.Contains(reason, testCase.wantReason) {
				t.Fatalf("reason %q does not name the deciding signal %q", reason, testCase.wantReason)
			}
		})
	}
}

// Production configures no prompt material, so an audit that classified every
// seat would report a fleet-wide violation. The check must say it has nothing
// to assert instead — and must never print the clean-audit wording.
func TestSpawnAuditIsInertUnderProductionPolicy(t *testing.T) {
	var stdout bytes.Buffer
	machine := config.Config{Claude: config.ClaudePrefs{SystemPrompt: config.SystemPromptProduction}}
	if warnings := printSpawnAuditDoctor(context.Background(), &stdout, paths.Values{}, machine, 1); warnings != 0 {
		t.Fatalf("production policy warned %d times: %q", warnings, stdout.String())
	}
	line := stdout.String()
	if !strings.Contains(line, "nothing to audit") {
		t.Fatalf("production line = %q", line)
	}
	if strings.Contains(line, "VIOLATION") || strings.Contains(line, "no live Claude chats found") {
		t.Fatalf("production policy borrowed an audited verdict: %q", line)
	}
}

// A tmux directory that cannot be read is a FAILED audit, never an empty one.
func TestSpawnAuditFailureNeverReadsAsNoChats(t *testing.T) {
	var stdout bytes.Buffer
	machine := config.Config{Claude: config.ClaudePrefs{SystemPrompt: config.SystemPromptProfessor}}
	resolved := paths.Values{Home: t.TempDir(), TmuxDir: filepath.Join(t.TempDir(), "not-a-directory")}
	if err := os.WriteFile(resolved.TmuxDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := printSpawnAuditDoctor(context.Background(), &stdout, resolved, machine, 1)
	line := stdout.String()
	if warnings == 0 {
		t.Fatalf("unreadable socket directory reported no warning: %q", line)
	}
	if !strings.Contains(line, "CHECK FAILED to run") {
		t.Fatalf("failed audit line = %q", line)
	}
	if strings.Contains(line, "no live Claude chats found") {
		t.Fatalf("a failed audit rendered as an empty one: %q", line)
	}
}

// fakeProcFS serves a hand-built process table to the resolver tests.
type fakeProcFS struct {
	cmdlines map[int][]string
	parents  map[int]int
}

func (fake fakeProcFS) PIDs() ([]int, error) {
	pids := make([]int, 0, len(fake.cmdlines))
	for pid := range fake.cmdlines {
		pids = append(pids, pid)
	}
	return pids, nil
}

func (fake fakeProcFS) Cmdline(pid int) ([]string, error) {
	argv, ok := fake.cmdlines[pid]
	if !ok {
		return nil, errors.New("no such process")
	}
	return argv, nil
}

func (fake fakeProcFS) Environ(int) (map[string]string, error) {
	return map[string]string{}, nil
}

func (fake fakeProcFS) FDLinks(int) ([]gather.FDLink, error) { return nil, nil }

func (fake fakeProcFS) Stat(pid int) (gather.ProcStat, error) {
	if _, ok := fake.cmdlines[pid]; !ok {
		return gather.ProcStat{}, errors.New("no such process")
	}
	return gather.ProcStat{ParentPID: fake.parents[pid]}, nil
}

// The claude launcher execs the version-named file
// (~/.local/share/claude/versions/2.1.250), so a live seat's argv[0] basename
// is a version string, never "claude". The resolver must identify it through
// the fleet's one matcher or the audit reports a live fleet as empty.
func TestResolveClaudeProcessMatchesVersionNamedBinary(t *testing.T) {
	proc := fakeProcFS{
		cmdlines: map[int][]string{
			100: {"zsh"},
			101: {"/srv/seat/.local/share/claude/versions/2.1.250", "--system-prompt-file", "/p.md"},
		},
		parents: map[int]int{101: 100},
	}
	pid, argv, found, err := resolveClaudeProcess(proc, 100, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("version-named claude binary was not recognized — a live seat renders as an empty pane")
	}
	if pid != 101 {
		t.Fatalf("resolved pid = %d, want 101", pid)
	}
	if len(argv) == 0 || argv[0] != "/srv/seat/.local/share/claude/versions/2.1.250" {
		t.Fatalf("resolved argv = %q", argv)
	}
}

// TestSpawnDoorStampIsTheLaterOfThePromptAndTheInstalledBinary pins the
// spawn-audit age regression. The --settings flag shipped after the prompt,
// and an install that leaves the prompt's bytes alone never moves its mtime,
// so a stamp read from the prompt alone put every chat an older pfm launched
// "after the layer": `violations=5` on a healthy host, doctor exit 1, and
// pfm update rolled itself back.
func TestSpawnDoorStampIsTheLaterOfThePromptAndTheInstalledBinary(t *testing.T) {
	home := t.TempDir()
	prompt := action.ProfessorPromptPath(home)
	binary := filepath.Join(t.TempDir(), "pfm")
	for _, path := range []string{prompt, binary} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	promptAt, binaryAt := time.Unix(1_700_000_000, 0), time.Unix(1_700_500_000, 0)
	if err := os.Chtimes(prompt, promptAt, promptAt); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(binary, binaryAt, binaryAt); err != nil {
		t.Fatal(err)
	}
	previous := spawnDoorExecutable
	t.Cleanup(func() { spawnDoorExecutable = previous })
	spawnDoorExecutable = func() (string, error) { return binary, nil }

	stamp, signal := spawnDoorStamp(home)
	if stamp != binaryAt.Unix() || !strings.Contains(signal, prompt) || !strings.Contains(signal, binary) {
		t.Fatalf("spawnDoorStamp = %d %q; want the binary's %d with both inputs named", stamp, signal, binaryAt.Unix())
	}
	// Launched by an older pfm between the prompt and the binary: prompt
	// carried, --settings not. History, not a broken door.
	seat := spawnObservation{
		Argv:        []string{"claude", "--system-prompt-file", prompt},
		Environ:     map[string]string{},
		StartedUnix: promptAt.Unix() + 3600,
	}
	if verdict, reason := classifySpawn(seat, stamp); verdict != spawnPredatesLayer {
		t.Fatalf("older seat = %s (%s), want %s", verdict, reason, spawnPredatesLayer)
	}
	// Born after the binary landed and still flagless: the door is broken.
	seat.StartedUnix = binaryAt.Unix() + 60
	if verdict, reason := classifySpawn(seat, stamp); verdict != spawnViolation {
		t.Fatalf("fresh seat = %s (%s), want %s", verdict, reason, spawnViolation)
	}
	// The binary unreadable: the prompt still stands and the signal says why.
	spawnDoorExecutable = func() (string, error) { return "", errors.New("no executable path") }
	if stamp, signal := spawnDoorStamp(
		home,
	); stamp != promptAt.Unix() ||
		!strings.Contains(signal, "no executable path") {
		t.Fatalf(
			"unreadable binary: spawnDoorStamp = %d %q; want the prompt's %d and the reason",
			stamp,
			signal,
			promptAt.Unix(),
		)
	}
}
