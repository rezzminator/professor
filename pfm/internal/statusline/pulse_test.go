package statusline

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// pulseNow is a render time on the pulse's five-second grid.
var pulseNow = time.Unix(1_790_000_000, 0)

// pulseTraceReadings is a trace that holds every kind of step: no growth, three
// of the five growth levels, a compaction, and a recount that sheds a few tokens.
func pulseTraceReadings() []pulseReading {
	at := func(seconds int) int64 { return pulseNow.Add(time.Duration(seconds) * time.Second).UnixMilli() }
	return []pulseReading{
		{At: at(-80), Tokens: 9_000},
		{At: at(-72), Tokens: 10_000},
		{At: at(-62), Tokens: 10_300},
		{At: at(-41), Tokens: 13_500},
		{At: at(-33), Tokens: 63_500},
		{At: at(-22), Tokens: 12_000},
		{At: at(-11), Tokens: 11_990},
		{At: at(-3), Tokens: 12_240},
		{At: at(0), Tokens: 12_241},
	}
}

// The agent row's scale, row for row (plugins/professor/tests/pulse.test.ts):
// a chat's trace and its agents' must mean the same growth by the same glyph.
func TestPulseLevelIsTheAgentRowsScale(t *testing.T) {
	cases := []struct {
		grown int64
		level int
	}{
		{-90_000, pulseLevelDrop},
		{-1_001, pulseLevelDrop},
		{-1_000, pulseLevelDrop},
		{-999, 0},
		{-1, 0},
		{0, 0},
		{1, 1},
		{999, 1},
		{1_000, 2},
		{4_999, 2},
		{5_000, 3},
		{19_999, 3},
		{20_000, 4},
		{49_999, 4},
		{50_000, 5},
		{1_000_000, 5},
	}
	for _, tc := range cases {
		if got := pulseLevel(tc.grown); got != tc.level {
			t.Errorf("pulseLevel(%d) = %d, want %d", tc.grown, got, tc.level)
		}
	}
}

func TestPulseLitIsTheAgentRowsFill(t *testing.T) {
	cases := []struct {
		used, window int64
		lit          int
	}{
		{0, 1_000, 0},
		{1, 1_000, 1},
		{100, 1_000, 2},
		{500, 1_000, 8},
		{999, 1_000, 14},
		{1_000, 1_000, 15},
		{1_500, 1_000, 15},
		{10, 0, 0},
	}
	for _, tc := range cases {
		if got := pulseLit(tc.used, tc.window, 15); got != tc.lit {
			t.Errorf("pulseLit(%d, %d, 15) = %d, want %d", tc.used, tc.window, got, tc.lit)
		}
	}
}

func TestPulseStripDrawsFillAndGrowthInOneStrip(t *testing.T) {
	first := []pulseReading{{At: pulseNow.Add(-12 * time.Second).UnixMilli(), Tokens: 700}}
	for _, tc := range []struct {
		name     string
		readings []pulseReading
		lit      int
		cells    int
		tone     string
		want     string
		grown    int64
	}{
		{
			name: "every kind of step, the first four cells lit", readings: pulseTraceReadings(), lit: 4, cells: 15,
			tone: cGood, grown: 1,
			want: "\x1b[38;5;118m\x1b[7m_⎽__\x1b[0m\x1b[38;5;117m_⎼_¯_↓___⎽⎽\x1b[0m",
		},
		{
			name: "the narrow strip keeps the newest steps", readings: pulseTraceReadings(), lit: 8, cells: 8,
			tone: cBad, grown: 1,
			want: "\x1b[1;38;5;203m\x1b[7m¯_↓___⎽⎽\x1b[0m",
		},
		{
			name: "steps before the first reading are unborn, lit ones blank blocks", readings: first, lit: 2,
			cells: 15, tone: cWarn,
			want: "\x1b[38;5;226m\x1b[7m  \x1b[0m\x1b[38;5;245m__________\x1b[0m\x1b[38;5;117m___\x1b[0m",
		},
		{
			name: "no reading at all", lit: 0, cells: 15, tone: cMuted,
			want: "\x1b[38;5;245m_______________\x1b[0m",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			strip, grown := pulseTrace(tc.readings, pulseNow, tc.lit, tc.cells)
			if got := pulseStrip(strip, tc.tone); got != tc.want {
				t.Errorf("strip = %q, want %q", got, tc.want)
			}
			if grown != tc.grown {
				t.Errorf("grown = %d, want %d", grown, tc.grown)
			}
		})
	}
}

