package deps

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProbeDistinguishesOKMinimumGarbageMissingAndTimeout(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "tmux-ok", "printf 'tmux 3.4\\n'")
	writeProbeStub(t, directory, "tmux-old", "printf 'tmux 1.7\\n'")
	writeProbeStub(t, directory, "garbage", "printf 'not-a-version\\n'")
	writeProbeStub(t, directory, "timeout", "exec /bin/sleep 30")
	writeProbeStub(t, directory, "failed", "printf 'permission denied by fixture\\n'; exit 7")
	t.Setenv("PATH", directory)

	entries := []Entry{
		{
			Name:        "ok",
			Command:     "tmux-ok",
			Required:    true,
			VersionArgs: []string{"-V"},
			MinVersion:  "1.8",
			Parse:       prefixedVersion("tmux"),
		},
		{
			Name:        "old",
			Command:     "tmux-old",
			Required:    true,
			VersionArgs: []string{"-V"},
			MinVersion:  "1.8",
			Parse:       prefixedVersion("tmux"),
		},
		{Name: "garbage", Command: "garbage", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion},
		{Name: "missing", Command: "absent", Required: true},
		{Name: "timeout", Command: "timeout", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion},
		{Name: "failed", Command: "failed", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion},
	}
	results := Probe(context.Background(), entries, ProbeOptions{
		GOOS: "linux", Timeout: ProbeTimeout,
	})
	want := []State{StateOK, StateBroken, StateBroken, StateMissing, StateTimeout, StateBroken}
	for index := range want {
		if results[index].State != want[index] {
			t.Errorf(
				"%s state=%s error=%q raw=%q, want %s",
				entries[index].Name,
				results[index].State,
				results[index].Error,
				results[index].Raw,
				want[index],
			)
		}
	}
	if results[0].Version != "3.4" || results[1].Version != "1.7" {
		t.Fatalf("parsed versions ok=%q old=%q", results[0].Version, results[1].Version)
	}
	if !strings.HasPrefix(results[4].Error, "timeout (") || !strings.Contains(results[4].Error, ProbeTimeout.String()) {
		t.Fatalf(
			"timeout error=%q, want it to name the enforced bound %q rather than a bare sentinel",
			results[4].Error,
			ProbeTimeout,
		)
	}
}

// Regression: probeOne's version branch mapped every failure — including
// context.DeadlineExceeded — straight to StateBroken, the same bucket as an
// actually-broken binary. A version probe that legitimately outran its bound
// must be named as a timeout distinct from a real failure, and a genuinely
// broken tool in the same run must still read as broken — proving the fix
// distinguishes the two rather than relabelling every failure as a timeout.
func TestVersionProbeTimeoutIsNotConflatedWithBroken(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "hung", "exec /bin/sleep 30")
	writeProbeStub(t, directory, "broken", "printf 'permission denied by fixture\\n'; exit 7")
	t.Setenv("PATH", directory)

	entries := []Entry{
		{Name: "hung", Command: "hung", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion},
		{Name: "broken", Command: "broken", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion},
	}
	results := Probe(context.Background(), entries, ProbeOptions{GOOS: "linux", Timeout: ProbeTimeout})

	if results[0].State != StateTimeout {
		t.Fatalf(
			"hung state=%s error=%q, want StateTimeout — an outran bound must not read as broken",
			results[0].State,
			results[0].Error,
		)
	}
	if !strings.HasPrefix(results[0].Error, "timeout (") || !strings.Contains(results[0].Error, ProbeTimeout.String()) {
		t.Fatalf("hung error=%q, want the enforced bound named", results[0].Error)
	}
	if results[1].State != StateBroken {
		t.Fatalf(
			"broken state=%s error=%q, want StateBroken — a genuinely broken tool must not be relabelled as a timeout",
			results[1].State,
			results[1].Error,
		)
	}
}

