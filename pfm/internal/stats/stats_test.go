package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func uintString(value uint64) string { return strconv.FormatUint(value, 10) }

func TestSamplerSampleLimitsSkipsLinuxResourceFiles(t *testing.T) {
	now := int64(123456789)
	sampler := &Sampler{
		ProcRoot: filepath.Join(t.TempDir(), "missing-proc"),
		Clock:    func() int64 { return now },
		Limits: NewLimitsSampler([]LimitAccount{{
			ID: 3, Label: "account 3", Absent: true,
		}}),
	}

	snapshot := sampler.SampleLimits()
	if !snapshot.Ready || snapshot.SampleTime != now {
		t.Fatalf("Limits snapshot = %#v, want ready snapshot at %d", snapshot, now)
	}
	if len(snapshot.Chats) != 0 || len(snapshot.Docker) != 0 {
		t.Fatalf("Limits snapshot unexpectedly sampled resources: %#v", snapshot)
	}
	if len(snapshot.Limits) != 1 || !snapshot.Limits[0].Absent || snapshot.Limits[0].Status != "account 3" {
		t.Fatalf("Limits snapshot lost account status: %#v", snapshot.Limits)
	}
}

func TestSamplerComputesInstantaneousTreeCPUFromTwoProcSamples(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return 1 }}
	rows := []compose.Row{{
		Kind: compose.LiveClaude, Socket: "probe-chat", Name: "worker", PanePIDs: []int{100},
	}}
	first, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if first.Header.CPUValid || len(first.Chats) != 1 || first.Chats[0].CPUValid {
		t.Fatalf("first delta sample = %#v", first)
	}
	writeStatsFixture(t, proc, 1200, 130)
	sampler.Clock = func() int64 { return 2_000_000_001 }
	second, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Chats[0].CPUValid || second.Chats[0].CPUPercent < 14.9 || second.Chats[0].CPUPercent > 15.1 {
		t.Fatalf("instantaneous chat CPU = %#v, want 15%%", second.Chats[0])
	}
	if second.Chats[0].RSSBytes == 0 || second.Header.RAMPercent != 50 || second.Header.SwapPercent != 25 {
		t.Fatalf("memory sample = %#v", second)
	}
}

func TestChatTreeIncludesTmuxServerAndMarksForeignDescendants(t *testing.T) {
	current := map[int]gather.ProcessCounter{
		50:  {Parent: 1, CPUTicks: 10, RSSBytes: 10, Command: "/usr/bin/tmux"},
		100: {Parent: 50, CPUTicks: 20, RSSBytes: 20, Command: "/opt/bin/claude"},
		101: {Parent: 100, CPUTicks: 30, RSSBytes: 30, Command: "/usr/bin/node"},
		102: {Parent: 100, CPUTicks: 40, RSSBytes: 40, Command: "/work/dev-server"},
	}
	rows := []compose.Row{{
		Kind: compose.LiveClaude, Socket: "probe-tree", Name: "worker", PanePIDs: []int{100},
	}}
	chats := chatTrees(rows, current, nil, 0, 1, 1000)
	if len(chats) != 1 || chats[0].RSSBytes != 100 || chats[0].GearCount != 2 {
		t.Fatalf("socket tree = %#v, want tmux+engine+children RSS and two foreign descendants", chats)
	}
}

func TestDockerCgroupSamplingNeedsNoDaemon(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	container := filepath.Join(cgroup, "system.slice", "docker-1234567890abcdef.scope")
	for _, directory := range []string{filepath.Join(proc, "pressure"), container} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeStatsFixture(t, proc, 1000, 100)
	writeDockerFixture(t, container, 1_000, 200, 1000)
	now := int64(1)
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return now }}
	first, err := sampler.Sample(nil)
	if err != nil || len(first.Docker) != 1 || first.Docker[0].CPUValid {
		t.Fatalf("first Docker sample=%#v err=%v", first.Docker, err)
	}
	now = 1_000_000_001
	writeStatsFixture(t, proc, 1100, 100)
	writeDockerFixture(t, container, 1_500, 250, 1000)
	second, err := sampler.Sample(nil)
	if err != nil || len(second.Docker) != 1 {
		t.Fatalf("second Docker sample=%#v err=%v", second.Docker, err)
	}
	got := second.Docker[0]
	if got.Name != "1234567890ab" || !got.CPUValid || got.CPUPercent != 0.05 ||
		got.MemoryBytes != 250 || got.MemoryPercent != 25 {
		t.Fatalf("Docker cgroup sample = %#v", got)
	}
}

