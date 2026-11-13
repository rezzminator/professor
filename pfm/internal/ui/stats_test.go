package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	claudeengine "github.com/rezzminator/professor/pfm/internal/engine/claude"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/theme"
)

type countingStatsSampler struct {
	calls     int
	snapshots []pfmstats.Snapshot
}

type splitStatsSampler struct {
	fullCalls     int
	resourceCalls int
	limitCalls    int
}

type switchingStatsSampler struct {
	fullStarted      chan struct{}
	fullRelease      chan struct{}
	resourceStarted  chan struct{}
	resourceRelease  chan struct{}
	fullSnapshot     pfmstats.Snapshot
	resourceSnapshot pfmstats.Snapshot
}

func (sampler *switchingStatsSampler) Sample(_ []compose.Row) (pfmstats.Snapshot, error) {
	select {
	case sampler.fullStarted <- struct{}{}:
	default:
	}
	<-sampler.fullRelease
	return sampler.fullSnapshot, nil
}

func (sampler *switchingStatsSampler) SampleResources(_ []compose.Row) (pfmstats.Snapshot, error) {
	select {
	case sampler.resourceStarted <- struct{}{}:
	default:
	}
	<-sampler.resourceRelease
	return sampler.resourceSnapshot, nil
}

func waitForSample(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("sample command did not enter its in-flight gate")
	}
}

func runStatsCommand(t *testing.T, command tea.Cmd) <-chan tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("tab switch returned no replacement stats command")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- command() }()
	return result
}

func statsMessage(t *testing.T, messages <-chan tea.Msg) statsSampleMsg {
	t.Helper()
	select {
	case message := <-messages:
		result, ok := message.(statsSampleMsg)
		if !ok {
			t.Fatalf("stats command returned %T", message)
		}
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("stats command did not complete")
		return statsSampleMsg{}
	}
}