func TestVersionProbePreservesParentCancellationAndDeadline(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "hung", "exec /bin/sleep 30")
	t.Setenv("PATH", directory)
	entry := Entry{
		Name:        "hung",
		Command:     "hung",
		Required:    true,
		VersionArgs: []string{"--version"},
		Parse:       firstVersion,
	}

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		resultCh := make(chan Result, 1)
		go func() {
			resultCh <- Probe(ctx, []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: 2 * time.Second})[0]
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		result := <-resultCh
		if result.State != StateCancelled {
			t.Fatalf("state=%s error=%q, want StateCancelled", result.State, result.Error)
		}
		if result.Error != "cancelled by parent context" {
			t.Fatalf("error=%q, want parent cancellation provenance", result.Error)
		}
	})

	t.Run("parent deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		result := Probe(ctx, []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: 2 * time.Second})[0]
		if result.State != StateCancelled {
			t.Fatalf("state=%s error=%q, want StateCancelled", result.State, result.Error)
		}
		if result.Error != "parent context deadline exceeded before probe timeout" {
			t.Fatalf("error=%q, want parent deadline provenance", result.Error)
		}
	})
}

func TestSelfDoctorParentStopsRemainCancelledInHelpAndSummary(t *testing.T) {
	tests := []struct {
		name       string
		phase      string
		selfDoctor []string
	}{
		{name: "help cancellation", phase: "help", selfDoctor: []string{"doctor", "--summary"}},
		{name: "help deadline", phase: "help", selfDoctor: []string{"doctor", "--summary"}},
		{name: "summary cancellation", phase: "summary", selfDoctor: []string{"doctor", "--summary"}},
		{name: "summary deadline", phase: "summary", selfDoctor: []string{"doctor", "--summary"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			marker := filepath.Join(directory, "phase-ready")
			t.Setenv("PROBE_PHASE", test.phase)
			t.Setenv("PROBE_PHASE_MARKER", marker)
			writeProbeStub(t, directory, "engine", `
if [ "$1" = "--version" ]; then printf 'engine-cli 1.2.3\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then
  if [ "$PROBE_PHASE" = "help" ]; then : > "$PROBE_PHASE_MARKER"; exec /bin/sleep 30; fi
  printf 'usage: engine doctor\n'; exit 0
fi
if [ "$1" = "doctor" ] && [ "$2" = "--summary" ]; then
  if [ "$PROBE_PHASE" = "summary" ]; then : > "$PROBE_PHASE_MARKER"; exec /bin/sleep 30; fi
  printf 'healthy\n'; exit 0
fi
exit 2`)
			t.Setenv("PATH", directory)
			entry := Entry{
				Name: "engine", Command: "engine",
				VersionArgs: []string{"--version"}, Parse: firstVersion,
				SelfDoctorArgs: test.selfDoctor,
			}

			var (
				parent   context.Context
				cancel   func()
				deadline *deferredDeadlineContext
			)
			if strings.HasSuffix(test.name, "deadline") {
				deadline = newDeferredDeadlineContext()
				parent = deadline
				cancel = deadline.expire
			} else {
				var cancelContext context.CancelFunc
				parent, cancelContext = context.WithCancel(context.Background())
				cancel = cancelContext
			}
			defer cancel()

			probeTimeout := selfDoctorProbeTimeout(t)
			resultCh := make(chan Result, 1)
			go func() {
				resultCh <- Probe(parent, []Entry{entry}, ProbeOptions{
					GOOS: "linux", Timeout: probeTimeout, SelfDoctorTimeout: probeTimeout,
				})[0]
			}()
			waitForProbePhaseMarker(t, marker)
			cancel()
			result := <-resultCh
			if result.Version != "1.2.3" {
				t.Fatalf(
					"version=%q state=%s error=%q, want version probe to pass before %s stop",
					result.Version,
					result.State,
					result.Error,
					test.phase,
				)
			}
			if result.State != StateCancelled || result.SelfDoctor != "cancelled" {
				t.Fatalf("result=%#v, want StateCancelled/SelfDoctor=cancelled", result)
			}
			wantError := "cancelled by parent context"
			if strings.HasSuffix(test.name, "deadline") {
				wantError = "parent context deadline exceeded before probe timeout"
			}
			if result.Error != wantError {
				t.Fatalf("error=%q, want %q", result.Error, wantError)
			}
		})
	}
}

