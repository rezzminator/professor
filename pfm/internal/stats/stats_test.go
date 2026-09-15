package stats

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/compose"
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
	current := map[int]processSample{
		50:  {parent: 1, jiffies: 10, rss: 10, command: "/usr/bin/tmux"},
		100: {parent: 50, jiffies: 20, rss: 20, command: "/opt/bin/claude"},
		101: {parent: 100, jiffies: 30, rss: 30, command: "/usr/bin/node"},
		102: {parent: 100, jiffies: 40, rss: 40, command: "/work/dev-server"},
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
	chats := chatsBySocket(first.Chats)
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
	chats = chatsBySocket(second.Chats)
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
		return chatsBySocket(snapshot.Chats)["spark-socket"]
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

func chatsBySocket(chats []Chat) map[string]Chat {
	result := make(map[string]Chat, len(chats))
	for index := range chats {
		chat := &chats[index]
		result[chat.Socket] = *chat
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
	// msg_A once (1000) + msg_B (100) + req_C once (5) + two id-less records (1 + 1).
	if got := chatsBySocket(snapshot.Chats)["split-socket"]; !got.TokensKnown || got.TokenCount != 1107 {
		t.Fatalf("split-message token accounting = %#v, want 1107 (each message counted once)", got)
	}
}