func TestSamplerCountsClaudeAndCodexLifetimeTokensIncrementally(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)

	claudePath := filepath.Join(root, "claude.jsonl")
	codexPath := filepath.Join(root, "codex.jsonl")
	claude := `{"timestamp":"2026-08-16T10:00:00Z","type":"user","message":{"role":"user"}}` + "\n" + `{"timestamp":"2026-08-16T11:00:00Z","type":"assistant","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"output_tokens":400}}}` + "\n"
	codex := `{"timestamp":"2026-08-16T10:30:00Z","type":"session_meta","payload":{}}` + "\n" + `{"timestamp":"2026-08-16T11:30:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":9000}}}}` + "\n"
	if err := os.WriteFile(claudePath, []byte(claude), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(codex), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC).UnixNano()
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		Clock: func() int64 { return now },
	}
	rows := []compose.Row{
		{Kind: compose.LiveClaude, Socket: "claude-socket", Name: "claude", Path: claudePath, PanePIDs: []int{100}},
		{Kind: compose.LiveCodex, Socket: "codex-socket", Name: "codex", Path: codexPath, PanePIDs: []int{100}},
	}
	first, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	chats := chatsBySeat(first.Chats)
	if got := chats["claude-socket"]; !got.TokensKnown || got.TokenCount != 1000 ||
		got.TokenRateValid {
		t.Fatalf("Claude token accounting = %#v, want 1000 total and unknown first rate", got)
	}
	if got := chats["codex-socket"]; !got.TokensKnown || got.TokenCount != 9000 ||
		got.TokenRateValid {
		t.Fatalf("Codex token accounting = %#v, want 9000 total and unknown first rate", got)
	}

	appendFile(
		t,
		claudePath,
		`{"timestamp":"2026-08-16T12:00:00Z","type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":40}}}`+"\n",
	)
	appendFile(
		t,
		codexPath,
		`{"timestamp":"2026-08-16T12:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":12000}}}}`+"\n",
	)
	now = time.Date(2026, 8, 16, 12, 0, 30, 0, time.UTC).UnixNano()
	second, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	chats = chatsBySeat(second.Chats)
	if got := chats["claude-socket"]; got.TokenCount != 1100 || !got.TokenRateValid || got.TokensPerMinute != 200 {
		t.Fatalf("incremental Claude token accounting = %#v, want 1100 total and 200/min", got)
	}
	if got := chats["codex-socket"]; got.TokenCount != 12000 || !got.TokenRateValid || got.TokensPerMinute != 6000 {
		t.Fatalf("incremental Codex token accounting = %#v, want latest 12000 total and 6000/min", got)
	}
}