// deferredDeadlineContext lets this test release a parent deadline only after
// the version probe and the selected self-doctor phase have reached the marker.
// A short real timeout would race fixture startup and could fail before the
// test reaches the phase whose provenance it is meant to pin.
type deferredDeadlineContext struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func newDeferredDeadlineContext() *deferredDeadlineContext {
	return &deferredDeadlineContext{done: make(chan struct{})}
}

func (ctx *deferredDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *deferredDeadlineContext) Done() <-chan struct{} { return ctx.done }

func (ctx *deferredDeadlineContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.err
}

func (ctx *deferredDeadlineContext) Value(any) any { return nil }

func (ctx *deferredDeadlineContext) expire() {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.err != nil {
		return
	}
	ctx.err = context.DeadlineExceeded
	close(ctx.done)
}

// selfDoctorProbeTimeout is the per-call bound this test hands to Probe()
// for its version probe and each self-doctor stage (--help, then --summary).
// It used to be a flat 2s literal, and that is exactly what broke: --help
// gates --summary in probeSelfDoctor, so a --help probe that outran only its
// own 2s bound on a loaded box (the trivial fixture never even scheduled in
// time) made probeSelfDoctor return "broken" WITHOUT ever attempting
// --summary — so the summary phase marker this test waits for was never
// written, no matter how long the wait after it was made. The bound is
// derived from the test's own deadline rather than another guessed literal,
// capped to a quarter of what remains so the deliberately-hung phase this
// test drives still leaves room to be cancelled and observed afterward.
func selfDoctorProbeTimeout(t *testing.T) time.Duration {
	t.Helper()
	budget := DefaultSelfDoctorTimeout
	if deadline, ok := t.Deadline(); ok {
		if remaining := time.Until(deadline) / 4; remaining < budget {
			budget = remaining
		}
	}
	return budget
}

// probePhaseMarkerFallbackBudget is the wait used only when the test binary
// carries no deadline at all (e.g. `-timeout 0`), where there is no ceiling
// to respect and the wait needs a floor rather than a guess.
const probePhaseMarkerFallbackBudget = 5 * time.Minute

func waitForProbePhaseMarker(t *testing.T, marker string) {
	t.Helper()
	// The budget is derived from the test binary's own deadline, not from a
	// fixed constant: a hardcoded ceiling (60s, tried here before) is exactly
	// the "generous-looking" number that machine load blows through — the
	// marker is written by a /bin/sh stub that must first be forked and
	// scheduled, and under a loaded box running the whole suite in parallel
	// that scheduling alone was observed to exceed 60s. Spending everything
	// up to the deadline costs nothing: a genuine hang never writes the
	// marker AT ALL, so no budget lets this pass by accident — it only
	// decides how long the failure takes to name itself, and failing here
	// beats being killed by a whole-binary timeout panic that names nothing
	// about which wait never finished.
	budget := probePhaseMarkerFallbackBudget
	if testDeadline, ok := t.Deadline(); ok {
		if remaining := time.Until(testDeadline) - time.Second; remaining < budget {
			budget = remaining
		}
	}
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat probe phase marker %q: %v", marker, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("probe phase marker %q was not created within %s", marker, budget)
}

