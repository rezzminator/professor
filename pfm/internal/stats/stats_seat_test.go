package stats

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func statsJail(t *testing.T) (proc, cgroup string) {
	t.Helper()
	root := t.TempDir()
	proc, cgroup = filepath.Join(root, "proc"), filepath.Join(root, "cgroup")
	for _, directory := range []string{filepath.Join(proc, "pressure"), cgroup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeStatsFixture(t, proc, 1000, 100)
	return proc, cgroup
}

// Every Herdr pane shares its server's socket. Keyed by socket, the whole
// native fleet folded into one row: the first chat's name, nobody's process,
// and every transcript's tokens summed.
func TestHerdrSeatsOnOneServerSocketGetARowEach(t *testing.T) {
	proc, cgroup := statsJail(t)
	transcripts := t.TempDir()
	first, second := filepath.Join(transcripts, "first.jsonl"), filepath.Join(transcripts, "second.jsonl")
	writeClaudeTokenFixture(t, first, 100)
	writeClaudeTokenFixture(t, second, 7)
	rows := []compose.Row{
		{
			Kind: compose.LiveClaude, Backend: "herdr", Socket: "/jail/herdr.sock", PaneID: "w1:p1",
			Name: "first", Path: first, PanePIDs: []int{100},
		},
		{
			Kind: compose.LiveCodex, Backend: "herdr", Socket: "/jail/herdr.sock", PaneID: "w1:p2",
			Name: "second", Path: second,
		},
	}
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return 1 }}
	snapshot, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Chats) != 2 {
		t.Fatalf("chats=%#v, want one row per Herdr seat", snapshot.Chats)
	}
	byName := map[string]Chat{}
	for index := range snapshot.Chats {
		byName[snapshot.Chats[index].Name] = snapshot.Chats[index]
	}
	if byName["first"].TokenCount != 100 || byName["second"].TokenCount != 7 || byName["first"].RSSBytes == 0 {
		t.Fatalf("per-seat usage=%#v", byName)
	}
}

// A live seat with no process in the table has unknown resources; printing
// 0 B and 0.0% would read as an idle chat.
func TestSeatWithoutAProcessIsUnknownNotIdle(t *testing.T) {
	proc, cgroup := statsJail(t)
	rows := []compose.Row{
		{Kind: compose.LiveClaude, Socket: "cc-found", Name: "found", PanePIDs: []int{100}},
		{Kind: compose.LiveClaude, Socket: "cc-gone", Name: "gone", PanePIDs: []int{999}},
		{Kind: compose.LiveClaude, Socket: "cc-none", Name: "none"},
		{
			Kind: compose.LiveClaude, Backend: "herdr", Socket: "/run/herdr.sock", PaneID: "w:1", Name: "unproved",
			BackendError: "HERDR ERROR /run/herdr.sock: pane.list refused",
		},
	}
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return 1 }}
	if _, err := sampler.Sample(rows); err != nil {
		t.Fatal(err)
	}
	sampler.Clock = func() int64 { return 2_000_000_001 }
	writeStatsFixture(t, proc, 1200, 130)
	snapshot, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	chats := chatsBySeat(snapshot.Chats)
	if chats["cc-found"].NoProcess || !chats["cc-found"].CPUValid {
		t.Fatalf("found seat=%#v", chats["cc-found"])
	}
	for _, seat := range []string{"cc-gone", "cc-none"} {
		if !chats[seat].NoProcess || chats[seat].CPUValid || chats[seat].ProbeFailed {
			t.Fatalf("%s=%#v, want NoProcess with no CPU reading", seat, chats[seat])
		}
	}
	// A seat whose probe failed is unread, never the same "no process" fact.
	if unproved := chats[compose.LiveSeatKey(rows[3])]; !unproved.NoProcess || !unproved.ProbeFailed {
		t.Fatalf("unproved seat=%#v, want ProbeFailed", unproved)
	}
}

// A kernel without PSI has no /proc/pressure/cpu; that is one unavailable
// header column, never a failed sample of the whole tab.
func TestSamplerWithoutCPUPressureStillSamples(t *testing.T) {
	proc, cgroup := statsJail(t)
	if err := os.Remove(filepath.Join(proc, "pressure", "cpu")); err != nil {
		t.Fatal(err)
	}
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return 1 }}
	snapshot, err := sampler.Sample(nil)
	if err != nil {
		t.Fatalf("sample without PSI failed: %v", err)
	}
	if !snapshot.Header.PSIUnavailable || snapshot.Header.RAMPercent != 50 {
		t.Fatalf("header=%#v, want PSI unavailable and RAM still read", snapshot.Header)
	}
}

// docker stats reports an unlimited container against host RAM; 0.0% read
// as an empty container.
func TestUnlimitedContainerMemoryIsAShareOfHostRAM(t *testing.T) {
	proc, cgroup := statsJail(t)
	container := filepath.Join(cgroup, "system.slice", "docker-abcdef0123456789.scope")
	if err := os.MkdirAll(container, 0o700); err != nil {
		t.Fatal(err)
	}
	writeDockerFixture(t, container, 1_000, 102_400, 0)
	if err := os.WriteFile(filepath.Join(container, "memory.max"), []byte("max\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return 1 }}
	snapshot, err := sampler.Sample(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Docker) != 1 || snapshot.Docker[0].LimitBytes != 0 || snapshot.Docker[0].MemoryPercent != 10 {
		t.Fatalf("docker=%#v, want 10%% of the 1000 kB host", snapshot.Docker)
	}
}
