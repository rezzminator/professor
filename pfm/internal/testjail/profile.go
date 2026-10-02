package testjail

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// profiler is the always-on recorder of one package test process: a summary
// on every exit, and a diagnosis bundle (goroutines, execution-trace window,
// heap) when the process exits red or nears its -test.timeout. The testing
// package's own timeout panics without running cleanups, so the watchdog fires
// before it.
type profiler struct {
	dir      string
	pkg      string
	start    time.Time
	timeout  time.Duration
	root     string // module root directory, "" outside a module
	fr       *trace.FlightRecorder
	frErr    error
	watch    *time.Timer
	watchS   float64 // seconds after start the watchdog fires; meaningful when watch is set
	mu       sync.Mutex
	bundles  []string
	psi0     map[string]float64
	psiErr   error
	cpu      *os.File
	cpuErr   error
	helperOf string
}

// startProfile begins recording for this process. It returns a nil profiler
// when profiling is off or has nowhere to write; the second value is then the
// reason that deserves a line on a red run, and "" when the off was asked for
// (PFM_TEST_PROFILE=0) or is not a run at all (-test.list).
func startProfile() (*profiler, string) {
	if !flag.Parsed() {
		flag.Parse()
	}
	if paths.TestProfileMode() == "0" {
		return nil, ""
	}
	if f := flag.Lookup("test.list"); f != nil && f.Value.String() != "" {
		return nil, ""
	}
	root, _ := paths.TestArtifactDir()
	if root == "" {
		return nil, paths.EnvTestArtifactDir + " unset"
	}
	wd, moduleDir := moduleLocation()
	pkg := packageLabel(wd, moduleDir, buildInfoPath(), os.Args[0])
	dir := filepath.Join(root, fmt.Sprintf("%s.%d", pkg, os.Getpid()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Sprintf("mkdir %s: %v", dir, err)
	}
	p := &profiler{dir: dir, pkg: pkg, root: moduleDir, start: time.Now(), bundles: []string{}}
	p.psi0, p.psiErr = readPSI(psiDir)
	if paths.TestProfileMode() == "cpu" {
		p.startCPU()
	}
	if parent, _ := paths.TestProfileParent(); parent != "" {
		p.helperOf = parent
	} else if err := os.Setenv(paths.EnvTestProfileParent, strconv.Itoa(os.Getpid())); err != nil {
		warnSetup("profile: set %s: %v", paths.EnvTestProfileParent, err)
	}
	p.mu.Lock()
	p.startRecorder()
	p.mu.Unlock()
	if f := flag.Lookup("test.timeout"); f != nil {
		if d, err := time.ParseDuration(f.Value.String()); err == nil && d > 0 {
			p.timeout = d
		}
	}
	delay, source := p.planWatchdog()
	// Written now and overwritten at exit: a process that execs itself away or
	// is killed still leaves a summary saying it started and never finished.
	p.writeSummary(-1, "started-no-exit-recorded")
	if source != "" {
		p.watch = time.AfterFunc(delay, func() {
			p.stopCPU()
			p.bundle(source)
			p.writeSummary(-1, source+"-imminent")
		})
	}
	return p, ""
}

// planWatchdog picks when the watchdog fires and for which deadline ("timeout"
// or "deadline"); "" arms nothing. A helper arms none: it exits non-zero on
// purpose and its parent owns the diagnosis. The testing package's own timeout
// panics without running cleanups, so the watchdog fires before it.
func (p *profiler) planWatchdog() (time.Duration, string) {
	if p.helperOf != "" {
		return 0, ""
	}
	epoch, _ := paths.TestDeadlineEpoch()
	now := time.Now()
	deadline, source, warn := pickDeadline(now, p.start, p.timeout, epoch)
	if warn != "" {
		warnSetup("%s", warn)
	}
	if source == "" {
		return 0, ""
	}
	remaining := deadline.Sub(now)
	delay := max(remaining-watchdogMargin(remaining), 0)
	p.watchS = round3(now.Sub(p.start).Seconds() + delay.Seconds())
	return delay, source
}

// pickDeadline is the earlier of start + the -test.timeout and the step
// deadline epoch ("" when none is set). A step deadline that is not decimal
// seconds, or leaves one second or less, arms nothing and comes back as warn,
// naming the value; the -test.timeout watchdog is unaffected.
func pickDeadline(now, start time.Time, timeout time.Duration, epoch string) (deadline time.Time, source, warn string) {
	if timeout > 0 {
		deadline, source = start.Add(timeout), "timeout"
	}
	if epoch == "" {
		return deadline, source, ""
	}
	at, err := parseEpoch(epoch)
	switch {
	case err != nil:
		warn = fmt.Sprintf("profile: %s=%q is not decimal epoch seconds (%v): no deadline watchdog",
			paths.EnvTestDeadlineEpoch, epoch, err)
	case at.Sub(now) <= time.Second:
		warn = fmt.Sprintf("profile: %s=%q leaves %.1fs before the step deadline: no deadline watchdog",
			paths.EnvTestDeadlineEpoch, epoch, at.Sub(now).Seconds())
	case source == "" || at.Before(deadline):
		deadline, source = at, "deadline"
	}
	return deadline, source, warn
}

// watchdogMargin is how long before a deadline the watchdog fires: a tenth of
// what remains, between one and fifteen seconds.
func watchdogMargin(remaining time.Duration) time.Duration {
	return min(max(remaining/10, time.Second), 15*time.Second)
}

// epochForm is decimal epoch seconds as $EPOCHREALTIME prints them: digits,
// optionally a point and more digits.
var epochForm = regexp.MustCompile(`^(\d+)(?:\.(\d+))?$`)

// parseEpoch reads decimal epoch seconds; anything else is an error.
func parseEpoch(value string) (time.Time, error) {
	found := epochForm.FindStringSubmatch(value)
	if found == nil {
		return time.Time{}, errors.New("want digits with an optional .fraction")
	}
	sec, err := strconv.ParseInt(found[1], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	nsec, err := strconv.ParseInt((found[2] + "000000000")[:9], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, nsec), nil
}

// finish records the process's exit: always the summary, and on a red exit
// the diagnosis bundle.
func (p *profiler) finish(code int) {
	if p == nil {
		return
	}
	if p.watch != nil {
		p.watch.Stop()
	}
	p.stopCPU()
	event := "exit"
	switch {
	case p.helperOf != "":
		event = "helper-exit"
	case code != 0:
		p.bundle("exit")
	}
	p.writeSummary(code, event)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fr != nil {
		p.fr.Stop()
		p.fr = nil
	}
}

// flightRecorderConfig is the trace window every recorder of this process
// keeps, the first and each one PauseFlightRecorder restarts.
var flightRecorderConfig = trace.FlightRecorderConfig{MinAge: 10 * time.Second, MaxBytes: 16 << 20}

// startRecorder starts a new flight recorder in p.fr, or leaves p.fr nil and
// the reason in p.frErr. Callers hold p.mu: p.fr is read and replaced only
// under it, so a bundle never writes from a recorder mid-swap.
func (p *profiler) startRecorder() {
	fr := trace.NewFlightRecorder(flightRecorderConfig)
	if err := fr.Start(); err != nil {
		p.fr, p.frErr = nil, err
		return
	}
	p.fr, p.frErr = fr, nil
}

// pauseRecorder stops the running flight recorder on behalf of who and
// reports whether one was running; while paused, a bundle's errors.txt names
// who paused it.
func (p *profiler) pauseRecorder(who string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fr == nil {
		return false
	}
	p.fr.Stop()
	p.fr, p.frErr = nil, fmt.Errorf("paused by %s", who)
	return true
}

// resumeRecorder starts a new flight recorder after a pause; a refusal stays
// in p.frErr for the next bundle to name.
func (p *profiler) resumeRecorder() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fr == nil {
		p.startRecorder()
	}
}

// bundle writes goroutines, the trace window and a heap profile under
// {dir}/{reason}/. Each failure to write is recorded in the bundle's
// errors.txt, never dropped.
func (p *profiler) bundle(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dir := filepath.Join(p.dir, reason)
	var errs []error
	if err := os.MkdirAll(dir, 0o755); err != nil {
		warnSetup("profile bundle %s: %v", dir, err)
		return
	}
	var dump bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&dump, 2); err != nil {
		errs = append(errs, fmt.Errorf("goroutine dump: %w", err))
	} else if err := os.WriteFile(filepath.Join(dir, "goroutines.txt"), dump.Bytes(), 0o644); err != nil {
		errs = append(errs, fmt.Errorf("write goroutines: %w", err))
	}
	errs = append(errs, writeProfile(filepath.Join(dir, "heap.pprof"), "heap", 0))
	if p.fr == nil {
		errs = append(errs, fmt.Errorf("trace window: flight recorder not running: %v; no trace.out", p.frErr))
	} else {
		var buf bytes.Buffer
		if _, err := p.fr.WriteTo(&buf); err != nil {
			errs = append(errs, fmt.Errorf("trace window: %w", err))
		} else if err := os.WriteFile(filepath.Join(dir, "trace.out"), buf.Bytes(), 0o644); err != nil {
			errs = append(errs, fmt.Errorf("write trace window: %w", err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		_ = os.WriteFile(filepath.Join(dir, "errors.txt"), []byte(err.Error()+"\n"), 0o644)
	}
	p.bundles = append(p.bundles, reason)
	text := diagnose(p.root, reason, p.summary(-1, reason), dump.String())
	if err := os.WriteFile(filepath.Join(dir, "DIAGNOSIS.txt"), []byte(text), 0o644); err != nil {
		warnSetup("profile diagnosis %s: %v", dir, err)
	}
	warnSetup("profile bundle (%s) → %s/DIAGNOSIS.txt", reason, dir)
}

// startCPU profiles the whole process into cpu.pprof; a failure to start (a
// caller's own -cpuprofile already running) is recorded in the summary.
func (p *profiler) startCPU() {
	path := filepath.Join(p.dir, "cpu.pprof")
	f, err := os.Create(path)
	if err != nil {
		p.cpuErr = fmt.Errorf("create %s: %w", path, err)
		return
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		p.cpuErr = fmt.Errorf("start cpu profile: %w", err)
		return
	}
	p.cpu = f
}

func (p *profiler) stopCPU() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cpu == nil {
		return
	}
	pprof.StopCPUProfile()
	if err := p.cpu.Close(); err != nil {
		p.cpuErr = fmt.Errorf("close cpu profile: %w", err)
	}
	p.cpu = nil
}

func writeProfile(path, name string, debugLevel int) error {
	prof := pprof.Lookup(name)
	if prof == nil {
		return fmt.Errorf("profile %s: not registered", name)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := prof.WriteTo(f, debugLevel); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// writeSummary records wall, CPU, memory, I/O and scheduler delay of this
// process. A counter the platform cannot read is written as null with its
// reason, never as zero.
func (p *profiler) writeSummary(code int, event string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.summary(code, event)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		warnSetup("profile summary: %v", err)
		return
	}
	path := filepath.Join(p.dir, "summary.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		warnSetup("profile summary %s: %v", path, err)
	}
}

// summary reads this process's counters now. Callers hold p.mu.
func (p *profiler) summary(code int, event string) map[string]any {
	s := map[string]any{
		"package":    p.pkg,
		"pid":        os.Getpid(),
		"helper_of":  p.helperOf,
		"event":      event,
		"exit_code":  code,
		"wall_s":     round3(time.Since(p.start).Seconds()),
		"timeout_s":  p.timeout.Seconds(),
		"goroutines": runtime.NumGoroutine(),
		"bundles":    p.bundles,
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"num_cpu":    runtime.NumCPU(),
	}
	if p.watch != nil {
		s["watchdog_s"] = p.watchS
	} else {
		s["watchdog_s"] = nil
	}
	if p.cpuErr != nil {
		s["cpu_profile_error"] = p.cpuErr.Error()
	}
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		s["rusage_error"] = err.Error()
	} else {
		s["user_s"] = round3(timevalSeconds(ru.Utime))
		s["sys_s"] = round3(timevalSeconds(ru.Stime))
		s["maxrss_kb"] = maxRSSKB(ru.Maxrss)
		s["inblock"] = ru.Inblock
		s["oublock"] = ru.Oublock
		s["nvcsw"] = ru.Nvcsw
		s["nivcsw"] = ru.Nivcsw
	}
	if d, err := runDelaySeconds(); err != nil {
		s["run_delay_s"] = nil
		s["run_delay_error"] = err.Error()
	} else {
		s["run_delay_s"] = round3(d)
	}
	psi1, err := readPSI(psiDir)
	switch {
	case p.psiErr != nil:
		s["psi"] = nil
		s["psi_error"] = p.psiErr.Error()
	case err != nil:
		s["psi"] = nil
		s["psi_error"] = err.Error()
	default:
		delta := map[string]float64{}
		for k, v := range psi1 {
			delta[k] = round3(v - p.psi0[k])
		}
		s["psi"] = delta
	}
	return s
}

func timevalSeconds(tv syscall.Timeval) float64 {
	return float64(tv.Sec) + float64(tv.Usec)/1e6
}

func round3(v float64) float64 { return float64(int64(v*1000+0.5)) / 1000 }

// moduleLocation is the working directory and the directory of the nearest
// go.mod at or above it; each is "" when unknown (a go test binary runs in its
// package directory, so inside this module both are known).
func moduleLocation() (wd, root string) {
	wd, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	return wd, moduleRootOf(wd)
}

// moduleRootOf is the nearest directory at or above dir holding a go.mod, or
// "" when there is none.
func moduleRootOf(dir string) string {
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// buildInfoPath is the import path the toolchain recorded in this binary
// ("pkg.test" for a test binary), "" when it recorded none.
func buildInfoPath() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Path
	}
	return ""
}

// packageLabel names this process by its package directory relative to the
// module root, slashes flattened: internal/pricing → internal_pricing. With no
// module above the working directory, or the working directory at the module
// root, the binary's import path stands in with the ".test" suffix and this
// module's path dropped; a path outside this module yields the binary's own
// name minus ".test".
func packageLabel(wd, root, importPath, arg0 string) string {
	if root != "" {
		if rel, err := filepath.Rel(root, wd); err == nil && rel != "." {
			return strings.ReplaceAll(rel, string(filepath.Separator), "_")
		}
	}
	prefix := modulePrefix()
	if rest, ok := strings.CutPrefix(
		strings.TrimSuffix(importPath, ".test"),
		prefix,
	); ok && prefix != "" &&
		rest != "" {
		return strings.ReplaceAll(rest, "/", "_")
	}
	return strings.TrimSuffix(filepath.Base(arg0), ".test")
}