func TestProbePlatformAndHarvestFiltering(t *testing.T) {
	entries := []Entry{
		{Name: "ps", Command: "ps", Required: true, Platforms: []string{"darwin"}},
		{Name: "uv", Command: "/managed/uv", Required: true, Harvest: true},
	}
	linux := Probe(context.Background(), entries, ProbeOptions{GOOS: "linux", SkipHarvest: true})
	if linux[0].State != StateSkipped || linux[0].Error != "not this platform" || linux[1].State != StateSkipped ||
		linux[1].Error != "--skip-harvest" {
		t.Fatalf("linux filters=%#v", linux)
	}
	darwin := Probe(context.Background(), entries[:1], ProbeOptions{
		GOOS:     "darwin",
		LookPath: func(string) (string, error) { return "/usr/bin/ps", nil },
	})
	if darwin[0].State != StateOK || darwin[0].Path != "/usr/bin/ps" {
		t.Fatalf("darwin ps=%#v", darwin[0])
	}
	provision := Probe(context.Background(), entries[1:], ProbeOptions{GOOS: "linux", Provisioning: true})
	if provision[0].State != StateSkipped || provision[0].Error != "provisioned by install" {
		t.Fatalf("install harvest filter=%#v", provision[0])
	}
}

func TestDarwinLsofParsesApplesMultilineBanner(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "lsof", "printf 'lsof version information:\n    revision: 4.91\n' >&2")
	t.Setenv("PATH", directory)

	var lsof Entry
	for _, entry := range Registry(Options{Home: t.TempDir(), GOOS: "darwin", GOARCH: "arm64"}) {
		if entry.Name == "lsof" {
			lsof = entry
			break
		}
	}
	if lsof.Name == "" {
		t.Fatal("Darwin registry has no lsof entry")
	}
	result := Probe(context.Background(), []Entry{lsof}, ProbeOptions{GOOS: "darwin"})[0]
	if result.State != StateOK || result.Path == "" || result.Version != "4.91" {
		t.Fatalf("Darwin lsof result=%#v, want parsed Apple revision 4.91", result)
	}
}

func TestConfiguredEngineDependenciesRemainOptionalForHostInstall(t *testing.T) {
	entries := Registry(Options{Home: t.TempDir()})
	for _, entry := range entries {
		if entry.Name != "claude" && entry.Name != "codex" {
			continue
		}
		if entry.Required {
			t.Errorf("configured engine %s is required, want optional capability", entry.Name)
		}
	}
}

func TestSelfDoctorFailureNamesTheSpecificFailingCheck(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "codex", `
if [ "$1" = "--version" ]; then printf 'codex-cli 0.149.1\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--summary" ]; then
  printf 'Codex Doctor v0.149.1 · linux-x86_64\n[FAIL] auth — active model provider auth env var is missing\n18 ok · 1 fail\n'
  exit 1
fi
exit 2`)
	t.Setenv("PATH", directory)
	entry := Entry{
		Name: "codex", Command: "codex", VersionArgs: []string{"--version"}, Parse: firstVersion,
		SelfDoctorArgs: []string{"doctor", "--summary", "--ascii", "--no-color"},
	}
	result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux"})[0]
	if result.State != StateBroken || !strings.Contains(result.Error, "auth") ||
		strings.Contains(result.Error, "Codex Doctor v0.149.1") {
		t.Fatalf("self-doctor result=%#v, want the auth failure rather than the banner", result)
	}
}

func TestRegistryDoesNotAdvertiseRetiredGCloud(t *testing.T) {
	for _, entry := range Registry(Options{Home: t.TempDir(), GOOS: "linux", GOARCH: "amd64"}) {
		if entry.Name == "gcloud" || entry.Command == "gcloud" {
			t.Fatalf("retired gcloud dependency remains registered: %#v", entry)
		}
	}
}

