package testjail

import (
	"strings"
	"testing"
)

// cannedRoot is where the canned dumps pretend the module lives.
const cannedRoot = "/work/pfm"

// canned turns a dump template into a dump of this module: MOD/ is the module's
// import path, ROOT the directory the dump's files were built in.
func canned(dump string) string {
	return strings.NewReplacer("MOD/", modulePrefix(), "ROOT", cannedRoot).Replace(dump)
}

const cannedHangDump = `goroutine 21 [select (no cases)]:
MOD/internal/fleet.TestScanHangs(0xc000123456)
	ROOT/internal/fleet/scan_test.go:42 +0x25
testing.tRunner(0xc000123456, 0x5a1b10)
	/usr/local/go/src/testing/testing.go:1934 +0xea
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:1997 +0x465

goroutine 1 [chan receive]:
testing.(*T).Run(0xc0000b2000, {0x7a, 0x10}, 0x5a1b18)
	/usr/local/go/src/testing/testing.go:2000 +0x4f7
testing.runTests(0xc0000a6000, {0x7b, 0x1, 0x1}, 0xc00009c000)
	/usr/local/go/src/testing/testing.go:2400 +0x4d
testing.(*M).Run(0xc0000a6000)
	/usr/local/go/src/testing/testing.go:2300 +0x9ac
MOD/internal/testjail.Run(0xc0000a6000)
	ROOT/internal/testjail/testjail.go:140 +0x2a
MOD/internal/fleet.TestMain(0xc0000a6000)
	ROOT/internal/fleet/main_test.go:15 +0x1a
main.main()
	_testmain.go:50 +0x1d3

goroutine 22 [sync.Mutex.Lock, 2 minutes]:
sync.runtime_SemacquireMutex(0x0, 0x0, 0x1)
	/usr/local/go/src/runtime/sema.go:95 +0x25
sync.(*Mutex).Lock(...)
	/usr/local/go/src/sync/mutex.go:90
MOD/internal/fleet.worker(0xc000010000)
	ROOT/internal/fleet/scan.go:90 +0x3b
created by MOD/internal/fleet.Scan in goroutine 21
	ROOT/internal/fleet/scan.go:70 +0x9f

goroutine 23 [sync.Mutex.Lock, 2 minutes]:
sync.runtime_SemacquireMutex(0x0, 0x0, 0x1)
	/usr/local/go/src/runtime/sema.go:95 +0x25
sync.(*Mutex).Lock(...)
	/usr/local/go/src/sync/mutex.go:90
MOD/internal/fleet.worker(0xc000010008)
	ROOT/internal/fleet/scan.go:90 +0x3b
created by MOD/internal/fleet.Scan in goroutine 21
	ROOT/internal/fleet/scan.go:70 +0x9f

goroutine 24 [sync.Mutex.Lock, 2 minutes]:
sync.runtime_SemacquireMutex(0x0, 0x0, 0x1)
	/usr/local/go/src/runtime/sema.go:95 +0x25
sync.(*Mutex).Lock(...)
	/usr/local/go/src/sync/mutex.go:90
MOD/internal/fleet.worker(0xc000010010)
	ROOT/internal/fleet/scan.go:90 +0x3b
created by MOD/internal/fleet.Scan in goroutine 21
	ROOT/internal/fleet/scan.go:70 +0x9f

goroutine 5 [syscall]:
os/signal.signal_recv()
	/usr/local/go/src/runtime/sigqueue.go:149 +0x25
created by os/signal.Notify.func1.1 in goroutine 1
	/usr/local/go/src/os/signal/signal.go:152 +0x2f
`

// cannedReturnedDump is a process whose tests have all returned: no tRunner
// goroutine, one leaked worker.
const cannedReturnedDump = `goroutine 1 [running]:
MOD/internal/fleet.TestMain(0xc0000a6000)
	ROOT/internal/fleet/main_test.go:15 +0x1a
main.main()
	_testmain.go:50 +0x1d3

goroutine 9 [chan receive]:
MOD/internal/fleet.leak(0xc000010000)
	ROOT/internal/fleet/leak.go:12 +0x1c
created by MOD/internal/fleet.TestLeaky in goroutine 7
	ROOT/internal/fleet/leak_test.go:30 +0x5a
`

