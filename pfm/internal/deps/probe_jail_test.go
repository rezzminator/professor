//go:build jail

package deps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestProbeDistinguishesOKMinimumGarbageMissingAndTimeout(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "tmux-ok", "printf 'tmux 3.4\\n'")
	writeProbeStub(t, directory, "tmux-old", "printf 'tmux 1.7\\n'")
	writeProbeStub(t, directory, "garbage", "printf 'not-a-version\\n'")
	writeProbeStub(t, directory, "timeout", `: > "$PROBE_PHASE_MARKER"; exec /bin/sleep 30`)
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
		{Name: "failed", Command: "failed", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion},
	}
	results := Probe(context.Background(), entries, ProbeOptions{
		GOOS: "linux", Timeout: ProbeTimeout,
	})
	want := []State{StateOK, StateBroken, StateBroken, StateMissing, StateBroken}
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
	hung := probeAtBound(t, ProbeTimeout, Entry{
		Name: "timeout", Command: "timeout", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion,
	}, ProbeOptions{GOOS: "linux", Timeout: ProbeTimeout})
	if hung.State != StateTimeout {
		t.Fatalf("hung state=%s error=%q, want StateTimeout", hung.State, hung.Error)
	}
	if hung.Error != "timeout (5s)" {
		t.Fatalf(
			"timeout error=%q, want it to name the enforced bound %q rather than a bare sentinel",
			hung.Error,
			"timeout (5s)",
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
	writeProbeStub(t, directory, "hung", `: > "$PROBE_PHASE_MARKER"; exec /bin/sleep 30`)
	writeProbeStub(t, directory, "broken", "printf 'permission denied by fixture\\n'; exit 7")
	t.Setenv("PATH", directory)

	hung := probeAtBound(t, ProbeTimeout, Entry{
		Name: "hung", Command: "hung", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion,
	}, ProbeOptions{GOOS: "linux", Timeout: ProbeTimeout})
	broken := Probe(context.Background(), []Entry{{Name: "broken", Command: "broken", Required: true, VersionArgs: []string{"--version"}, Parse: firstVersion}}, ProbeOptions{GOOS: "linux", Timeout: ProbeTimeout})[0]

	if hung.State != StateTimeout {
		t.Fatalf(
			"hung state=%s error=%q, want StateTimeout — an outran bound must not read as broken",
			hung.State,
			hung.Error,
		)
	}
	if hung.Error != "timeout (5s)" {
		t.Fatalf("hung error=%q, want the enforced bound named: %q", hung.Error, "timeout (5s)")
	}
	if broken.State != StateBroken {
		t.Fatalf(
			"broken state=%s error=%q, want StateBroken — a genuinely broken tool must not be relabelled as a timeout",
			broken.State,
			broken.Error,
		)
	}
}

func TestVersionProbePreservesParentCancellationAndDeadline(t *testing.T) {
	directory := t.TempDir()
	writeProbeStub(t, directory, "hung", `: > "$PROBE_PHASE_MARKER"; exec /bin/sleep 30`)
	t.Setenv("PATH", directory)
	entry := Entry{
		Name:        "hung",
		Command:     "hung",
		Required:    true,
		VersionArgs: []string{"--version"},
		Parse:       firstVersion,
	}

	t.Run("cancelled", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "hung-started")
		t.Setenv("PROBE_PHASE_MARKER", marker)
		ctx, cancel := context.WithCancel(context.Background())
		resultCh := make(chan Result, 1)
		go func() {
			resultCh <- Probe(ctx, []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: probeStageBound})[0]
		}()
		waitForProbePhaseMarker(t, marker)
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
		marker := filepath.Join(t.TempDir(), "hung-started")
		t.Setenv("PROBE_PHASE_MARKER", marker)
		ctx := newDeferredDeadlineContext()
		resultCh := make(chan Result, 1)
		go func() {
			resultCh <- Probe(ctx, []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: probeStageBound})[0]
		}()
		waitForProbePhaseMarker(t, marker)
		ctx.expire()
		result := <-resultCh
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

			probeTimeout := probeStageBound
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