func TestResolveRejectsRegisteredOffPlatformCommand(t *testing.T) {
	var command string
	switch runtime.GOOS {
	case "linux":
		command = "launchctl"
	case "darwin":
		command = "setsid"
	default:
		t.Skip("registry platform contract is Linux/Darwin only")
	}
	directory := t.TempDir()
	writeProbeStub(t, directory, command, "exit 0")
	t.Setenv("PATH", directory)
	_, err := Resolve(command)
	if err == nil || !strings.Contains(err.Error(), "not supported on "+runtime.GOOS) {
		t.Fatalf("Resolve(%q) error=%v", command, err)
	}
}

func TestProbeDelegatesSupportedEngineSelfDoctor(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "claude", `
if [ "$1" = "--version" ]; then printf '2.1.238 (Claude Code)\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then printf 'usage: claude doctor\n'; exit 0; fi
if [ "$1" = "doctor" ]; then printf 'healthy\n'; exit 0; fi
exit 2`)
	t.Setenv("PATH", directory)
	entry := Entry{
		Name:           "claude",
		Command:        "claude",
		Required:       true,
		VersionArgs:    []string{"--version"},
		Parse:          firstVersion,
		SelfDoctorArgs: []string{"doctor"},
	}
	result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux"})[0]
	if result.State != StateOK || result.Version != "2.1.238" || result.SelfDoctor != "ok" {
		t.Fatalf("self doctor result=%#v", result)
	}
}

func TestProbeWritesSuccessfulRawOutputOnlyWhenVerbose(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "tool", "printf 'tool 4.2\\n'")
	t.Setenv("PATH", directory)
	verboseDir := filepath.Join(t.TempDir(), "tmp", "doctor")
	entry := Entry{Name: "tool", Command: "tool", VersionArgs: []string{"--version"}, Parse: firstVersion}
	result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux", VerboseDir: verboseDir})[0]
	if result.State != StateOK || result.VerboseErr != "" {
		t.Fatalf("verbose probe result=%#v", result)
	}
	raw, err := os.ReadFile(filepath.Join(verboseDir, "tool-version.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "tool 4.2\n" {
		t.Fatalf("verbose output=%q", raw)
	}
}

func TestProbeSelfDoctorUnsupportedAndTimeoutAreHonest(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "unsupported", `
if [ "$1" = "--version" ]; then printf '1.0\n'; exit 0; fi
exit 2`)
	writeProbeStub(t, directory, "hung", `
if [ "$1" = "--version" ]; then printf '1.0\n'; exit 0; fi
exec /bin/sleep 30`)
	t.Setenv("PATH", directory)
	entries := []Entry{
		{
			Name:           "unsupported",
			Command:        "unsupported",
			Required:       true,
			VersionArgs:    []string{"--version"},
			Parse:          firstVersion,
			SelfDoctorArgs: []string{"doctor"},
		},
		{
			Name:           "hung",
			Command:        "hung",
			Required:       true,
			VersionArgs:    []string{"--version"},
			Parse:          firstVersion,
			SelfDoctorArgs: []string{"doctor"},
		},
	}
	// The separation that matters is between the bound and the hung command's
	// 30s sleep, never between the bound and a healthy stub's startup. A 250ms
	// self-doctor bound sat BELOW this platform's own cost to launch the very
	// fixtures written above — on macOS the first exec of a freshly written
	// script costs ~120ms median and ~553ms peak against ~6ms warm — so under
	// suite load the unsupported stub's --help was cancelled before it could
	// answer, and a healthy fixture reported itself as a broken engine. Match
	// production's ProbeTimeout, exactly as the sibling regression below does:
	// still six times clear of the 30s sleep the hung fixture must outrun,
	// while no longer racing the operating system to start a shell.
	results := Probe(context.Background(), entries, ProbeOptions{
		GOOS:              "linux",
		Timeout:           ProbeTimeout,
		SelfDoctorTimeout: ProbeTimeout,
	})
	if results[0].State != StateOK || results[0].SelfDoctor != "unavailable" {
		t.Fatalf("unsupported self-doctor=%#v", results[0])
	}
	if results[1].State != StateBroken || results[1].SelfDoctor != "broken" {
		t.Fatalf("hung self-doctor=%#v", results[1])
	}
}