// cannedSubtestDump is a parent test waiting in t.Run and its subtest blocked
// inside a closure.
const cannedSubtestDump = `goroutine 30 [chan receive]:
testing.(*T).Run(0xc000102680, {0x7a, 0x4}, 0x5a1b18)
	/usr/local/go/src/testing/testing.go:2000 +0x4f7
MOD/internal/fleet.TestScan(0xc000102680)
	ROOT/internal/fleet/scan_test.go:60 +0x2e
testing.tRunner(0xc000102680, 0x5a1b10)
	/usr/local/go/src/testing/testing.go:1934 +0xea
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:1997 +0x465

goroutine 31 [select]:
MOD/internal/fleet.TestScan.func1(0xc000102820)
	ROOT/internal/fleet/scan_test.go:55 +0x71
testing.tRunner(0xc000102820, 0xc000010030)
	/usr/local/go/src/testing/testing.go:1934 +0xea
created by testing.(*T).Run in goroutine 30
	/usr/local/go/src/testing/testing.go:1997 +0x465
`

// cannedTrimpathDump is a -trimpath build's dump: files carry the import path.
const cannedTrimpathDump = `goroutine 8 [sleep]:
time.Sleep(0x3b9aca00)
	runtime/time.go:300 +0x15e
MOD/internal/fleet.TestSlow(0xc000102680)
	MOD/internal/fleet/slow_test.go:11 +0x2a
testing.tRunner(0xc000102680, 0x5a1b10)
	testing/testing.go:1934 +0xea
created by testing.(*T).Run in goroutine 1
	testing/testing.go:1997 +0x465
`

// cannedJunkDump carries lines the parser cannot place: a non-goroutine block,
// a goroutine header with no state, and non-call lines inside a goroutine
// (one of them opens a parenthesis it never closes).
const cannedJunkDump = `panic: something unrelated

goroutine 7:
MOD/internal/fleet.Lost(0x1)
	ROOT/internal/fleet/lost.go:1 +0x1

goroutine 12 [select]:
...additional frames elided...
MOD/internal/fleet.Waiter(0xc000010000)
	ROOT/internal/fleet/wait.go:20 +0x44
not a frame at all
	ROOT/internal/fleet/ghost.go:99 +0x1
MOD/internal/fleet.cutoff(0x1 trailing text
`

// cannedTieDump is two module goroutines in the same state, one each, listed
// here in the opposite order to the one they must be reported in.
const cannedTieDump = `goroutine 41 [select]:
MOD/internal/fleet.zulu(0xc000010000)
	ROOT/internal/fleet/z.go:9 +0x1

goroutine 40 [select]:
MOD/internal/fleet.alpha(0xc000010008)
	ROOT/internal/fleet/a.go:3 +0x1
`