func TestPulseReadingsKeepOneReadingAStepAndOnlyWhatTheStripNeeds(t *testing.T) {
	var warn bytes.Buffer
	runtime := Runtime{CacheDir: t.TempDir(), Warn: &warn}
	path := filepath.Join(runtime.CacheDir, "cc-sl-pulse-session-1")
	for _, step := range []struct {
		name   string
		after  time.Duration
		tokens int64
		known  bool
		want   []pulseReading
		file   string
	}{
		{
			name: "the first reading", tokens: 1_000, known: true,
			want: []pulseReading{{At: 1_790_000_000_000, Tokens: 1_000}},
			file: `[{"at":1790000000000,"tokens":1000}]`,
		},
		{
			name: "a second render in the same step replaces it", after: 2 * time.Second, tokens: 1_200, known: true,
			want: []pulseReading{{At: 1_790_000_002_000, Tokens: 1_200}},
			file: `[{"at":1790000002000,"tokens":1200}]`,
		},
		{
			name: "a later step that reads the only reading's size adds none", after: 5 * time.Second, tokens: 1_200,
			known: true,
			want:  []pulseReading{{At: 1_790_000_002_000, Tokens: 1_200}},
			file:  `[{"at":1790000002000,"tokens":1200}]`,
		},
		{
			name: "the next step adds one", after: 5 * time.Second, tokens: 1_500, known: true,
			want: []pulseReading{{At: 1_790_000_002_000, Tokens: 1_200}, {At: 1_790_000_005_000, Tokens: 1_500}},
			file: `[{"at":1790000002000,"tokens":1200},{"at":1790000005000,"tokens":1500}]`,
		},
		{
			name: "a render that knows no size adds none", after: 10 * time.Second,
			want: []pulseReading{{At: 1_790_000_002_000, Tokens: 1_200}, {At: 1_790_000_005_000, Tokens: 1_500}},
			file: `[{"at":1790000002000,"tokens":1200},{"at":1790000005000,"tokens":1500}]`,
		},
		{
			name: "a later step that reads the same size adds none", after: 12 * time.Second, tokens: 1_500, known: true,
			want: []pulseReading{{At: 1_790_000_002_000, Tokens: 1_200}, {At: 1_790_000_005_000, Tokens: 1_500}},
			file: `[{"at":1790000002000,"tokens":1200},{"at":1790000005000,"tokens":1500}]`,
		},
		{
			name: "the reading one step before the strip stays, the one before it goes", after: 80 * time.Second,
			tokens: 1_800, known: true,
			want: []pulseReading{{At: 1_790_000_005_000, Tokens: 1_500}, {At: 1_790_000_080_000, Tokens: 1_800}},
			file: `[{"at":1790000005000,"tokens":1500},{"at":1790000080000,"tokens":1800}]`,
		},
		{
			name: "past the strip only the newest older reading stays", after: 200 * time.Second, tokens: 2_000,
			known: true,
			want:  []pulseReading{{At: 1_790_000_080_000, Tokens: 1_800}, {At: 1_790_000_200_000, Tokens: 2_000}},
			file:  `[{"at":1790000080000,"tokens":1800},{"at":1790000200000,"tokens":2000}]`,
		},
	} {
		got := pulseReadings(runtime, "session-1", pulseNow.Add(step.after), step.tokens, step.known)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s: readings = %v, want %v", step.name, got, step.want)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != step.file {
			t.Fatalf("%s: %s holds %s, want %s", step.name, path, body, step.file)
		}
	}
	if warn.String() != "" {
		t.Fatalf("warned %q, want nothing", warn.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("%s has mode %o, want 600", path, got)
	}
}

func TestPulseReadingsNameAFileTheyCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session string
		plant   func(t *testing.T, path string)
		want    []pulseReading
		warn    string
		entries []string
	}{
		{name: "no session id", session: ""},
		{name: "a session id that is no file name", session: "../escape"},
		{
			name: "a file that does not parse starts again", session: "session-1",
			plant: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want:    []pulseReading{{At: 1_790_000_000_000, Tokens: 500}},
			warn:    "statusline: parse pulse readings {path}: unexpected end of JSON input\n",
			entries: []string{"cc-sl-pulse-session-1"},
		},
		{
			name: "a path that cannot be read has no trace", session: "session-1",
			plant: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			warn:    "statusline: read pulse readings {path}: read {path}: is a directory\n",
			entries: []string{"cc-sl-pulse-session-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warn bytes.Buffer
			runtime := Runtime{CacheDir: t.TempDir(), Warn: &warn}
			path := filepath.Join(runtime.CacheDir, "cc-sl-pulse-"+tc.session)
			if tc.plant != nil {
				tc.plant(t, path)
			}
			got := pulseReadings(runtime, tc.session, pulseNow, 500, true)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("readings = %v, want %v", got, tc.want)
			}
			wantWarn := bytes.ReplaceAll([]byte(tc.warn), []byte("{path}"), []byte(path))
			if warn.String() != string(wantWarn) {
				t.Errorf("warned %q, want %q", warn.String(), wantWarn)
			}
			var entries []string
			listed, err := os.ReadDir(runtime.CacheDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range listed {
				entries = append(entries, entry.Name())
			}
			if !reflect.DeepEqual(entries, tc.entries) {
				t.Errorf("%s holds %v, want %v", runtime.CacheDir, entries, tc.entries)
			}
		})
	}
}