func TestRapidLimitsToStatsDropsInFlightLimitsResult(t *testing.T) {
	oldResources := pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{{Seat: "old-resource"}}}
	sampler := &switchingStatsSampler{
		fullStarted:     make(chan struct{}, 1),
		fullRelease:     make(chan struct{}),
		resourceStarted: make(chan struct{}, 1),
		resourceRelease: make(chan struct{}),
		fullSnapshot:    pfmstats.Snapshot{Ready: true, Limits: []pfmstats.AccountLimits{{Account: 99}}},
		resourceSnapshot: pfmstats.Snapshot{
			Ready: true, Chats: []pfmstats.Chat{{Seat: "new-resource"}},
		},
	}
	snapshot := fixtureSnapshot(120)
	snapshot.StatsSampler = sampler
	model := NewModel(snapshot)
	model.stats = oldResources

	model, _ = applyKey(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	model, limitsCommand := applyKey(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	limitsResult := runStatsCommand(t, limitsCommand)
	waitForSample(t, sampler.fullStarted)
	model, statsCommand := applyKey(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.Tab() != TabStats {
		t.Fatalf("rapid Limits→Stats landed on tab %d", model.Tab())
	}
	close(sampler.fullRelease)
	stale := statsMessage(t, limitsResult)
	updated, _ := model.Update(stale)
	model = updated.(Model)
	if len(model.stats.Limits) != 0 || len(model.stats.Chats) != 1 || model.stats.Chats[0].Seat != "old-resource" {
		t.Fatalf("stale Limits result overwrote Stats resources: %#v", model.stats)
	}
	resourceResult := runStatsCommand(t, statsCommand)
	close(sampler.resourceRelease)
	resourceMessage := statsMessage(t, resourceResult)
	updated, _ = model.Update(resourceMessage)
	model = updated.(Model)
	if len(model.stats.Chats) != 1 || model.stats.Chats[0].Seat != "new-resource" {
		t.Fatalf("fresh Stats resource result = %#v", model.stats)
	}
}

func TestRapidStatsToLimitsDropsInFlightResourceResult(t *testing.T) {
	oldLimits := pfmstats.Snapshot{Ready: true, Limits: []pfmstats.AccountLimits{{Account: 7}}}
	sampler := &switchingStatsSampler{
		fullStarted:     make(chan struct{}, 1),
		fullRelease:     make(chan struct{}),
		resourceStarted: make(chan struct{}, 1),
		resourceRelease: make(chan struct{}),
		fullSnapshot:    pfmstats.Snapshot{Ready: true, Limits: []pfmstats.AccountLimits{{Account: 8}}},
		resourceSnapshot: pfmstats.Snapshot{
			Ready: true, Chats: []pfmstats.Chat{{Seat: "stale-resource"}},
		},
	}
	snapshot := fixtureSnapshot(120)
	snapshot.StatsSampler = sampler
	model := NewModel(snapshot)
	model.stats = oldLimits

	model, resourceCommand := applyKey(t, model, specialKey(tea.KeyTab))
	resourceResult := runStatsCommand(t, resourceCommand)
	waitForSample(t, sampler.resourceStarted)
	model, limitsCommand := applyKey(t, model, specialKey(tea.KeyTab))
	if model.Tab() != TabLimits {
		t.Fatalf("rapid Stats→Limits landed on tab %d", model.Tab())
	}
	close(sampler.resourceRelease)
	stale := statsMessage(t, resourceResult)
	updated, _ := model.Update(stale)
	model = updated.(Model)
	if len(model.stats.Limits) != 1 || model.stats.Limits[0].Account != 7 || len(model.stats.Chats) != 0 {
		t.Fatalf("stale Stats result overwrote Limits: %#v", model.stats)
	}
	fullResult := runStatsCommand(t, limitsCommand)
	close(sampler.fullRelease)
	fullMessage := statsMessage(t, fullResult)
	updated, _ = model.Update(fullMessage)
	model = updated.(Model)
	if len(model.stats.Limits) != 1 || model.stats.Limits[0].Account != 8 {
		t.Fatalf("fresh Limits result = %#v", model.stats)
	}
}

func (sampler *splitStatsSampler) Sample(_ []compose.Row) (pfmstats.Snapshot, error) {
	sampler.fullCalls++
	return pfmstats.Snapshot{}, errors.New("limits fetch blocked the resource sample")
}

func (sampler *splitStatsSampler) SampleResources(_ []compose.Row) (pfmstats.Snapshot, error) {
	sampler.resourceCalls++
	return pfmstats.Snapshot{Ready: true}, nil
}

func (sampler *splitStatsSampler) SampleLimits() pfmstats.Snapshot {
	sampler.limitCalls++
	return pfmstats.Snapshot{
		Ready:  true,
		Limits: []pfmstats.AccountLimits{{Account: 4, Status: "credential rejected"}},
	}
}

func TestStatsTabSamplesResourcesWithoutWaitingForLimits(t *testing.T) {
	sampler := &splitStatsSampler{}
	snapshot := fixtureSnapshot(120)
	snapshot.StatsSampler = sampler
	model := NewModel(snapshot)

	_, command := applyKey(t, model, specialKey(tea.KeyTab))
	if command == nil {
		t.Fatal("entering Stats returned no sampling command")
	}
	message, ok := command().(statsSampleMsg)
	if !ok {
		t.Fatalf("Stats command returned %T", message)
	}
	if message.err != nil || sampler.resourceCalls != 1 || sampler.fullCalls != 0 {
		t.Fatalf(
			"Stats sample err=%v resource=%d full=%d; resource sampling must not wait for limits",
			message.err,
			sampler.resourceCalls,
			sampler.fullCalls,
		)
	}
}

func TestLimitsTabSamplesLimitsWithoutReadingResources(t *testing.T) {
	sampler := &splitStatsSampler{}
	snapshot := fixtureSnapshot(120)
	snapshot.StatsSampler = sampler
	model := NewModel(snapshot)

	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	_, command := applyKey(t, model, specialKey(tea.KeyTab))
	if command == nil {
		t.Fatal("entering Limits returned no sampling command")
	}
	message, ok := command().(statsSampleMsg)
	if !ok {
		t.Fatalf("Limits command returned %T", message)
	}
	if message.err != nil || sampler.limitCalls != 1 || sampler.fullCalls != 0 || sampler.resourceCalls != 0 {
		t.Fatalf(
			"Limits sample err=%v limits=%d full=%d resource=%d; limits sampling must not read resources",
			message.err,
			sampler.limitCalls,
			sampler.fullCalls,
			sampler.resourceCalls,
		)
	}
	if !message.snapshot.Ready || len(message.snapshot.Limits) != 1 ||
		message.snapshot.Limits[0].Status != "credential rejected" {
		t.Fatalf("Limits sample lost truthful account status: %#v", message.snapshot)
	}
}

func (sampler *countingStatsSampler) Sample(_ []compose.Row) (pfmstats.Snapshot, error) {
	index := sampler.calls
	sampler.calls++
	if index >= len(sampler.snapshots) {
		index = len(sampler.snapshots) - 1
	}
	return sampler.snapshots[index], nil
}

func TestStatsSamplerIsLazyAndStopsAfterLeaving(t *testing.T) {
	sampler := &countingStatsSampler{snapshots: []pfmstats.Snapshot{{}}}
	snapshot := fixtureSnapshot(120)
	snapshot.StatsSampler = sampler
	model := NewModel(snapshot)
	if sampler.calls != 0 {
		t.Fatalf("picker boot sampled stats: calls=%d", sampler.calls)
	}

	model, command := applyKey(t, model, specialKey(tea.KeyTab))
	if command == nil || sampler.calls != 0 {
		t.Fatalf("Stats focus command=%v calls=%d", command, sampler.calls)
	}
	message := command()
	if sampler.calls != 1 {
		t.Fatalf("first Stats focus sampled %d times, want 1", sampler.calls)
	}
	updated, _ := model.Update(message)
	model = updated.(Model)
	model, _ = applyKey(t, model, tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	updated, command = model.Update(statsTickMsg{generation: model.statsGeneration})
	model = updated.(Model)
	if command != nil || sampler.calls != 1 || model.Tab() != TabChats {
		t.Fatalf("sampling continued after leave: command=%v calls=%d tab=%d", command, sampler.calls, model.Tab())
	}
}

func TestLimitsPanelRendersEveryCardState(t *testing.T) {
	now := time.Unix(0, fixtureNowNS)
	home := testjail.Place("/home/test")
	label := func(id int) string {
		return pfmconfig.DisplayAccountDir(home, id, pfmconfig.DefaultAccountDir(home, id))
	}
	emoji1 := pfmconfig.Defaults(
		home, []string{filepath.Join(pfmconfig.DefaultAccountDir(home, 1), "projects")},
	).EmojiFor(1)
	var out strings.Builder
	for _, test := range []struct {
		name          string
		width, height int
		limits        []pfmstats.AccountLimits
	}{
		{"account windows", 120, 8, []pfmstats.AccountLimits{{
			Account: 2, Emoji: "🔹",
			Windows: []pfmstats.Window{{Name: "7d-fable", UsedPct: 23, ResetAt: time.Unix(1_800_000_000, 0)}},
		}}},
		{"fancy cards refresh past resets", 120, 10, []pfmstats.AccountLimits{{
			Account: 1, Emoji: "🥇", Engine: pfmengine.Claude,
			Windows: []pfmstats.Window{
				{Name: "5h", UsedPct: 52, ResetAt: now.Add(90 * time.Minute)},
				{Name: "7d", UsedPct: 100, ResetAt: now.Add(-time.Minute)},
			},
		}}},
		{"cached windows under a refresh warning", 120, 9, []pfmstats.AccountLimits{{
			Account: 2, Emoji: "🥈", Engine: "claude", ConfirmedAt: now.Add(-7 * time.Minute),
			Status:  "refresh timed out; showing cached limits",
			Windows: []pfmstats.Window{{Name: "7d", UsedPct: 73, ResetAt: now.Add(4 * 24 * time.Hour)}},
		}}},
		{"unsupported engine omitted", 120, 10, []pfmstats.AccountLimits{
			{Account: 1, Engine: pfmengine.Claude, Windows: []pfmstats.Window{{Name: "5h", UsedPct: 5}}},
			{Account: 1, Engine: pfmengine.OpenCode, Unsupported: true, Status: "engine ox: no usage source registered"},
		}},
		{"codex shows its first window only", 120, 10, []pfmstats.AccountLimits{{
			Account: 1, Engine: pfmengine.Codex, Label: "Codex 1",
			Windows: []pfmstats.Window{
				{Name: "7d", UsedPct: 2}, {Name: "7d-reserve"}, {Name: "5h-spark"}, {Name: "7d-spark"},
			},
		}}},
		{"no provider max totals", 120, 10, []pfmstats.AccountLimits{
			{Account: 1, Engine: pfmengine.Claude, Windows: []pfmstats.Window{{Name: "5h", UsedPct: 52}}},
			{Account: 2, Engine: pfmengine.Claude, Windows: []pfmstats.Window{{Name: "5h", UsedPct: 75}}},
		}},
		{"engine absences are two dim lines", 120, 8, []pfmstats.AccountLimits{
			{Engine: pfmengine.Claude, Label: "no Claude accounts configured", Status: "no Claude accounts configured", Absent: true},
			{Engine: pfmengine.Codex, Label: "no Codex accounts configured", Status: "no Codex accounts configured", Absent: true},
		}},
		{"skips consolidated, unknowns dropped, errors kept", 140, 18, []pfmstats.AccountLimits{
			{
				Account: 1, Emoji: emoji1, Engine: pfmengine.Claude, Label: label(1),
				Windows: []pfmstats.Window{
					{Name: "7d-fable", ResetNote: "reset unavailable"},
					{Name: "unknown[seven_day_nimbus_quill]", ResetNote: "reset unavailable"},
				},
			},
			{Account: 3, Engine: pfmengine.Claude, Label: label(3), Status: "skipped " + label(3) + ": credentials rejected"},
			{Account: 4, Engine: pfmengine.Claude, Label: label(4), Status: "skipped " + label(4) + ": no valid credentials"},
			{Engine: pfmengine.Codex, Label: "Codex", Status: "Codex credential rejected (HTTP 401)"},
		}},
		{"a passed reset shows its note, not a stale bar", 120, 10, []pfmstats.AccountLimits{{
			Account: 1, Emoji: "🥇", Engine: pfmengine.Claude,
			Windows: []pfmstats.Window{
				{Name: "5h", UsedPct: pfmstats.UnknownUsedPct, ResetNote: "reset passed · awaiting refetch"},
				{Name: "7d", UsedPct: 61, ResetAt: now.Add(3 * 24 * time.Hour)},
			},
		}}},
	} {
		model := NewModel(fixtureSnapshot(test.width))
		model.tab = TabLimits
		model.stats = pfmstats.Snapshot{Limits: test.limits}
		out.WriteString("## " + test.name + "\n" + ansi.Strip(model.renderLimitsPanel(test.width, test.height)) + "\n")
	}
	golden(t, "ui_limits_states.txt", out.String())
}

func TestLimitsTabScrollsToAccountsBelowTheViewport(t *testing.T) {
	model := NewModel(fixtureSnapshot(80))
	model.tab = TabLimits
	model.height = 12
	model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{
		{Account: 1, Engine: pfmengine.Claude, Label: "Claude 1", Windows: []pfmstats.Window{{Name: "5h", UsedPct: 1}}},
		{Account: 2, Engine: pfmengine.Claude, Label: "Claude 2", Windows: []pfmstats.Window{{Name: "7d", UsedPct: 2}}},
		{
			Account: 1,
			Engine:  pfmengine.Codex,
			Label:   "Codex 1",
			Windows: []pfmstats.Window{{Name: "5h-spark", UsedPct: 3}},
		},
	}}
	before := ansi.Strip(model.renderLimitsPanel(80, 6))
	if strings.Contains(before, "Codex 1") {
		t.Fatalf("setup did not place Codex below the viewport:\n%s", before)
	}
	model, command := applyKey(t, model, specialKey(tea.KeyEnd))
	if command != nil || model.limitsOffset == 0 {
		t.Fatalf("End did not scroll Limits: command=%v offset=%d", command, model.limitsOffset)
	}
	after := ansi.Strip(model.renderLimitsPanel(80, 6))
	if !strings.Contains(after, "Codex 1") || !strings.Contains(after, "5h-spark") {
		t.Fatalf("Codex remained unreachable after scrolling:\n%s", after)
	}
}

func TestLimitsTabScrollsAcrossClaudeCodexAndOpenCodeCardsAfterResize(t *testing.T) {
	model := NewModel(fixtureSnapshot(50))
	model.tab = TabLimits
	model.height = 12
	model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{
		{Account: 1, Engine: pfmengine.Claude, Label: "Claude 1", Windows: []pfmstats.Window{{Name: "5h", UsedPct: 1}}},
		{Account: 2, Engine: pfmengine.Claude, Label: "Claude 2", Windows: []pfmstats.Window{{Name: "7d", UsedPct: 2}}},
		{Account: 1, Engine: pfmengine.Codex, Label: "Codex 1", Windows: []pfmstats.Window{{Name: "5h", UsedPct: 3}}},
		{Account: 2, Engine: pfmengine.Codex, Label: "Codex 2", Windows: []pfmstats.Window{{Name: "7d", UsedPct: 4}}},
		{
			Account: 1,
			Engine:  pfmengine.OpenCode,
			Label:   "OpenCode 1",
			Windows: []pfmstats.Window{{Name: "5h", UsedPct: 5}},
		},
		{
			Account: 2,
			Engine:  pfmengine.OpenCode,
			Label:   "OpenCode 2",
			Windows: []pfmstats.Window{{Name: "7d", UsedPct: 6}},
		},
	}}
	if before := ansi.Strip(model.renderLimitsPanel(50, 6)); strings.Contains(before, "OpenCode 2") {
		t.Fatalf("tiny viewport unexpectedly showed the final card:\n%s", before)
	}
	model, command := applyKey(t, model, specialKey(tea.KeyEnd))
	if command != nil || model.limitsOffset == 0 {
		t.Fatalf(
			"End did not reach the bottom of the tiny Limits viewport: command=%v offset=%d",
			command,
			model.limitsOffset,
		)
	}
	bottom := ansi.Strip(model.renderLimitsPanel(50, 6))
	if !strings.Contains(bottom, "OpenCode") || !strings.Contains(bottom, " 6%") || !strings.Contains(bottom, "7d") {
		t.Fatalf("bottom of Limits cards was unreachable:\n%s", bottom)
	}

	model.height = 30
	model, command = applyKey(t, model, specialKey(tea.KeyHome))
	if command != nil || model.limitsOffset != 0 {
		t.Fatalf("Home after resize did not reset Limits offset: command=%v offset=%d", command, model.limitsOffset)
	}
	wide := ansi.Strip(model.renderLimitsPanel(80, 30))
	for _, label := range []string{"Claude", "Codex", "OpenCode", " 1% used", " 4% used", " 6% used"} {
		if !strings.Contains(wide, label) {
			t.Fatalf("resized Limits viewport lost %q:\n%s", label, wide)
		}
	}
}

func TestLimitsTabRendersRuleAboveHeader(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	model.tab = TabLimits
	model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{{
		Account: 1, Emoji: "🥇", Engine: pfmengine.Claude,
		Windows: []pfmstats.Window{{Name: "5h", UsedPct: 5}},
	}}}
	plain := ansi.Strip(model.renderLimitsPanel(120, 10))
	ruleAt, headerAt := -1, -1
	for i, line := range strings.Split(plain, "\n") {
		if ruleAt == -1 && strings.Contains(line, "──") {
			ruleAt = i
		}
		if headerAt == -1 && strings.Contains(line, "account 1") {
			headerAt = i
		}
	}
	if ruleAt == -1 || headerAt == -1 || ruleAt > headerAt {
		t.Fatalf("rule (line %d) must sit above its header (line %d):\n%s", ruleAt, headerAt, plain)
	}
}