func TestDiagnoseReadsADebugTwoDump(t *testing.T) {
	cases := []struct {
		name    string
		dump    string
		want    []string
		inOrder []string // each must follow the one before it
		wantNot []string
	}{
		{
			name: "running test with its state and frame, workers grouped with counts",
			dump: cannedHangDump,
			want: []string{
				"Tests running at capture:\n  fleet.TestScanHangs [select (no cases)] at internal/fleet/scan_test.go:42 (fleet.TestScanHangs)\n",
				"3× [sync.Mutex.Lock, 2 minutes] at internal/fleet/scan.go:90 (fleet.worker), " +
					"created by fleet.Scan in goroutine 21 at internal/fleet/scan.go:70\n",
				"6 goroutines in all:",
			},
			wantNot: []string{"/work/pfm", "signal_recv"},
		},
		{
			name: "the larger group is listed before the smaller one",
			dump: cannedHangDump,
			inOrder: []string{
				"  3× [sync.Mutex.Lock",
				"  1× [chan receive] at internal/fleet/main_test.go:15 (fleet.TestMain)",
			},
		},
		{
			name: "groups of the same size are listed in key order",
			dump: cannedTieDump,
			inOrder: []string{
				"  1× [select] at internal/fleet/a.go:3 (fleet.alpha)",
				"  1× [select] at internal/fleet/z.go:9 (fleet.zulu)",
			},
		},
		{
			name:    "TestMain is no test running, its goroutine is a module goroutine",
			dump:    cannedHangDump,
			want:    []string{"1× [chan receive] at internal/fleet/main_test.go:15 (fleet.TestMain)"},
			wantNot: []string{"fleet.TestMain [chan receive]"},
		},
		{
			name: "no test running",
			dump: cannedReturnedDump,
			want: []string{
				"Tests running at capture:\n  none (every test had returned)\n",
				"1× [chan receive] at internal/fleet/leak.go:12 (fleet.leak), created by fleet.TestLeaky in goroutine 7 at internal/fleet/leak_test.go:30\n",
			},
		},
		{
			name: "a subtest closure names the test function it sits in, beside its waiting parent",
			dump: cannedSubtestDump,
			want: []string{
				"  fleet.TestScan [chan receive] at internal/fleet/scan_test.go:60 (fleet.TestScan)\n",
				"  fleet.TestScan [select] at internal/fleet/scan_test.go:55 (fleet.TestScan.func1)\n",
			},
		},
		{
			name: "a trimpath build's import-path files read module-relative",
			dump: cannedTrimpathDump,
			want: []string{"  fleet.TestSlow [sleep] at internal/fleet/slow_test.go:11 (fleet.TestSlow)\n"},
		},
		{
			name: "lines it cannot place are skipped",
			dump: cannedJunkDump,
			want: []string{
				"1× [select] at internal/fleet/wait.go:20 (fleet.Waiter)\n",
				"1 goroutines in all:",
			},
			wantNot: []string{"Lost", "ghost", "unrelated", "elided", "not a frame", "cutoff"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := diagnose(cannedRoot, "timeout", map[string]any{}, canned(tc.dump))
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("diagnosis lacks %q:\n%s", want, text)
				}
			}
			last := -1
			for _, line := range tc.inOrder {
				at := strings.Index(text, line)
				if at < 0 || at < last {
					t.Errorf("diagnosis holds %q at %d, want it after offset %d:\n%s", line, at, last, text)
				}
				last = at
			}
			for _, not := range tc.wantNot {
				if strings.Contains(text, not) {
					t.Errorf("diagnosis holds %q:\n%s", not, text)
				}
			}
		})
	}
}

func TestParseGoroutinesKeepsOnlyWhatItCanPlace(t *testing.T) {
	gs := parseGoroutines(canned(cannedJunkDump))
	if len(gs) != 1 || gs[0].id != "12" || gs[0].state != "select" {
		t.Fatalf("parsed %+v, want only goroutine 12 [select]", gs)
	}
	if len(gs[0].frames) != 1 || !strings.HasSuffix(gs[0].frames[0].fn, "fleet.Waiter") ||
		gs[0].frames[0].file != cannedRoot+"/internal/fleet/wait.go:20" {
		t.Fatalf("frames %+v, want the one Waiter frame with its file:line", gs[0].frames)
	}
}

func TestUserFrameSkipsTheProfilersOwnFrames(t *testing.T) {
	prefix := modulePrefix()
	if prefix == "" {
		t.Fatal("modulePrefix is empty: user frames cannot be told from the standard library")
	}
	own := frame{
		fn:   prefix + "internal/testjail.(*profiler).bundle",
		file: cannedRoot + "/internal/testjail/profile.go:200",
	}
	user := frame{fn: prefix + "internal/fleet.Scan", file: cannedRoot + "/internal/fleet/scan.go:42"}
	std := frame{fn: "sync.(*Mutex).Lock", file: "/usr/local/go/src/sync/mutex.go:90"}
	cases := []struct {
		name   string
		frames []frame
		want   frame
		found  bool
	}{
		{
			"profiler frames, then the first module frame outside internal/testjail",
			[]frame{std, own, own, user, own},
			user,
			true,
		},
		{"the innermost module frame when none is the profiler's", []frame{std, user, own}, user, true},
		{"only the profiler's frames", []frame{own, std}, frame{}, false},
		{"only the standard library", []frame{std}, frame{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := goroutine{frames: tc.frames}.userFrame(prefix)
			if found != tc.found || got != tc.want {
				t.Fatalf("userFrame = (%+v, %v), want (%+v, %v)", got, found, tc.want, tc.found)
			}
		})
	}
}

