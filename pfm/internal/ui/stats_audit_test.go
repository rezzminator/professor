package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
)

// Before the first sample, and where a host has no PSI or memory reading, the
// header says so instead of printing 0%.
func TestStatsHeaderNeverPrintsAMissingReadingAsZero(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats pfmstats.Snapshot
		want  string
	}{
		{"pending", pfmstats.Snapshot{}, "CPU … · PSI … · RAM … · SWAP …"},
		{
			"sampled without PSI",
			pfmstats.Snapshot{SampleTime: 1, Header: pfmstats.Header{PSIUnavailable: true, RAMPercent: 40}},
			"CPU … · PSI n/a · RAM 40% · SWAP 0%",
		},
		{
			"memory unread",
			pfmstats.Snapshot{SampleTime: 1, Header: pfmstats.Header{RAMUnread: true, SwapUnread: true}},
			"CPU … · PSI 0% · RAM err · SWAP err",
		},
	} {
		model := NewModel(fixtureSnapshot(120))
		model.tab, model.stats = TabStats, tc.stats
		if got := strings.TrimSpace(ansi.Strip(model.renderStatsHeader(120))); got != tc.want {
			t.Errorf("%s: header = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// statsPanelLines is a rendered stats panel line by line, the frame and the
// padding around each line cut away.
func statsPanelLines(panel string) []string {
	lines := strings.Split(ansi.Strip(panel), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight(strings.TrimLeft(line, "│╭╰─ "), "│╮╯─ ")
	}
	return lines
}

// The stats panel in every state it can meet: a seat without a process shows
// dashes and one whose probe failed shows err (never 0); names are never cut
// while the line has room; a host without cgroups says so; a sampled empty
// list is not pending; an unlimited container's MEM% without host RAM is a
// dash; a fleet longer than the panel scrolls to the cursor's page.
func TestStatsPanelRendersEveryState(t *testing.T) {
	const chatHeader = "NAME     ENGINE    CPU%↓      RSS   RAM%    TOKENS  TOK/MIN  GEAR        USAGE"
	const dockerHeader = "NAME     IMAGE                                                                                            CPU%↓   MEMORY    LIMIT   MEM%"
	chats := make([]pfmstats.Chat, 10)
	for index := range chats {
		chats[index] = pfmstats.Chat{Seat: string(rune('a' + index)), Name: "chat-" + string(rune('a'+index))}
	}
	for _, tc := range []struct {
		name   string
		width  int
		docker bool
		cursor int
		stats  pfmstats.Snapshot
		want   []string
	}{
		{
			name: "a seat without a process", width: 140,
			stats: pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{
				{Seat: "gone", Name: "GONE", Engine: "claude", NoProcess: true},
			}},
			want: []string{"stats", chatHeader, "GONE     claude        —        —      —         …        …     -            …", "", "", ""},
		},
		{
			name: "a seat whose probe failed", width: 140,
			stats: pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{
				{Seat: "unproved", Name: "UNPROVED", Engine: "claude", NoProcess: true, ProbeFailed: true},
			}},
			want: []string{"stats", chatHeader, "UNPROVED claude      err      err    err         …        …     -            …", "", "", ""},
		},
		{
			name: "a long chat name with room to spare", width: 180,
			stats: pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{
				{Seat: "long", Name: "a-chat-name-well-past-the-old-thirty-two-cap", Engine: "claude", CPUValid: true},
			}},
			want: []string{
				"stats",
				"NAME                                         ENGINE    CPU%↓      RSS   RAM%    TOKENS  TOK/MIN  GEAR        USAGE",
				"a-chat-name-well-past-the-old-thirty-two-cap claude     0.0%       0B   0.0%         …        …     -            …",
				"", "", "",
			},
		},
		{
			name: "two Docker names one the prefix of the other", width: 180, docker: true,
			stats: pfmstats.Snapshot{Ready: true, Docker: []pfmstats.Container{
				{ID: "1", Name: "acme-postgres-metrics", Image: "postgres:16"},
				{ID: "2", Name: "acme-postgres-metrics-test", Image: "postgres:16"},
			}},
			want: []string{
				"stats",
				"NAME                       IMAGE                                                                                                                  CPU%↓   MEMORY    LIMIT   MEM%",
				"acme-postgres-metrics      postgres:16                                                                                                                …       0B      max      —",
				"acme-postgres-metrics-test postgres:16                                                                                                                …       0B      max      —",
				"", "",
			},
		},
		{
			name: "a host without cgroups", width: 140, docker: true,
			stats: pfmstats.Snapshot{Ready: true, DockerUnsupported: true},
			want:  []string{"stats", dockerHeader, "Docker stats read Linux cgroups; this host has none", "", "", ""},
		},
		{
			name: "an empty list after the first sample", width: 140,
			stats: pfmstats.Snapshot{SampleTime: 1},
			want:  []string{"stats", chatHeader, "no live rows", "", "", ""},
		},
		{
			name: "an unlimited container without host RAM", width: 140, docker: true,
			stats: pfmstats.Snapshot{Ready: true, Docker: []pfmstats.Container{
				{ID: "a", Name: "free", Image: "img", MemoryBytes: 1 << 20},
			}},
			want: []string{
				"stats", dockerHeader,
				"free     img                                                                                                  …     1.0M      max      —",
				"", "", "",
			},
		},
		{
			name: "cursor 7 of 10 in a three-row panel", width: 140, cursor: 7,
			stats: pfmstats.Snapshot{Ready: true, Chats: chats},
			want: []string{
				"stats 7-9/10", chatHeader,
				"chat-g                 …       0B   0.0%         …        …     -            …",
				"› chat-h                 …       0B   0.0%         …        …     -            …",
				"chat-i                 …       0B   0.0%         …        …     -            …",
				"",
			},
		},
	} {
		model := NewModel(fixtureSnapshot(tc.width))
		model.tab, model.stats, model.statsCursor = TabStats, tc.stats, tc.cursor
		if tc.cursor > 0 { // a cursor marks its row only with the list focused
			model.statsFocus = StatsFocusContent
		}
		if tc.docker {
			model.statsSubtab = StatsDocker
		}
		if got := statsPanelLines(model.renderStatsPanel(tc.width, 6)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: panel =\n%q\nwant\n%q", tc.name, got, tc.want)
		}
	}
}