func TestLimitBarsRoundToWholeFontGlyphsAndFullTail(t *testing.T) {
	_ = NewModel(fixtureSnapshot(120))
	left := ansi.Strip(limitBar(52.4, 20))
	right := ansi.Strip(limitBar(55, 20))
	if left != "[▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱]" || right != "[▰▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱]" {
		t.Fatalf("bars did not round 52.4%% and 55%% to whole cells: %q vs %q", left, right)
	}
	// Block elements (U+2580–U+259F) are custom glyphs in VS Code's WebGL
	// terminal and rendered as stale/blank cells; the bar must never use one.
	for _, bar := range []string{left, right, ansi.Strip(limitBar(100, 20))} {
		for _, r := range bar {
			if r >= 0x2580 && r <= 0x259F {
				t.Fatalf("bar %q uses block element %U", bar, r)
			}
		}
	}
	full := ansi.Strip(limitBar(100, 20))
	if !strings.Contains(full, "FULL]") {
		t.Fatalf("full bar=%q, want FULL tail", full)
	}
}

func TestLimitPercentageUsesStableHighContrastForeground(t *testing.T) {
	palette := theme.Load("default")
	configureStyles(palette)
	t.Cleanup(func() { configureStyles(theme.Load("default")) })
	line := renderLimitWindow(time.Unix(0, 0), pfmstats.Window{Name: "5h", UsedPct: 4}, 80)
	want := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Header)).
		Render("  4% used")
	if !strings.Contains(line, want) {
		t.Fatalf("percentage does not use stable high-contrast foreground:\n%q\nwant fragment %q", line, want)
	}
	narrow := ansi.Strip(renderLimitWindow(time.Unix(0, 0), pfmstats.Window{Name: "5h", UsedPct: 4}, 38))
	if !strings.Contains(narrow, "4% used") {
		t.Fatalf("narrow percentage lost semantic used label: %q", narrow)
	}
	if got := lipgloss.Width(narrow); got != 38 {
		t.Fatalf("narrow percentage line width=%d, want 38: %q", got, narrow)
	}
}

