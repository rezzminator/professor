package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
)

// Before the first sample, and where a host has no PSI, the header says so
// instead of printing 0%.
func TestStatsHeaderNeverPrintsAMissingReadingAsZero(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	model.tab = TabStats
	pending := ansi.Strip(model.renderStatsHeader(120))
	for _, want := range []string{"CPU …", "PSI …", "RAM …", "SWAP …"} {
		if !strings.Contains(pending, want) {
			t.Fatalf("pending header %q missing %q", pending, want)
		}
	}
	model.stats = pfmstats.Snapshot{SampleTime: 1, Header: pfmstats.Header{PSIUnavailable: true, RAMPercent: 40}}
	sampled := ansi.Strip(model.renderStatsHeader(120))
	for _, want := range []string{"PSI n/a", "RAM 40%", "SWAP 0%"} {
		if !strings.Contains(sampled, want) {
			t.Fatalf("sampled header %q missing %q", sampled, want)
		}
	}
}

func TestStatsSeatWithoutAProcessRendersDashes(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.stats = pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{
		{Seat: "gone", Name: "GONE", Engine: "claude", NoProcess: true},
	}}
	panel := ansi.Strip(model.renderStatsPanel(140, 6))
	row := strings.Split(panel, "\n")[2]
	if strings.Count(row, "—") != 3 || strings.Contains(row, "0B") || strings.Contains(row, "0.0%") {
		t.Fatalf("process-less row %q, want CPU, RSS and RAM as dashes", row)
	}
}

func TestStatsSeatWhoseProbeFailedRendersAnError(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.stats = pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{
		{Seat: "unproved", Name: "UNPROVED", Engine: "claude", NoProcess: true, ProbeFailed: true},
	}}
	row := strings.Split(ansi.Strip(model.renderStatsPanel(140, 6)), "\n")[2]
	if strings.Count(row, "err") != 3 || strings.Contains(row, "—") {
		t.Fatalf("probe-failed row %q, want CPU, RSS and RAM as err", row)
	}
}

// Names identify a row: with room on the line, none is truncated into an
// ambiguous prefix of its neighbour.
func TestStatsNameColumnsFitTheirNames(t *testing.T) {
	model := NewModel(fixtureSnapshot(180))
	model.tab = TabStats
	long := "a-chat-name-well-past-the-old-thirty-two-cap"
	model.stats = pfmstats.Snapshot{
		Ready: true,
		Chats: []pfmstats.Chat{{Seat: "long", Name: long, Engine: "claude", CPUValid: true}},
		Docker: []pfmstats.Container{
			{ID: "1", Name: "acme-postgres-metrics", Image: "postgres:16"},
			{ID: "2", Name: "acme-postgres-metrics-test", Image: "postgres:16"},
		},
	}
	if panel := ansi.Strip(model.renderStatsPanel(180, 6)); !strings.Contains(panel, long) {
		t.Fatalf("chat name truncated with room to spare:\n%s", panel)
	}
	model.statsSubtab = StatsDocker
	panel := ansi.Strip(model.renderStatsPanel(180, 6))
	for _, name := range []string{"acme-postgres-metrics ", "acme-postgres-metrics-test"} {
		if !strings.Contains(panel, name) {
			t.Fatalf("Docker name %q truncated:\n%s", name, panel)
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

func TestStatsDockerSaysWhenTheHostHasNoCgroups(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.statsSubtab = StatsDocker
	model.stats = pfmstats.Snapshot{Ready: true, DockerUnsupported: true}
	if panel := ansi.Strip(model.renderStatsPanel(140, 6)); !strings.Contains(panel, "no cgroups") &&
		!strings.Contains(panel, "has none") {
		t.Fatalf("unsupported Docker reads as empty:\n%s", panel)
	}
}

// The first sample landed with no live rows: the list says so, as the header
// already shows real readings, rather than still waiting.
func TestStatsEmptyListAfterTheFirstSampleIsNotPending(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.stats = pfmstats.Snapshot{SampleTime: 1}
	if panel := ansi.Strip(model.renderStatsPanel(140, 6)); !strings.Contains(panel, "no live rows") {
		t.Fatalf("sampled empty list still pending:\n%s", panel)
	}
}

// An unlimited container's MEM% is a share of host RAM; with host RAM unread
// it is unknown, never a measured 0.0%.
func TestStatsUnlimitedContainerWithoutHostRAMIsUnknown(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.statsSubtab = StatsDocker
	model.stats = pfmstats.Snapshot{Ready: true, Docker: []pfmstats.Container{
		{ID: "a", Name: "free", Image: "img", MemoryBytes: 1 << 20},
	}}
	row := strings.Split(ansi.Strip(model.renderStatsPanel(140, 6)), "\n")[2]
	if strings.Contains(row, "0.0%") || !strings.HasSuffix(strings.TrimRight(row, " │"), "—") {
		t.Fatalf("unlimited container without host RAM %q, want MEM%% as a dash", row)
	}
}

func dockerOrder(model Model) string {
	names := make([]string, len(model.stats.Docker))
	for index := range model.stats.Docker {
		names[index] = model.stats.Docker[index].Name
	}
	return strings.Join(names, " ")
}

// A fleet longer than the panel scrolls to the cursor's page; rows past the
// bottom border never vanish under a cursor that moved onto them.
func TestStatsListsScrollToTheCursor(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.statsFocus = StatsFocusContent
	chats := make([]pfmstats.Chat, 10)
	for index := range chats {
		chats[index] = pfmstats.Chat{Seat: string(rune('a' + index)), Name: "chat-" + string(rune('a'+index))}
	}
	model.stats = pfmstats.Snapshot{Ready: true, Chats: chats}
	model.statsCursor = 7
	panel := ansi.Strip(model.renderStatsPanel(140, 6))
	if !strings.Contains(panel, "chat-h") || strings.Contains(panel, "chat-a") ||
		!strings.Contains(panel, "stats 7-9/10") {
		t.Fatalf("cursor 7 of 10 in a 3-row panel:\n%s", panel)
	}
}