// Regression: real `go version` output is "go version go1.24.13 linux/amd64" —
// firstVersion's "vV"-only trim never strips the "go" prefix on the version
// field, so the go dependency read as broken on every real host. codex-cli
// and tmux are carried alongside as guards: both already parse correctly and
// must keep doing so.
func TestFirstVersionParsesRealCommandVersionStrings(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "go", "printf 'go version go1.24.13 linux/amd64\\n'")
	writeProbeStub(t, directory, "codex", "printf 'codex-cli 0.148.0\\n'")
	writeProbeStub(t, directory, "tmux", "printf 'tmux 3.5a\\n'")
	t.Setenv("PATH", directory)

	tests := []struct {
		name        string
		versionArgs []string
		parse       func(string) (string, error)
		wantVersion string
	}{
		{name: "go", versionArgs: []string{"version"}, parse: firstVersion, wantVersion: "1.24.13"},
		{name: "codex", versionArgs: []string{"--version"}, parse: firstVersion, wantVersion: "0.148.0"},
		{name: "tmux", versionArgs: []string{"-V"}, parse: prefixedVersion("tmux"), wantVersion: "3.5a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := Entry{
				Name:        test.name,
				Command:     test.name,
				Required:    true,
				VersionArgs: test.versionArgs,
				Parse:       test.parse,
			}
			result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux"})[0]
			if result.State != StateOK {
				t.Fatalf("%s state=%s error=%q raw=%q, want ok", test.name, result.State, result.Error, result.Raw)
			}
			if result.Version != test.wantVersion {
				t.Fatalf("%s version=%q, want %q", test.name, result.Version, test.wantVersion)
			}
		})
	}
}

// Regression: probeSelfDoctor's second boundedOutput call (the summary
// itself, not the --help probe) maps every failure — including
// context.DeadlineExceeded — straight to "broken", the same bucket as an
// actually-broken binary. A codex doctor that legitimately runs past the
// probe timeout while scanning a large rollout corpus must not read as a
// broken engine and block install preflight; it must be named as a timeout
// distinct from a real failure.
func TestProbeSelfDoctorTimeoutIsNotConflatedWithBroken(t *testing.T) {
	// Subprocess startup competes with every other package during `go test
	// ./...`; 200ms made the quick --version/--help probes fail under ordinary
	// suite contention before this test ever reached the deliberate timeout.
	// On macOS the first exec of a freshly written executable — exactly what
	// writeProbeStub hands each subtest — costs ~120ms median and ~553ms peak
	// (vs ~6ms warm, measured over 60 stubs on an idle box), and `go test
	// ./...` execs dozens of those concurrently; matching production's
	// ProbeTimeout leaves ample scheduling room instead of re-deriving a
	// shorter number that flakes under load.
	const timeout = ProbeTimeout
	durationPattern := regexp.MustCompile(`\d+(\.\d+)?\s*(ms|s|m)\b`)

	t.Run("slow but healthy self-doctor stays ok and is named as a timeout", func(t *testing.T) {
		directory := t.TempDir()
		writeProbeStub(t, directory, "codex", `
if [ "$1" = "--version" ]; then printf 'codex-cli 0.148.0\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--summary" ]; then exec /bin/sleep 30; fi
exit 2`)
		t.Setenv("PATH", directory)
		entry := Entry{
			Name:           "codex",
			Command:        "codex",
			Required:       true,
			VersionArgs:    []string{"--version"},
			Parse:          firstVersion,
			SelfDoctorArgs: []string{"doctor", "--summary", "--ascii", "--no-color"},
		}
		result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: timeout})[0]
		if result.State != StateOK {
			t.Fatalf(
				"state=%s error=%q, want ok — a self-doctor that outran the probe timeout must not read as a broken engine",
				result.State,
				result.Error,
			)
		}
		if !strings.HasPrefix(result.SelfDoctor, "timeout") {
			t.Fatalf("self_doctor=%q, want it to start with %q", result.SelfDoctor, "timeout")
		}
		combined := result.SelfDoctor + " " + result.Error
		if !durationPattern.MatchString(combined) {
			t.Fatalf(
				"self_doctor=%q error=%q, want the timeout duration named in one of them",
				result.SelfDoctor,
				result.Error,
			)
		}
	})

	t.Run("quick non-zero self-doctor still reads broken", func(t *testing.T) {
		directory := t.TempDir()
		writeProbeStub(t, directory, "codex", `
if [ "$1" = "--version" ]; then printf 'codex-cli 0.148.0\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--summary" ]; then printf 'boom\n'; exit 3; fi
exit 2`)
		t.Setenv("PATH", directory)
		entry := Entry{
			Name:           "codex",
			Command:        "codex",
			Required:       true,
			VersionArgs:    []string{"--version"},
			Parse:          firstVersion,
			SelfDoctorArgs: []string{"doctor", "--summary", "--ascii", "--no-color"},
		}
		result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: timeout})[0]
		if result.State != StateBroken || result.SelfDoctor != "broken" {
			t.Fatalf("result=%#v, want StateBroken with self_doctor=broken", result)
		}
	})
}