// c and m sort the Docker subtab too, the header marks the active key, and
// the cursor stays on its container across a re-sort and a shrinking list.
func TestStatsDockerSortsAndKeepsItsCursor(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.statsSubtab = StatsDocker
	model.statsFocus = StatsFocusContent
	model.applyStats(pfmstats.Snapshot{Ready: true, Docker: []pfmstats.Container{
		{ID: "a", Name: "alpha", CPUPercent: 1, CPUValid: true, MemoryBytes: 300},
		{ID: "b", Name: "bravo", CPUPercent: 9, CPUValid: true, MemoryBytes: 100},
		{ID: "c", Name: "charlie", CPUPercent: 5, CPUValid: true, MemoryBytes: 200},
	}})
	if got := dockerOrder(model); got != "bravo charlie alpha" {
		t.Fatalf("CPU order=%q", got)
	}
	next, _ := model.updateStatsKey(keyDown)
	model = next.(Model)
	if model.selectedDockerKey() != "c" {
		t.Fatalf("cursor on %q, want charlie", model.selectedDockerKey())
	}
	next, _ = model.updateStatsKey("m")
	model = next.(Model)
	if got := dockerOrder(model); got != "alpha charlie bravo" || model.selectedDockerKey() != "c" {
		t.Fatalf("RAM order=%q cursor=%q", got, model.selectedDockerKey())
	}
	header := strings.Split(ansi.Strip(model.renderStatsPanel(140, 6)), "\n")[1]
	if !strings.Contains(header, "MEMORY↓") || strings.Contains(header, "CPU%↓") {
		t.Fatalf("Docker header %q does not mark the RAM sort", header)
	}
	model.statsDockerCursor = 2
	model.applyStats(pfmstats.Snapshot{Ready: true, Docker: []pfmstats.Container{{ID: "z", Name: "zulu"}}})
	if model.statsDockerCursor != 0 || model.selectedDockerKey() != "z" {
		t.Fatalf("cursor=%d after the list shrank to one", model.statsDockerCursor)
	}
}

func dockerOrder(model Model) string {
	names := make([]string, len(model.stats.Docker))
	for index := range model.stats.Docker {
		names[index] = model.stats.Docker[index].Name
	}
	return strings.Join(names, " ")
}