func TestLimitsTabNarrowRowsDropResetAndNeverWrap(t *testing.T) {
	model := NewModel(fixtureSnapshot(50))
	model.tab = TabLimits
	now := time.Unix(0, model.nowNS)
	model.stats = pfmstats.Snapshot{Limits: []pfmstats.AccountLimits{{
		Account: 1, Emoji: "🥇", Engine: pfmengine.Claude,
		Windows: []pfmstats.Window{{Name: "7d-fable", UsedPct: 52.4, ResetAt: now.Add(14 * time.Minute)}},
	}}}
	panel := model.renderLimitsPanel(50, 8)
	if strings.Contains(ansi.Strip(panel), "↻") {
		t.Fatalf("narrow Limits panel kept reset column:\n%s", ansi.Strip(panel))
	}
	for index, line := range strings.Split(panel, "\n") {
		if got := lipgloss.Width(line); got != 50 {
			t.Fatalf("narrow Limits line %d width=%d, want 50: %q", index, got, line)
		}
	}
}

// A 429 sampled end to end — the stats door records the shared backoff, the
// sampler words the status, renderLimitCards draws the card — says "limits
// unavailable" at most once and keeps the retry time on the card at a wide
// pane and a narrow one. The status used to wrap the backoff message in a
// second "limits unavailable:", which pushed the retry time past a narrow
// pane's edge.
func TestRateLimitedLimitCardSaysUnavailableOnceAndKeepsTheRetryTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	configDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credentials := `{"claudeAiOauth":{"accessToken":"fixture-oauth-token-not-a-real-secret"}}`
	if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}
	// cmd/pfm wires each engine's usage source; the real Claude one is wired
	// here so the card comes out of the same path a running picker takes.
	if _, err := pfmstats.UsageSourceFor(pfmengine.Claude); err != nil {
		pfmstats.RegisterUsageSource(pfmengine.Claude, claudeengine.UsageSource{})
	}
	now := time.Unix(1_800_000_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	sampler := pfmstats.NewLimitsSampler([]pfmstats.LimitAccount{
		{ID: 6, Engine: pfmengine.Claude, Label: "account 6", ConfigDir: configDir},
	})
	sampler.Endpoint = server.URL
	sampler.Now = func() time.Time { return now }
	limits, _ := sampler.Sample(context.Background())
	if len(limits) != 1 {
		t.Fatalf("limits=%#v, want one card", limits)
	}
	utcClock(t)
	model := NewModel(fixtureSnapshot(120))
	model.stats = pfmstats.Snapshot{Limits: limits}
	golden(t, "ui_limits_rate_limited.txt", ansi.Strip(strings.Join(model.renderLimitCards(118), "\n")+"\n"+
		strings.Join(model.renderLimitCards(50), "\n")))
}

