package costsink

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func fakeArm(port int, err error, calls *int) func(ArmOptions) (Armed, error) {
	return func(ArmOptions) (Armed, error) {
		*calls++
		return Armed{Port: port}, err
	}
}

func logText(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".local", "state", "pfm", "log", "cost-sink.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func count(environ []string, prefix string) int {
	n := 0
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			n++
		}
	}
	return n
}

func TestPrepareArmsACleanLaunchAndHandsClaudeThePortAndTheMarker(t *testing.T) {
	home, calls := t.TempDir(), 0
	base := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=" + t.TempDir(), "HOME=" + home}
	environ, state := Prepare(
		LaunchOptions{
			Environ: base,
			Home:    home,
			Self:    "/x/pfm",
			PID:     4242,
			Enabled: true,
			Arm:     fakeArm(43181, nil, &calls),
		},
	)
	if state != claudelaunch.CostLedgerOn || calls != 1 {
		t.Fatalf("state = %q after %d arms, want on after 1", state, calls)
	}
	for _, want := range claudelaunch.CostEnv(43181, 4242) {
		if !slices.Contains(environ, want) {
			t.Errorf("environ lacks %s", want)
		}
	}
	for _, kept := range base {
		if !slices.Contains(environ, kept) {
			t.Errorf("environ dropped %s", kept)
		}
	}
}