func TestDiagnoseNamesTheFrameOutsideTheProfiler(t *testing.T) {
	dump := canned(`goroutine 40 [chan receive]:
MOD/internal/testjail.(*profiler).bundle(0xc000010000, {0x1, 0x2})
	ROOT/internal/testjail/profile.go:200 +0x1
MOD/internal/fleet.Scan(0xc000010000)
	ROOT/internal/fleet/scan.go:42 +0x2
created by MOD/internal/fleet.Boot in goroutine 1
	ROOT/internal/fleet/boot.go:7 +0x3
`)
	text := diagnose(cannedRoot, "exit", map[string]any{}, dump)
	want := "1× [chan receive] at internal/fleet/scan.go:42 (fleet.Scan), created by fleet.Boot in goroutine 1 at internal/fleet/boot.go:7\n"
	if !strings.Contains(text, want) {
		t.Fatalf("diagnosis lacks %q:\n%s", want, text)
	}
}

func TestShortFile(t *testing.T) {
	mod := modulePrefix()
	cases := []struct{ name, file, root, want string }{
		{
			"module root directory stripped",
			cannedRoot + "/internal/fleet/scan.go:42",
			cannedRoot,
			"internal/fleet/scan.go:42",
		},
		{
			"a root with a trailing slash",
			cannedRoot + "/internal/fleet/scan.go:42",
			cannedRoot + "/",
			"internal/fleet/scan.go:42",
		},
		{
			"import path stripped when the root is unknown",
			mod + "internal/fleet/scan.go:42",
			"",
			"internal/fleet/scan.go:42",
		},
		{
			"import path stripped beside a root",
			mod + "internal/fleet/scan.go:42",
			cannedRoot,
			"internal/fleet/scan.go:42",
		},
		{
			"a sibling directory is not the root",
			cannedRoot + "-other/internal/x.go:1",
			cannedRoot,
			cannedRoot + "-other/internal/x.go:1",
		},
		{
			"a path under neither is unchanged",
			"/usr/local/go/src/testing/testing.go:1934",
			cannedRoot,
			"/usr/local/go/src/testing/testing.go:1934",
		},
		{
			"a pfm directory elsewhere is no module root",
			"/src/pfm/internal/x.go:1",
			cannedRoot,
			"/src/pfm/internal/x.go:1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortFile(tc.file, tc.root); got != tc.want {
				t.Fatalf("shortFile(%q, %q) = %q, want %q", tc.file, tc.root, got, tc.want)
			}
		})
	}
}

func TestDiagnoseNeverRendersAnUnreadableCounterAsZero(t *testing.T) {
	summary := map[string]any{
		"package": "internal_fleet", "pid": 7, "wall_s": 1.5, "timeout_s": 4.0,
		"psi": nil, "psi_error": "psi: open /proc/pressure/cpu: no such file or directory",
		"run_delay_s": nil, "run_delay_error": "run delay: not available on darwin",
		"rusage_error": "getrusage: boom",
	}
	text := diagnose(cannedRoot, "exit", summary, canned(cannedReturnedDump))
	for _, want := range []string{
		"VM pressure while this process ran: not measured (psi: open /proc/pressure/cpu: no such file or directory)\n",
		"run delay not measured (run delay: not available on darwin)",
		"cpu user not measured (getrusage: boom) sys not measured (getrusage: boom)",
		"max rss not measured (getrusage: boom)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnosis lacks %q:\n%s", want, text)
		}
	}
}

func TestDiagnoseRendersMeasuredCounters(t *testing.T) {
	summary := map[string]any{
		"package": "internal_fleet", "pid": 7, "wall_s": 1.5, "timeout_s": 4.0,
		"user_s": 0.25, "sys_s": 0.5, "run_delay_s": 0.125, "maxrss_kb": int64(31800),
		"psi": map[string]float64{
			"cpu_some": 0.005, "io_some": 0.01, "io_full": 0.02, "memory_some": 0.03, "memory_full": 0.04,
		},
	}
	text := diagnose(cannedRoot, "exit", summary, canned(cannedReturnedDump))
	for _, want := range []string{
		"DIAGNOSIS exit — package internal_fleet, pid 7\n",
		"wall 1.5s of timeout 4s · cpu user 0.25s sys 0.5s · run delay 0.125s · max rss 31800 KB\n",
		"stalled s — cpu some 0.005 · io some 0.010 full 0.020 · memory some 0.030 full 0.040\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnosis lacks %q:\n%s", want, text)
		}
	}
}