func TestUsageSparkScalesToTheBusiestSample(t *testing.T) {
	cases := []struct {
		name   string
		deltas []int64
		want   string
	}{
		{"no history yet", nil, "…"},
		{"all idle", []int64{0, 0, 0}, "___"},
		{"burst after idle", []int64{0, 100}, "_¯"},
		{"mixed", []int64{50, 100, 25}, "⎻¯⎽"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := usageSpark(testCase.deltas); got != testCase.want {
				t.Fatalf("usageSpark(%v) = %q, want %q", testCase.deltas, got, testCase.want)
			}
		})
	}
}

func TestStatsCursorFollowsSeatAcrossCPUSort(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	model.tab = TabStats
	model.statsSort = StatsSortCPU
	model.applyStats(pfmstats.Snapshot{Chats: []pfmstats.Chat{
		{Seat: "socket-a", Name: "A", CPUPercent: 10, CPUValid: true},
		{Seat: "socket-b", Name: "B", CPUPercent: 20, CPUValid: true},
	}})
	if model.selectedStatsKey() != "socket-b" {
		t.Fatalf("initial selected stats key = %q", model.selectedStatsKey())
	}
	model.applyStats(pfmstats.Snapshot{Chats: []pfmstats.Chat{
		{Seat: "socket-a", Name: "A", CPUPercent: 30, CPUValid: true},
		{Seat: "socket-b", Name: "B", CPUPercent: 5, CPUValid: true},
	}})
	if model.selectedStatsKey() != "socket-b" || model.statsCursor != 1 {
		t.Fatalf("selected=%q cursor=%d after resort", model.selectedStatsKey(), model.statsCursor)
	}
}

