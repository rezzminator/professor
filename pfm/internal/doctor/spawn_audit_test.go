package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestSpawnAuditRegistryPayload(t *testing.T) {
	home := t.TempDir()
	prompt := filepath.Join(home, "p.md")
	if err := os.WriteFile(prompt, []byte("prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{config.SystemPromptProfessor, config.SystemPromptLean, config.SystemPromptProduction} {
		t.Run(policy, func(t *testing.T) {
			machine := config.Config{Claude: config.ClaudePrefs{SystemPrompt: policy}}
			launch, err := claudelaunch.Render(
				claudelaunch.Request{Home: home, PromptFile: prompt, Resume: "abc"},
				machine,
			)
			if err != nil {
				t.Fatal(err)
			}
			observation := spawnObservation{Argv: append([]string{launch.Binary}, launch.Argv...), StartedUnix: 200}
			parsed, err := claudelaunch.Parse(observation.Argv)
			if err != nil {
				t.Fatal(err)
			}
			verdict, reason := classifySpawn(parsed, observation, machine.EffectiveClaude(1), home, 100)
			if verdict != spawnInjected {
				t.Fatalf("%s: %s", verdict, reason)
			}
		})
	}
}

func TestSpawnAuditHookDrift(t *testing.T) {
	home := t.TempDir()
	machine := config.Config{Claude: config.ClaudePrefs{SystemPrompt: config.SystemPromptProduction}}
	launch, err := claudelaunch.Render(claudelaunch.Request{Home: home}, machine)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := claudelaunch.Parse(append([]string{launch.Binary}, launch.Argv...))
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]claudelaunch.Hook) []claudelaunch.Hook{
		"missing": func(hooks []claudelaunch.Hook) []claudelaunch.Hook { return hooks[1:] },
		"extra":   func(hooks []claudelaunch.Hook) []claudelaunch.Hook { return append(hooks, hooks[0]) },
		"moved":   func(hooks []claudelaunch.Hook) []claudelaunch.Hook { hooks[0].Matcher = "other"; return hooks },
		"async flipped": func(hooks []claudelaunch.Hook) []claudelaunch.Hook {
			hooks[0].Async = !hooks[0].Async
			return hooks
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := parsed
			changed.Hooks = change(append([]claudelaunch.Hook(nil), parsed.Hooks...))
			verdict, reason := classifySpawn(
				changed,
				spawnObservation{StartedUnix: 50},
				machine.EffectiveClaude(1),
				home,
				100,
			)
			if verdict != spawnPredatesLayer || !strings.Contains(reason, "hook set differs from the registry") {
				t.Fatalf("%s: %s", verdict, reason)
			}
		})
	}
}

func TestSpawnAuditBypassAndUndecodable(t *testing.T) {
	home := t.TempDir()
	prefs := config.ClaudePrefs{SystemPrompt: config.SystemPromptProduction}
	observation := spawnObservation{Socket: "cc-bypassed", PID: 45, Argv: []string{"claude"}, StartedUnix: 200}
	parsed := mustParseSpawn(t, observation.Argv)
	if verdict, reason := classifySpawn(
		parsed,
		observation,
		prefs,
		home,
		100,
	); verdict != spawnViolation ||
		!strings.Contains(reason, "bypassed") {
		t.Fatalf("%s: %s", verdict, reason)
	}
	undecodable := spawnObservation{Socket: "cc-broken", PID: 46, Argv: []string{"claude", "--settings", "{"}}
	if warning := decodeSpawn(&undecodable); !strings.Contains(warning, "cc-broken pid=46: argv undecodable:") {
		t.Fatalf("warning = %q", warning)
	} else if warnings := spawnAuditUnreadWarnings(&bytes.Buffer{}, []string{warning}); warnings == 0 {
		t.Fatal("undecodable argv did not count as unread")
	}
}

func TestSpawnAuditMatchesAccountByProcessConfigDir(t *testing.T) {
	machine := config.Config{
		Claude: config.ClaudePrefs{SystemPrompt: config.SystemPromptProfessor},
		Accounts: []config.Account{
			{ID: 1, ConfigDir: "/accounts/1", Claude: &config.ClaudePrefs{SystemPrompt: config.SystemPromptProfessor}},
			{ID: 2, ConfigDir: "/accounts/2", Claude: &config.ClaudePrefs{SystemPrompt: config.SystemPromptProduction}},
		},
	}
	for _, test := range []struct {
		dir      string
		want     int
		fallback bool
	}{
		{"/accounts/2", 2, false}, {"/unknown", 1, true},
	} {
		got, reason := spawnAccount(
			machine,
			1,
			spawnObservation{Environ: map[string]string{"CLAUDE_CONFIG_DIR": test.dir}},
		)
		if got != test.want || (reason != "") != test.fallback {
			t.Fatalf("dir %s: account=%d reason=%q", test.dir, got, reason)
		}
	}
	got, reason := spawnAccount(machine, 1, spawnObservation{EnvironErr: errors.New("denied")})
	if got != 1 || !strings.Contains(reason, "unreadable") {
		t.Fatalf("account=%d reason=%q", got, reason)
	}
}