// probeStageBound is the per-call bound these tests hand to Probe() for its
// version probe and each self-doctor stage. It is production patience these
// tests never judge: a stage that outran a short guess on a loaded box made
// probeSelfDoctor return "broken" before the phase marker the test waits for
// was written. The bound only has to be longer than any healthy run, and a
// stage that hangs is failed by waitForProbePhaseMarker, never by this bound.
const probeStageBound = testwait.HungLimit

// probeAtBound probes entry with its bounds timed on a fake clock and ends the
// one call that hangs at its bound: the stub writes $PROBE_PHASE_MARKER as it
// starts to hang, a moment before bound the call still waits, at it Probe
// returns the result this test judges.
func probeAtBound(t *testing.T, bound time.Duration, entry Entry, options ProbeOptions) Result {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "hung-started")
	t.Setenv("PROBE_PHASE_MARKER", marker)
	fake := clock.NewFake(time.Unix(0, 0))
	options.Clock = fake
	resultCh := make(chan Result, 1)
	go func() { resultCh <- Probe(context.Background(), []Entry{entry}, options)[0] }()
	waitForProbePhaseMarker(t, marker)
	if pending := fake.Pending(); pending != 1 {
		t.Fatalf("armed probe bounds = %d (%s), want the hung call's one", pending, fake)
	}
	fake.Advance(bound - time.Nanosecond)
	if pending := fake.Pending(); pending != 1 {
		t.Fatalf("the probe bound fired %s before %s: %s", time.Nanosecond, bound, fake)
	}
	fake.Advance(time.Nanosecond)
	result, _ := testwait.Receive(t, "Probe to return once its bound fired", resultCh)
	return result
}

func waitForProbePhaseMarker(t *testing.T, marker string) {
	t.Helper()
	testwait.Until(t, "the probe stub to write its phase marker "+marker, func() (bool, string) {
		_, err := os.Stat(marker)
		if err == nil {
			return true, ""
		}
		if !os.IsNotExist(err) {
			t.Fatalf("stat probe phase marker %q: %v", marker, err)
		}
		return false, "marker not created"
	})
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
	if got, want := strings.ReplaceAll(fmt.Sprintf("%v · %s", result.State, result.Error), directory, "{dir}"),
		"broken · self-doctor failed raw=\"[FAIL] auth — active model provider auth env var is missing\""; got != want {
		t.Fatalf("self-doctor result = %q, want %q", got, want)
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
: > "$PROBE_PHASE_MARKER"
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
	unsupported := Probe(context.Background(), entries[:1], ProbeOptions{
		GOOS:              "linux",
		Timeout:           ProbeTimeout,
		SelfDoctorTimeout: ProbeTimeout,
	})[0]
	hung := probeAtBound(t, DefaultSelfDoctorTimeout, entries[1], ProbeOptions{
		GOOS:              "linux",
		Timeout:           ProbeTimeout,
		SelfDoctorTimeout: DefaultSelfDoctorTimeout,
	})
	if unsupported.State != StateOK || unsupported.SelfDoctor != "unavailable" {
		t.Fatalf("unsupported self-doctor=%#v", unsupported)
	}
	if hung.State != StateBroken || hung.SelfDoctor != "broken" {
		t.Fatalf("hung self-doctor=%#v", hung)
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
	durationPattern := regexp.MustCompile(`\d+(\.\d+)?\s*(ms|s|m)\b`)

	t.Run("slow but healthy self-doctor stays ok and is named as a timeout", func(t *testing.T) {
		directory := t.TempDir()
		writeProbeStub(t, directory, "codex", `
if [ "$1" = "--version" ]; then printf 'codex-cli 0.148.0\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--help" ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "$1" = "doctor" ] && [ "$2" = "--summary" ]; then : > "$PROBE_PHASE_MARKER"; exec /bin/sleep 30; fi
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
		result := probeAtBound(t, DefaultSelfDoctorTimeout, entry, ProbeOptions{
			GOOS: "linux", Timeout: ProbeTimeout, SelfDoctorTimeout: DefaultSelfDoctorTimeout,
		})
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
		result := Probe(context.Background(), []Entry{entry}, ProbeOptions{GOOS: "linux", Timeout: ProbeTimeout})[0]
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