func TestSamplerUsageSparkTracksBurnDeltasResetsAndCap(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	transcript := filepath.Join(root, "spark.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"timestamp":"2026-08-16T10:00:00Z","type":"assistant","message":{"usage":{"input_tokens":100}}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC).UnixNano()
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		Clock: func() int64 { return now },
	}
	rows := []compose.Row{{
		Kind: compose.LiveClaude, Socket: "spark-socket", Name: "spark",
		Path: transcript, PanePIDs: []int{100},
	}}
	sample := func() Chat {
		t.Helper()
		snapshot, err := sampler.Sample(rows)
		if err != nil {
			t.Fatal(err)
		}
		return chatsBySeat(snapshot.Chats)["spark-socket"]
	}

	first := sample()
	if len(first.Spark) != 0 {
		t.Fatalf("first sample spark = %v, want empty until a second sample exists", first.Spark)
	}
	appendFile(t, transcript,
		`{"timestamp":"2026-08-16T12:01:00Z","type":"assistant","message":{"usage":{"input_tokens":150}}}`+"\n")
	now += int64(30 * time.Second)
	if got := sample(); len(got.Spark) != 1 || got.Spark[0] != 150 {
		t.Fatalf("growth sample spark = %v, want [150]", got.Spark)
	}
	now += int64(30 * time.Second)
	if got := sample(); len(got.Spark) != 2 || got.Spark[1] != 0 {
		t.Fatalf("idle sample spark = %v, want [150 0]", got.Spark)
	}
	for index := 0; index < 20; index++ {
		appendFile(t, transcript, fmt.Sprintf(
			`{"timestamp":"2026-08-16T12:%02d:00Z","type":"assistant","message":{"usage":{"input_tokens":10}}}`+"\n",
			index%60,
		))
		now += int64(30 * time.Second)
		got := sample()
		if len(got.Spark) > 12 {
			t.Fatalf("spark grew past its cap: %d points", len(got.Spark))
		}
	}
	last := sample()
	if len(last.Spark) != 12 || last.Spark[10] != 10 || last.Spark[11] != 0 {
		t.Fatalf(
			"capped sample spark = %v (len=%d), want 12 points with a burn then an idle step",
			last.Spark,
			len(last.Spark),
		)
	}

	if err := os.WriteFile(transcript, []byte(
		`{"timestamp":"2026-08-16T13:00:00Z","type":"assistant","message":{"usage":{"input_tokens":5}}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	now += int64(30 * time.Second)
	if got := sample(); len(got.Spark) != 0 {
		t.Fatalf("rewrite-discontinuity sample spark = %v, want an emptied chart", got.Spark)
	}
}

func TestSamplerLiveTokenRateStartsUnknownThenIdle(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	transcript := filepath.Join(root, "idle.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"timestamp":"2026-08-16T10:00:00Z","type":"assistant","message":{"usage":{"input_tokens":100}}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC).UnixNano()
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		Clock: func() int64 { return now },
	}
	rows := []compose.Row{{
		Kind: compose.LiveClaude, Socket: "idle-socket", Name: "idle",
		Path: transcript, PanePIDs: []int{100},
	}}
	first, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Chats[0]; got.TokenRateValid {
		t.Fatalf("first token sample = %#v, want unknown live rate", got)
	}

	now += int64(4 * time.Second)
	second, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Chats[0]; !got.TokenRateValid || got.TokensPerMinute != 0 {
		t.Fatalf("idle second token sample = %#v, want known zero live rate", got)
	}
}

func TestSamplerLiveTokenRateUsesRollingGrowthAndExpiresToIdle(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	transcript := filepath.Join(root, "active.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"timestamp":"2026-08-17T08:00:00Z","type":"assistant","message":{"usage":{"input_tokens":100}}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC).UnixNano()
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		Clock: func() int64 { return now },
	}
	rows := []compose.Row{{
		Kind: compose.LiveClaude, Socket: "active-socket", Name: "active",
		Path: transcript, PanePIDs: []int{100},
	}}
	if _, err := sampler.Sample(rows); err != nil {
		t.Fatal(err)
	}

	now += int64(8 * time.Second)
	appendFile(t, transcript,
		`{"timestamp":"2026-08-17T08:00:08Z","type":"assistant","message":{"usage":{"input_tokens":60}}}`+"\n")
	growing, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := growing.Chats[0]; !got.TokenRateValid || got.TokensPerMinute != 450 {
		t.Fatalf("growing token sample = %#v, want 450/min", got)
	}

	now += int64(32 * time.Second)
	if _, err := sampler.Sample(rows); err != nil {
		t.Fatal(err)
	}
	now += int64(32 * time.Second)
	idle, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := idle.Chats[0]; !got.TokenRateValid || got.TokensPerMinute != 0 {
		t.Fatalf("expired token growth = %#v, want known idle rate", got)
	}
}

func TestSamplerLiveTokenRateResetsOnTranscriptDiscontinuity(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	now := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC).UnixNano()
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		Clock: func() int64 { return now },
	}
	firstPath := filepath.Join(root, "first.jsonl")
	writeClaudeTokenFixture(t, firstPath, 100)
	rows := []compose.Row{{
		Kind: compose.LiveClaude, ID: "session-one", Socket: "reset-socket", Name: "reset",
		Path: firstPath, PanePIDs: []int{100},
	}}
	if _, err := sampler.Sample(rows); err != nil {
		t.Fatal(err)
	}
	now += int64(4 * time.Second)
	appendFile(t, firstPath,
		`{"timestamp":"2026-08-17T08:00:04Z","type":"assistant","message":{"usage":{"input_tokens":40}}}`+"\n")
	active, err := sampler.Sample(rows)
	if err != nil || !active.Chats[0].TokenRateValid || active.Chats[0].TokensPerMinute == 0 {
		t.Fatalf("pre-reset token sample = %#v err=%v", active.Chats, err)
	}

	now += int64(4 * time.Second)
	writeClaudeTokenFixture(t, firstPath, 20)
	truncated, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := truncated.Chats[0]; got.TokenCount != 20 || got.TokenRateValid {
		t.Fatalf("truncated transcript sample = %#v, want reset unknown rate", got)
	}

	now += int64(4 * time.Second)
	idleAfterTruncation, err := sampler.Sample(rows)
	if err != nil || !idleAfterTruncation.Chats[0].TokenRateValid {
		t.Fatalf("post-truncation baseline sample = %#v err=%v", idleAfterTruncation.Chats, err)
	}
	rows[0].ID = "session-two"
	now += int64(4 * time.Second)
	changedSession, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := changedSession.Chats[0]; got.TokenCount != 20 || got.TokenRateValid {
		t.Fatalf("changed-session sample = %#v, want reset unknown rate", got)
	}

	now += int64(4 * time.Second)
	secondPath := filepath.Join(root, "second.jsonl")
	writeClaudeTokenFixture(t, secondPath, 500)
	rows[0].Path = secondPath
	rotated, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := rotated.Chats[0]; got.TokenCount != 500 || got.TokenRateValid {
		t.Fatalf("rotated transcript sample = %#v, want reset unknown rate", got)
	}

	now += int64(4 * time.Second)
	if _, err := sampler.Sample(rows); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement.jsonl")
	writeClaudeTokenFixture(t, replacement, 900)
	if err := os.Rename(replacement, secondPath); err != nil {
		t.Fatal(err)
	}
	now += int64(4 * time.Second)
	replaced, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := replaced.Chats[0]; got.TokenCount != 900 || got.TokenRateValid {
		t.Fatalf("replaced transcript sample = %#v, want reset unknown rate", got)
	}

	now += int64(4 * time.Second)
	if _, err := sampler.Sample(nil); err != nil {
		t.Fatal(err)
	}
	now += int64(4 * time.Second)
	reappeared, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := reappeared.Chats[0]; got.TokenCount != 900 || got.TokenRateValid {
		t.Fatalf("reappeared pruned transcript sample = %#v, want reset unknown rate", got)
	}
}

// A live row and its transcript inventory are sampled independently. The
// transcript may disappear between those snapshots; that ordinary refresh
// race means "tokens unknown", not a warning painted across the Stats header.
func TestSamplerTreatsAMissingLiveTokenTranscriptAsRefreshChurn(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	missing := filepath.Join(root, "rollout-disappeared.jsonl")
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1}
	snapshot, err := sampler.Sample([]compose.Row{{
		Kind: compose.LiveCodex, Socket: "probe-missing", Name: "worker",
		Path: missing, PanePIDs: []int{100},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Warnings) != 0 {
		t.Fatalf("missing transcript leaked into Stats warnings: %v", snapshot.Warnings)
	}
	if len(snapshot.Chats) != 1 || snapshot.Chats[0].TokensKnown {
		t.Fatalf("missing transcript token state = %#v", snapshot.Chats)
	}
}

func TestDockerIdentityIsResolvedOncePerCgroupID(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	id := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	container := filepath.Join(cgroup, "system.slice", "docker-"+id+".scope")
	for _, directory := range []string{filepath.Join(proc, "pressure"), container} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeStatsFixture(t, proc, 1000, 100)
	writeDockerFixture(t, container, 1_000, 200, 1000)
	inspectCalls := 0
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		DockerInspect: func(gotID string) (string, string, error) {
			inspectCalls++
			if gotID != id {
				t.Fatalf("inspected container %q, want full cgroup ID %q", gotID, id)
			}
			return "professor-web", "registry.example/professor:web", nil
		},
	}
	for sample := 0; sample < 2; sample++ {
		snapshot, err := sampler.Sample(nil)
		if err != nil || len(snapshot.Docker) != 1 {
			t.Fatalf("Docker sample %d=%#v err=%v", sample, snapshot.Docker, err)
		}
		got := snapshot.Docker[0]
		if got.ID != id || got.Name != "professor-web" || got.Image != "registry.example/professor:web" {
			t.Fatalf("Docker identity = %#v", got)
		}
	}
	if inspectCalls != 1 {
		t.Fatalf("Docker identity lookup calls=%d, want one cached lookup", inspectCalls)
	}
}

func chatsBySeat(chats []Chat) map[string]Chat {
	result := make(map[string]Chat, len(chats))
	for index := range chats {
		chat := &chats[index]
		result[chat.Seat] = *chat
	}
	return result
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeTokenFixture(t *testing.T, path string, tokens int64) {
	t.Helper()
	content := `{"timestamp":"2026-08-17T08:00:00Z","type":"assistant","message":{"usage":{"input_tokens":` +
		strconv.FormatInt(tokens, 10) + `}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeDockerFixture(t *testing.T, directory string, usage, memory, limit uint64) {
	t.Helper()
	files := map[string]string{
		"cpu.stat":       "usage_usec " + uintString(usage) + "\n",
		"memory.current": uintString(memory) + "\n",
		"memory.max":     uintString(limit) + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeStatsFixture(t *testing.T, proc string, systemTicks, processTicks uint64) {
	t.Helper()
	files := map[string]string{
		"stat":         "cpu " + uintString(systemTicks/2) + " 0 0 " + uintString(systemTicks/2) + " 0 0 0 0\n",
		"meminfo":      "MemTotal: 1000 kB\nMemAvailable: 500 kB\nSwapTotal: 400 kB\nSwapFree: 300 kB\n",
		"pressure/cpu": "some avg10=7.25 avg60=0.00 avg300=0.00 total=1\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(proc, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(proc, "100")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	fields := []string{
		"S",
		"1",
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		uintString(processTicks),
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		"0",
		"1",
	}
	if err := os.WriteFile(
		filepath.Join(directory, "stat"),
		[]byte("100 (pane) "+strings.Join(fields, " ")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "statm"), []byte("10 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "cmdline"), []byte("bash\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Claude writes one transcript record per content block of an assistant
// message — text, thinking, each tool_use — and every record repeats the
// message's usage block verbatim. A live transcript measured 967 records for
// 388 messages: summing per record inflated the lifetime tokens column, and
// the per-minute rate derived from it, by 2.49×. Usage counts once per
// message id (requestId when the id is absent); records with neither keep
// counting individually, which is what every older fixture relies on.
func TestSamplerCountsAMultiRecordAssistantMessageOnce(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	cgroup := filepath.Join(root, "cgroup")
	if err := os.MkdirAll(filepath.Join(proc, "pressure"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cgroup, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)

	usageA := `"usage":{"input_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"output_tokens":400}`
	path := filepath.Join(root, "split.jsonl")
	transcript := strings.Join([]string{
		`{"timestamp":"2026-09-02T10:00:00Z","type":"assistant","requestId":"req_A","message":{"id":"msg_A","content":[{"type":"thinking"}],` + usageA + `}}`,
		`{"timestamp":"2026-09-02T10:00:01Z","type":"assistant","requestId":"req_A","message":{"id":"msg_A","content":[{"type":"text","text":"hi"}],` + usageA + `}}`,
		`{"timestamp":"2026-09-02T10:00:02Z","type":"assistant","requestId":"req_A","message":{"id":"msg_A","content":[{"type":"tool_use"}],` + usageA + `}}`,
		`{"timestamp":"2026-09-02T10:00:03Z","type":"user","message":{"role":"user","content":[{"type":"tool_result"}]}}`,
		`{"timestamp":"2026-09-02T10:00:05Z","type":"assistant","requestId":"req_B","message":{"id":"msg_B","content":[{"type":"text"}],"usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":40}}}`,
		`{"timestamp":"2026-09-02T10:00:06Z","type":"assistant","requestId":"req_C","message":{"content":[{"type":"text"}],"usage":{"output_tokens":5}}}`,
		`{"timestamp":"2026-09-02T10:00:07Z","type":"assistant","requestId":"req_C","message":{"content":[{"type":"tool_use"}],"usage":{"output_tokens":5}}}`,
		`{"timestamp":"2026-09-02T10:00:08Z","type":"assistant","message":{"usage":{"output_tokens":1}}}`,
		`{"timestamp":"2026-09-02T10:00:09Z","type":"assistant","message":{"usage":{"output_tokens":1}}}`,
		`{"timestamp":"not-a-time","type":"assistant","message":{"usage":{"output_tokens":3}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 2, 10, 1, 0, 0, time.UTC).UnixNano()
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1,
		Clock: func() int64 { return now },
	}
	rows := []compose.Row{
		{Kind: compose.LiveClaude, Socket: "split-socket", Name: "split", Path: path, PanePIDs: []int{100}},
	}
	snapshot, err := sampler.Sample(rows)
	if err != nil {
		t.Fatal(err)
	}
	// msg_A once (1000) + msg_B (100) + req_C once (5) + two id-less records (1 + 1) + a record whose
	// timestamp is no time (3): the timestamp is a label and never gates the usage.
	if got := chatsBySeat(snapshot.Chats)["split-socket"]; !got.TokensKnown || got.TokenCount != 1110 {
		t.Fatalf("split-message token accounting = %#v, want 1110 (each message counted once)", got)
	}
	if len(snapshot.Warnings) != 0 {
		t.Fatalf("an unparsable timestamp warned: %q", snapshot.Warnings)
	}
}

// A live OpenCode seat is a live chat for counting, and its usage belongs to
// OpenCode. The engine-name fallback returns CLAUDE for anything it does not
// recognise, so a missing arm here does not read as "unknown" — it reads as
// somebody else's chat.
func TestLiveOpenCodeCountsAsItsOwnEngine(t *testing.T) {
	if !liveKind(compose.LiveOpenCode) {
		t.Fatal("liveKind(LiveOpenCode)=false, want true")
	}
	want := pfmengine.MustLookup(pfmengine.OpenCode).LongName
	if got := statsEngineName(compose.LiveOpenCode); got != want {
		t.Fatalf("statsEngineName(LiveOpenCode)=%q, want %q", got, want)
	}
}

func newStatsJail(t *testing.T) (proc, cgroup string) {
	t.Helper()
	root := t.TempDir()
	proc, cgroup = filepath.Join(root, "proc"), filepath.Join(root, "cgroup")
	for _, directory := range []string{filepath.Join(proc, "pressure"), cgroup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return proc, cgroup
}

func writeStatsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A host counter that cannot be read leaves its column unknown with a warning
// naming it; the chats and containers beside it are still sampled, and a later
// good counter never diffs against the unreadable one.
func TestSamplerKeepsTheSampleWhenAHostCounterIsUnreadable(t *testing.T) {
	rows := []compose.Row{{Kind: compose.LiveClaude, Socket: "probe-chat", Name: "worker", PanePIDs: []int{100}}}
	for _, testCase := range []struct {
		name      string
		breakFile func(t *testing.T, proc string)
		warning   func(proc string) string
		check     func(t *testing.T, header Header)
	}{
		{
			name: "cpu counter",
			breakFile: func(t *testing.T, proc string) {
				writeStatsFile(t, filepath.Join(proc, "stat"), "cpu x 0 0 0 0\n")
			},
			warning: func(string) string {
				return `parse host CPU counter "x": strconv.ParseUint: parsing "x": invalid syntax`
			},
		},
		{
			name: "memory counters",
			breakFile: func(t *testing.T, proc string) {
				if err := os.Remove(filepath.Join(proc, "meminfo")); err != nil {
					t.Fatal(err)
				}
			},
			warning: func(proc string) string {
				return "read host memory counters: open " + filepath.Join(proc, "meminfo") + ": no such file or directory"
			},
			check: func(t *testing.T, header Header) {
				if header.MemoryBytes != 0 || header.PSIPercent != 7.25 || !header.RAMUnread || !header.SwapUnread {
					t.Fatalf("header = %#v, want memory and swap unread and the pressure still read", header)
				}
			},
		},
		{
			name: "available memory",
			breakFile: func(t *testing.T, proc string) {
				writeStatsFile(t, filepath.Join(proc, "meminfo"), "MemTotal: 1000 kB\nSwapTotal: 400 kB\nSwapFree: 300 kB\n")
			},
			warning: func(proc string) string {
				return "read host memory counters: no MemAvailable line in " + filepath.Join(proc, "meminfo")
			},
			check: func(t *testing.T, header Header) {
				if header.MemoryBytes != 1000*1024 || header.RAMPercent != 0 || !header.RAMUnread || header.SwapUnread ||
					header.SwapPercent != 25 {
					t.Fatalf("header = %#v, want RAM unread, never 100%% used, and swap still read", header)
				}
			},
		},
		{
			name: "pressure avg10",
			breakFile: func(t *testing.T, proc string) {
				writeStatsFile(t, filepath.Join(proc, "pressure", "cpu"), "some avg10=zz avg60=0.00\n")
			},
			warning: func(string) string {
				return `parse CPU pressure avg10: strconv.ParseFloat: parsing "zz": invalid syntax`
			},
			check: func(t *testing.T, header Header) {
				if !header.PSIUnavailable || header.RAMPercent != 50 || header.RAMUnread || header.SwapUnread {
					t.Fatalf("header = %#v, want PSI unavailable and memory read", header)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			proc, cgroup := newStatsJail(t)
			writeStatsFixture(t, proc, 1000, 100)
			testCase.breakFile(t, proc)
			now := int64(1)
			sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return now }}
			first, err := sampler.Sample(rows)
			if err != nil {
				t.Fatalf("sample failed: %v", err)
			}
			if want := testCase.warning(proc); len(first.Warnings) != 1 || first.Warnings[0] != want {
				t.Fatalf("warnings = %q, want [%q]", first.Warnings, want)
			}
			if len(first.Chats) != 1 || first.Chats[0].RSSBytes == 0 {
				t.Fatalf("chats = %#v, want the chat still sampled", first.Chats)
			}
			if testCase.check != nil {
				testCase.check(t, first.Header)
			}
			if testCase.name != "cpu counter" {
				return
			}
			// The next good counter has no baseline to diff against.
			writeStatsFixture(t, proc, 1200, 130)
			now = 2_000_000_001
			second, err := sampler.Sample(rows)
			if err != nil || second.Header.CPUValid || second.Chats[0].CPUValid {
				t.Fatalf("second sample = %#v err=%v, want CPU unknown without a baseline", second, err)
			}
			writeStatsFixture(t, proc, 1400, 160)
			now = 4_000_000_002
			third, err := sampler.Sample(rows)
			if err != nil || !third.Chats[0].CPUValid ||
				third.Chats[0].CPUPercent < 14.9 || third.Chats[0].CPUPercent > 15.1 {
				t.Fatalf("third sample = %#v err=%v, want 15%% chat CPU", third, err)
			}
		})
	}
}

// A process whose stat record will not parse stays in its chat's tree with
// unknown CPU; its memory still counts, and the warning names the loss. Its
// zero ticks are no reading: no CPU delta is told across either side of it.
func TestSamplerKeepsAProcessWhoseStatRecordWillNotParse(t *testing.T) {
	const torn = "100 (pane) S 1 0 0 0 0 0 0 0 0 0 zz 0 0\n"
	for _, testCase := range []struct {
		name         string
		firstTorn    bool
		secondTorn   bool
		wantWarnings int
	}{
		{name: "torn then whole", firstTorn: true, wantWarnings: 0},
		{name: "whole then torn", secondTorn: true, wantWarnings: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			proc, cgroup := newStatsJail(t)
			now := int64(1)
			sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return now }}
			rows := []compose.Row{
				{Kind: compose.LiveClaude, Socket: "probe-chat", Name: "worker", PanePIDs: []int{100}},
			}
			var snapshot Snapshot
			for index, sample := range []struct {
				at, system, process uint64
				torn                bool
			}{
				{at: 1, system: 1000, process: 100, torn: testCase.firstTorn},
				{at: 1_000_000_001, system: 2000, process: 300, torn: testCase.secondTorn},
			} {
				now = int64(sample.at)
				writeStatsFixture(t, proc, sample.system, sample.process)
				if sample.torn {
					writeStatsFile(t, filepath.Join(proc, "100", "stat"), torn)
				}
				var err error
				if snapshot, err = sampler.Sample(rows); err != nil {
					t.Fatalf("sample %d: %v", index, err)
				}
			}
			want := `parse process 100 user ticks: strconv.ParseUint: parsing "zz": invalid syntax; process kept with unknown CPU`
			if len(snapshot.Warnings) != testCase.wantWarnings ||
				(testCase.wantWarnings == 1 && snapshot.Warnings[0] != want) {
				t.Fatalf("warnings = %q, want %d of %q", snapshot.Warnings, testCase.wantWarnings, want)
			}
			if len(snapshot.Chats) != 1 || snapshot.Chats[0].NoProcess || snapshot.Chats[0].RSSBytes == 0 {
				t.Fatalf("chats = %#v, want the chat found with its memory", snapshot.Chats)
			}
			if snapshot.Chats[0].CPUValid {
				t.Fatalf("chat CPU = %v valid, want unknown across a torn record", snapshot.Chats[0].CPUPercent)
			}
		})
	}
}

// A container whose CPU counter cannot be read stays in the list with CPU
// unknown and its memory shown.
func TestDockerContainerKeepsItsRowWhenCPUStatIsUnreadable(t *testing.T) {
	proc, cgroup := newStatsJail(t)
	container := filepath.Join(cgroup, "system.slice", "docker-1234567890abcdef.scope")
	if err := os.MkdirAll(container, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	writeDockerFixture(t, container, 1_000, 250, 1000)
	if err := os.Remove(filepath.Join(container, "cpu.stat")); err != nil {
		t.Fatal(err)
	}
	now := int64(1)
	sampler := &Sampler{ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return now }}
	var second Snapshot
	for _, at := range []int64{1, 1_000_000_001} {
		now = at
		var err error
		if second, err = sampler.Sample(nil); err != nil {
			t.Fatal(err)
		}
	}
	want := "read Docker CPU 1234567890ab: open " + filepath.Join(container, "cpu.stat") + ": no such file or directory"
	if len(second.Warnings) != 1 || second.Warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", second.Warnings, want)
	}
	if len(second.Docker) != 1 || second.Docker[0].CPUValid || second.Docker[0].MemoryBytes != 250 {
		t.Fatalf("docker = %#v, want the container kept with CPU unknown", second.Docker)
	}
}

// A failed identity lookup is asked again once the retry spacing has passed,
// never memoized for the sampler's life and never repeated every sample.
func TestDockerIdentityFailureIsRetriedAfterTheSpacing(t *testing.T) {
	proc, cgroup := newStatsJail(t)
	id := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	container := filepath.Join(cgroup, "system.slice", "docker-"+id+".scope")
	if err := os.MkdirAll(container, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStatsFixture(t, proc, 1000, 100)
	writeDockerFixture(t, container, 1_000, 200, 1000)
	calls := 0
	now := int64(1)
	sampler := &Sampler{
		ProcRoot: proc, CgroupRoot: cgroup, CPUCount: 1, Clock: func() int64 { return now },
		DockerInspect: func(string) (string, string, error) {
			calls++
			if calls == 1 {
				return "", "", fmt.Errorf("daemon down")
			}
			return "professor-web", "registry.example/professor:web", nil
		},
	}
	steps := []struct {
		atNS    int64
		calls   int
		name    string
		warning string
	}{
		{1, 1, "1234567890ab", "resolve Docker identity 1234567890ab: daemon down"},
		{10 * int64(time.Second), 1, "1234567890ab", "resolve Docker identity 1234567890ab: daemon down"},
		{31 * int64(time.Second), 2, "professor-web", ""},
	}
	for _, step := range steps {
		now = step.atNS
		snapshot, err := sampler.Sample(nil)
		if err != nil || len(snapshot.Docker) != 1 || snapshot.Docker[0].Name != step.name || calls != step.calls {
			t.Fatalf("at %d: docker=%#v calls=%d err=%v, want name %q after %d calls",
				step.atNS, snapshot.Docker, calls, err, step.name, step.calls)
		}
		if (step.warning == "") != (len(snapshot.Warnings) == 0) ||
			(step.warning != "" && snapshot.Warnings[0] != step.warning) {
			t.Fatalf("at %d: warnings = %q, want %q", step.atNS, snapshot.Warnings, step.warning)
		}
	}
}

// Where the kernel keeps no host tick counter (macOS), the total is the wall
// clock in centiseconds times the CPU count, and idle is what the process
// table's summed CPU leaves of it.
func TestSynthesizedCPUFromTheProcessTable(t *testing.T) {
	processes := map[int]gather.ProcessCounter{10: {CPUTicks: 100}, 11: {CPUTicks: 30, CPUUnknown: true}}
	for _, tc := range []struct {
		now      int64
		cpuCount int
		want     gather.CPU
	}{
		{now: 1_000_000_000, cpuCount: 2, want: gather.CPU{Total: 200, Idle: 70}},
		{now: 1_000_000_000, cpuCount: 0, want: gather.CPU{Total: 100}},
	} {
		if got := synthesizedCPU(processes, tc.now, tc.cpuCount); got != tc.want {
			t.Errorf("synthesizedCPU(now %d, %d CPUs) = %+v, want %+v", tc.now, tc.cpuCount, got, tc.want)
		}
	}
}