func TestPrepareKeepsTheUsersOwnTelemetryAndSaysWhy(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(configDir, "settings.json"),
		[]byte(`{"env":{"OTEL_METRICS_EXPORTER":"prometheus"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for name, base := range map[string][]string{
		"process env":   {"CLAUDE_CONFIG_DIR=" + t.TempDir(), "CLAUDE_CODE_ENABLE_TELEMETRY=1"},
		"settings.json": {"CLAUDE_CONFIG_DIR=" + configDir},
	} {
		home, calls := t.TempDir(), 0
		environ, state := Prepare(
			LaunchOptions{
				Environ: base,
				Home:    home,
				Self:    "/x/pfm",
				PID:     1,
				Enabled: true,
				Arm:     fakeArm(1, nil, &calls),
			},
		)
		if state != claudelaunch.CostLedgerPreempted || calls != 0 {
			t.Errorf("%s: state = %q after %d arms, want preempted without arming", name, state, calls)
		}
		if !slices.Contains(environ, "PFM_COST_LEDGER=preempted") || count(environ, "OTEL_") != count(base, "OTEL_") {
			t.Errorf("%s: environ = %q, want the marker and the user's variables untouched", name, environ)
		}
		if got := logText(t, home); !strings.Contains(got, "pre-empted") {
			t.Errorf("%s: sink log = %q, want the reason", name, got)
		}
	}
}

// A chat started from inside a pfm chat inherits the parent's exports and marker: they are pfm's,
// so it is not pre-empted, and it arms its own sink rather than reusing one that dies with the parent.
func TestPrepareArmsANestedChatFreshInsteadOfPreemptingIt(t *testing.T) {
	home, calls := t.TempDir(), 0
	base := append([]string{"PATH=/bin"}, claudelaunch.CostEnv(40111, 99)...)
	environ, state := Prepare(
		LaunchOptions{
			Environ: base,
			Home:    home,
			Self:    "/x/pfm",
			PID:     7,
			Enabled: true,
			Arm:     fakeArm(50222, nil, &calls),
		},
	)
	if state != claudelaunch.CostLedgerOn || calls != 1 {
		t.Fatalf("state = %q after %d arms, want on after 1", state, calls)
	}
	if n := count(
		environ,
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
	); n != 1 ||
		!slices.Contains(environ, "OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:50222") {
		t.Fatalf("environ = %q, want exactly one endpoint carrying the fresh port", environ)
	}
	if n := count(environ, "PFM_COST_LEDGER="); n != 1 {
		t.Fatalf("%d markers in %q", n, environ)
	}
}

// A chat launched through the cost launcher whose `claude` resolves to pfm's own launcher shim
// reaches Prepare a second time in the same process: the sink armed for this pid serves it.
func TestPrepareReusesTheSinkAlreadyArmedForThisProcess(t *testing.T) {
	home, calls := t.TempDir(), 0
	base := append([]string{"PATH=/bin"}, claudelaunch.CostEnv(40111, 7)...)
	environ, state := Prepare(LaunchOptions{
		Environ: base, Home: home, Self: "/x/pfm", PID: 7, Enabled: true, Arm: fakeArm(50222, nil, &calls),
	})
	if state != claudelaunch.CostLedgerOn || calls != 0 {
		t.Fatalf("state = %q after %d arms, want on with no second sink", state, calls)
	}
	if !slices.Equal(environ, base) {
		t.Fatalf("environ = %q, want the armed launch's own %q", environ, base)
	}
}

// Off or pre-empted, a chat started from inside a pfm chat must not keep the parent's exports:
// they point at the parent's sink, which would ledger a launch marked as having none.
func TestPrepareDropsAParentChatsExportsWhenItArmsNothing(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(configDir, "settings.json"),
		[]byte(`{"env":{"OTEL_METRICS_EXPORTER":"prometheus"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	parent := append([]string{"PATH=/bin", "OTEL_SERVICE_NAME=mine"}, claudelaunch.CostEnv(40111, 99)...)
	for name, tc := range map[string]struct {
		environ []string
		enabled bool
		want    string
	}{
		"off":       {parent, false, claudelaunch.CostLedgerOff},
		"preempted": {append(slices.Clone(parent), "CLAUDE_CONFIG_DIR="+configDir), true, claudelaunch.CostLedgerPreempted},
	} {
		calls := 0
		environ, state := Prepare(LaunchOptions{
			Environ: tc.environ, Home: t.TempDir(), Self: "/x/pfm", PID: 7, Enabled: tc.enabled,
			Arm: fakeArm(50222, nil, &calls),
		})
		if state != tc.want || calls != 0 {
			t.Errorf("%s: state = %q after %d arms", name, state, calls)
		}
		for _, entry := range environ {
			if strings.HasPrefix(entry, "OTEL_EXPORTER_OTLP_ENDPOINT=") ||
				strings.HasPrefix(entry, "CLAUDE_CODE_ENABLE_TELEMETRY=") ||
				strings.HasPrefix(entry, "PFM_COST_LEDGER_PID=") {
				t.Errorf("%s: the parent's %s survived", name, entry)
			}
		}
		if !slices.Contains(environ, "OTEL_SERVICE_NAME=mine") || count(environ, "PFM_COST_LEDGER=") != 1 {
			t.Errorf("%s: environ = %q, want the user's own variable and one marker", name, environ)
		}
	}
}

func TestPrepareMarksAFailedSinkAndLogsTheCause(t *testing.T) {
	home, calls := t.TempDir(), 0
	environ, state := Prepare(LaunchOptions{
		Environ: []string{"PATH=/bin"}, Home: home, Self: "/x/pfm", PID: 9, Enabled: true,
		Arm: fakeArm(0, errors.New("listen tcp4 127.0.0.1:0: too many open files"), &calls),
	})
	if state != claudelaunch.CostLedgerFailed || !slices.Contains(environ, "PFM_COST_LEDGER=failed") ||
		count(environ, "OTEL_") != 0 {
		t.Fatalf("state = %q environ = %q, want failed, the marker and no exporter variables", state, environ)
	}
	if got := logText(t, home); !strings.Contains(got, "too many open files") {
		t.Fatalf("sink log = %q, want the cause", got)
	}
}

func TestPrepareDisabledLaunchesMarkedOffWithoutArming(t *testing.T) {
	home, calls := t.TempDir(), 0
	environ, state := Prepare(
		LaunchOptions{Environ: []string{"PATH=/bin"}, Home: home, Enabled: false, Arm: fakeArm(1, nil, &calls)},
	)
	if state != claudelaunch.CostLedgerOff || calls != 0 || !slices.Contains(environ, "PFM_COST_LEDGER=off") {
		t.Fatalf("state = %q, arms = %d, environ = %q", state, calls, environ)
	}
}

func TestPrepareLogsAnUnreadableSettingsFileAndStillArms(t *testing.T) {
	configDir, home, calls := t.TempDir(), t.TempDir(), 0
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, state := Prepare(LaunchOptions{
		Environ: []string{
			"CLAUDE_CONFIG_DIR=" + configDir,
		},
		Home:    home,
		PID:     3,
		Enabled: true,
		Arm:     fakeArm(4321, nil, &calls),
	})
	if state != claudelaunch.CostLedgerOn || calls != 1 {
		t.Fatalf("state = %q after %d arms", state, calls)
	}
	if got := logText(t, home); !strings.Contains(got, "settings.json") {
		t.Fatalf("sink log = %q, want the unreadable settings named", got)
	}
}