func doctorProfessorPromptPath(t *testing.T, home string) string {
	t.Helper()
	if err := paths.WriteSourceRepoMarker(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	path, err := action.ProfessorPromptPath(home)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClassifyRolePromptSeparatesSoundMismatchAndUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "role-prompt-cc-reviewer.md")
	for _, testCase := range []struct {
		name       string
		read       rolePromptRead
		want       rolePromptOutcome
		wantReason string
	}{
		{
			name:       "sound role seat",
			read:       rolePromptRead{role: "reviewer", prompt: "fleet and role", found: true},
			want:       rolePromptOK,
			wantReason: "reviewer",
		},
		{
			name: "marker missing",
			read: rolePromptRead{
				found: true,
				err:   errors.New("agent role: seat prompt has no valid role marker"),
			},
			want:       rolePromptMismatch,
			wantReason: "no valid role marker",
		},
		{
			name: "empty role",
			read: rolePromptRead{
				found: true,
				err:   errors.New("agent role: seat prompt has an empty role marker"),
			},
			want:       rolePromptMismatch,
			wantReason: "empty role marker",
		},
		{
			name:       "empty channel",
			read:       rolePromptRead{role: "reviewer", found: true},
			want:       rolePromptMismatch,
			wantReason: "empty prompt channel",
		},
		{
			name: "file unreadable",
			read: rolePromptRead{
				found: true,
				err: &os.PathError{
					Op:   "open",
					Path: path,
					Err:  errors.New("permission denied"),
				},
			},
			want:       rolePromptCheckFailed,
			wantReason: "permission denied",
		},
		{
			name:       "file deleted",
			read:       rolePromptRead{},
			want:       rolePromptCheckFailed,
			wantReason: "does not exist",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outcome, reason := classifyRolePrompt(path, testCase.read)
			if outcome != testCase.want {
				t.Fatalf("classifyRolePrompt = %s (%s), want %s", outcome, reason, testCase.want)
			}
			if !strings.Contains(reason, path) || !strings.Contains(reason, testCase.wantReason) {
				t.Fatalf("reason %q does not name path %q and deciding signal %q", reason, path, testCase.wantReason)
			}
		})
	}
}