func TestStatsHeaderRendersSwapSeparate(t *testing.T) {
	model := NewModel(fixtureSnapshot(120))
	model.tab = TabStats
	model.stats = pfmstats.Snapshot{Ready: true, Header: pfmstats.Header{
		CPUPercent: 12, CPUValid: true, PSIPercent: 34,
		RAMPercent: 56, SwapPercent: 78,
	}}
	if line, want := ansi.Strip(
		model.renderStatsHeader(120),
	), " CPU 12% · PSI 34% · RAM 56% · SWAP 78%                                                                                 "; line != want {
		t.Fatalf("stats header = %q, want %q", line, want)
	}
}

func TestStatsPanelsRenderEveryColumnInItsColour(t *testing.T) {
	model := NewModel(fixtureSnapshot(140))
	model.tab = TabStats
	model.stats = pfmstats.Snapshot{
		Ready: true,
		Chats: []pfmstats.Chat{{
			Name: "BUILD:one", Engine: "claude", CPUPercent: 12.5, CPUValid: true,
			RSSBytes: 2 << 20, RAMPercent: 3.5, TokenCount: 1_250_000,
			TokensKnown: true, TokensPerMinute: 625_000, TokenRateValid: true, GearCount: 2,
		}},
		Docker: []pfmstats.Container{{
			Name: "professor-web", Image: "registry.example/professor:web",
			CPUPercent: 4.5, CPUValid: true, MemoryBytes: 128 << 20,
			LimitBytes: 512 << 20, MemoryPercent: 25,
		}},
	}
	panels := quoteANSI(model.renderStatsPanel(140, 8))
	model.statsSubtab = StatsDocker
	panels += quoteANSI(model.renderStatsPanel(140, 8))
	model.statsSubtab = StatsChats
	model.stats = pfmstats.Snapshot{Ready: true, Chats: []pfmstats.Chat{
		{Name: "UNKNOWN", Engine: "claude", CPUValid: true, TokenCount: 100, TokensKnown: true},
		{
			Name:            "ACTIVE",
			Engine:          "codex",
			CPUValid:        true,
			TokenCount:      200,
			TokensKnown:     true,
			TokensPerMinute: 125,
			TokenRateValid:  true,
		},
		{Name: "IDLE", Engine: "claude", CPUValid: true, TokenCount: 300, TokensKnown: true, TokenRateValid: true},
		{Name: "OPEN", Engine: pfmengine.MustLookup(pfmengine.OpenCode).LongName, CPUValid: true},
	}}
	golden(t, "ui_stats_columns.ansi", panels+quoteANSI(model.renderStatsPanel(140, 10)))
}

// ansiForeground renders a #rrggbb palette colour as the escape sequence
// lipgloss emits for it.
func ansiForeground(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	var red, green, blue int
	if _, err := fmt.Sscanf(hex[1:], "%02x%02x%02x", &red, &green, &blue); err != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", red, green, blue)
}