func TestSelfDoctorProbeDoesNotInventFailureFromDumbParentTerminal(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "codex", `
if [ "$1" = "--version" ]; then printf 'codex-cli 0.149.0\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--summary" ]; then
  if [ "$TERM" = "dumb" ]; then printf 'TERM=dumb\n'; exit 1; fi
  printf 'healthy\n'; exit 0
fi
exit 2`)
	t.Setenv("PATH", directory)
	t.Setenv("TERM", "dumb")
	entry := Entry{
		Name: "codex", Command: "codex", Required: true,
		VersionArgs: []string{"--version"}, Parse: firstVersion,
		SelfDoctorArgs: []string{"doctor", "--summary", "--ascii", "--no-color"},
	}
	result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux"})[0]
	if result.State != StateOK || result.SelfDoctor != "ok" {
		t.Fatalf("result=%#v, want a healthy engine independent of PFM's non-interactive TERM", result)
	}
}

// TestProbeRecordsEachVersionProbe proves ProbeOptions.Runner is the seam a
// version probe crosses: obs cannot import deps (obs.Runner's own signature
// names deps.Runner), so the observed door lives at this boundary instead — a
// FakeRunner sees every VersionArgs invocation boundedOutputWithEnvironment
// would otherwise have run via a bare exec.CommandContext, and Probe's
// parsed result is unchanged by crossing it.
func TestProbeRecordsEachVersionProbe(t *testing.T) {
	runner := &FakeRunner{}
	runner.Script([]string{"/usr/bin/tmux", "-V"}, RunResult{Stdout: []byte("tmux 3.4\n"), ExitCode: 0}, nil)
	entries := []Entry{
		{
			Name: "tmux", Command: "/usr/bin/tmux", Required: true,
			VersionArgs: []string{"-V"}, Parse: prefixedVersion("tmux"),
		},
	}
	results := Probe(context.Background(), entries, ProbeOptions{
		GOOS: "linux", Timeout: ProbeTimeout, Runner: runner,
		LookPath: func(string) (string, error) { return "/usr/bin/tmux", nil },
	})
	if results[0].State != StateOK || results[0].Version != "3.4" {
		t.Fatalf("result=%#v, want OK 3.4 through the injected Runner", results[0])
	}
	calls := runner.Calls()
	if len(calls) != 1 || strings.Join(calls[0].Argv, " ") != "/usr/bin/tmux -V" {
		t.Fatalf("runner saw %v, want exactly one call carrying the version probe argv", calls)
	}
}

func writeProbeStub(t *testing.T, directory, name, body string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