func TestPrintSpawnRoleAuditReportsRowsCountsAndWarnings(t *testing.T) {
	sidDir := t.TempDir()
	soundPath := mustDoctorSeatPromptPath(t, sidDir, "cc-sound", "%2")
	if err := agentrole.WriteSeatPrompt(
		sidDir, "cc-sound", "%2", "<!-- pfm agent-role: reviewer -->\nrole prompt",
	); err != nil {
		t.Fatal(err)
	}
	mismatchPath := mustDoctorSeatPromptPath(t, sidDir, "cc-mismatch", "")
	if err := os.WriteFile(mismatchPath, []byte("not a role marker\nrole prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadablePath := mustDoctorSeatPromptPath(t, sidDir, "cc-deleted", "")
	stagedPath := doctorProfessorPromptPath(t, t.TempDir())

	proc := fakeProcFS{
		cmdlines: map[int][]string{
			101: {"claude", "--system-prompt-file", soundPath},
			102: {"claude", "--system-prompt-file", mismatchPath},
			103: {"claude", "--system-prompt-file", unreadablePath},
			104: {"claude", "--system-prompt-file", stagedPath},
		},
		parents: map[int]int{},
	}
	observations := make([]spawnObservation, 0, len(proc.cmdlines))
	for pid := 101; pid <= 104; pid++ {
		resolvedPID, argv, found, err := resolveClaudeProcess(proc, pid, "claude")
		if err != nil || !found {
			t.Fatalf("resolve pid %d = pid %d found %v err %v", pid, resolvedPID, found, err)
		}
		observations = append(observations, spawnObservation{
			Socket: fmt.Sprintf("cc-role-%d", pid),
			PID:    resolvedPID,
			Argv:   argv,
		})
	}

	var stdout bytes.Buffer
	if warnings := printSpawnRoleAudit(&stdout, observations); warnings != 1 {
		t.Fatalf("role audit warnings = %d, want 1: %q", warnings, stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"ROLE-OK cc-role-101 pid=101",
		"role=reviewer",
		soundPath,
		"ROLE-MISMATCH cc-role-102 pid=102",
		mismatchPath,
		"no valid role marker",
		"ROLE-CHECK-FAILED cc-role-103 pid=103",
		unreadablePath,
		"does not exist",
		"role-seats=3 ok=1 mismatched=1 unreadable=1",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("role audit output missing %q: %q", want, output)
		}
	}
	if strings.Contains(output, stagedPath) || strings.Contains(output, "cc-role-104") {
		t.Fatalf("ordinary composed prompt produced a role row: %q", output)
	}
}

func TestPrintSpawnRoleAuditSoundSeatDoesNotWarn(t *testing.T) {
	sidDir := t.TempDir()
	path := mustDoctorSeatPromptPath(t, sidDir, "cc-sound", "")
	if err := agentrole.WriteSeatPrompt(
		sidDir, "cc-sound", "", "<!-- pfm agent-role: reviewer -->\nrole prompt",
	); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	warnings := printSpawnRoleAudit(&stdout, []spawnObservation{{
		Socket: "cc-sound",
		PID:    101,
		Argv:   []string{"claude", "--system-prompt-file", path},
	}})
	if warnings != 0 {
		t.Fatalf("sound role seat changed the warning count by %d: %q", warnings, stdout.String())
	}
	if !strings.Contains(stdout.String(), "role-seats=1 ok=1 mismatched=0 unreadable=0") {
		t.Fatalf("sound role seat counts = %q", stdout.String())
	}
}

func mustDoctorSeatPromptPath(t *testing.T, sidDir, socket, pane string) string {
	t.Helper()
	path, err := agentrole.SeatPromptPath(sidDir, socket, pane)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrintSpawnRoleAuditSaysNothingWithoutRoleSeats(t *testing.T) {
	var stdout bytes.Buffer
	observations := []spawnObservation{{
		Socket: "cc-ordinary",
		PID:    41,
		Argv:   []string{"claude", "--system-prompt-file", doctorProfessorPromptPath(t, t.TempDir())},
	}}
	if warnings := printSpawnRoleAudit(&stdout, observations); warnings != 0 {
		t.Fatalf("ordinary seats warned %d times", warnings)
	}
	if stdout.Len() != 0 {
		t.Fatalf("ordinary seats produced role audit output: %q", stdout.String())
	}
}

// Production configures no prompt material, so an audit that classified every
// seat would report a fleet-wide violation. The check must say it has nothing
// to assert instead — and must never print the clean-audit wording.
func TestSpawnAuditRunsUnderProductionPolicy(t *testing.T) {
	var stdout bytes.Buffer
	machine := config.Config{Claude: config.ClaudePrefs{SystemPrompt: config.SystemPromptProduction}}
	resolved := paths.Values{Home: t.TempDir(), TmuxDir: filepath.Join(t.TempDir(), "not-a-directory")}
	if err := os.WriteFile(resolved.TmuxDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if warnings := printSpawnAuditDoctor(
		context.Background(),
		&stdout,
		resolved,
		machine,
		1,
	); warnings == 0 ||
		!strings.Contains(stdout.String(), "CHECK FAILED to run") {
		t.Fatalf("production audit did not probe: %q", stdout.String())
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
	prompt := doctorProfessorPromptPath(t, home)
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
	if verdict, reason := classifySpawn(
		mustParseSpawn(t, seat.Argv),
		seat,
		config.ClaudePrefs{SystemPrompt: config.SystemPromptProfessor},
		home,
		stamp,
	); verdict != spawnPredatesLayer {
		t.Fatalf("older seat = %s (%s), want %s", verdict, reason, spawnPredatesLayer)
	}
	// Born after the binary landed and still flagless: the door is broken.
	seat.StartedUnix = binaryAt.Unix() + 60
	if verdict, reason := classifySpawn(
		mustParseSpawn(t, seat.Argv),
		seat,
		config.ClaudePrefs{SystemPrompt: config.SystemPromptProfessor},
		home,
		stamp,
	); verdict != spawnViolation {
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

func TestSpawnDoorStampUsesBinaryWhenComposedPromptUnreadable(t *testing.T) {
	home := t.TempDir()
	prompt := doctorProfessorPromptPath(t, home)
	binary := filepath.Join(t.TempDir(), "pfm")
	if err := os.WriteFile(binary, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(binary, at, at); err != nil {
		t.Fatal(err)
	}
	previous := spawnDoorExecutable
	t.Cleanup(func() { spawnDoorExecutable = previous })
	spawnDoorExecutable = func() (string, error) { return binary, nil }
	stamp, signal := spawnDoorStamp(home)
	if stamp != at.Unix() || !strings.Contains(signal, prompt) || !strings.Contains(signal, "prompt layer") {
		t.Fatalf("stamp=%d signal=%q; want binary stamp and unreadable prompt", stamp, signal)
	}
}

func mustParseSpawn(t *testing.T, argv []string) claudelaunch.Parsed {
	t.Helper()
	parsed, err := claudelaunch.Parse(argv)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
