package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleetdb"
	pfmstats "hostops/pfm/internal/stats"
	"hostops/pfm/internal/theme"
)

func TestRenderGoldens(t *testing.T) {
	// Render in a FIXED zone so these files pin the code and not the machine
	// that generated them. Two separate zone dependencies feed a frame: the
	// cosmos fixture's absolute clock (pinned to UTC in cosmosGoldenSnapshot,
	// which the sky widget consumes via sky.Frame's TimeNS) and the ledger
	// footer, which renders each edge as time.Unix(0, LastNS).Format(...) —
	// and time.Unix returns a LOCAL time. That second one is correct
	// production behaviour: an operator should read their own clock, not
	// UTC. It is only the golden that must not vary, so the zone is pinned
	// here, for this process, and never in the code under test.
	//
	// Without this, these goldens pass on the machine that wrote them and
	// fail everywhere else — which is exactly how the sky-enabled golden
	// passed on the host and failed in the dev container.
	restoreLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = restoreLocal })

	tests := []struct {
		name string
		path string
		got  func() string
	}{
		{
			name: "ansi 80 columns",
			path: "ui_80.ansi",
			got: func() string {
				return quoteANSI(NewModel(fixtureSnapshot(80)).View().Content)
			},
		},
		{
			name: "ansi 120 columns",
			path: "ui_120.ansi",
			got: func() string {
				return quoteANSI(NewModel(fixtureSnapshot(120)).View().Content)
			},
		},
		{
			name: "Codex-only ansi 80 columns",
			path: "ui_codex_only_80.ansi",
			got: func() string {
				return quoteANSI(engineOnlyGoldenSnapshot("cx", 80).View().Content)
			},
		},
		{
			name: "Claude-only ansi 80 columns",
			path: "ui_claude_only_80.ansi",
			got: func() string {
				return quoteANSI(engineOnlyGoldenSnapshot("cc", 80).View().Content)
			},
		},
		{
			name: "OpenCode row ansi 80 columns",
			path: "ui_opencode_80.ansi",
			got: func() string {
				return quoteANSI(openCodeGoldenSnapshot(80).View().Content)
			},
		},
		{
			name: "Stats Chats ansi 80 columns",
			path: "stats_chats_80.ansi",
			got: func() string {
				return quoteANSI(statsGoldenModel(80).View().Content)
			},
		},
		{
			name: "Stats Chats ansi 120 columns",
			path: "stats_chats_120.ansi",
			got: func() string {
				return quoteANSI(statsGoldenModel(120).View().Content)
			},
		},
		{
			name: "Stats Limits ansi 80 columns",
			path: "ui_limits_80.ansi",
			got: func() string {
				return quoteANSI(limitsGoldenModel(80).View().Content)
			},
		},
		{
			name: "Stats Limits ansi 120 columns",
			path: "ui_limits_120.ansi",
			got: func() string {
				return quoteANSI(limitsGoldenModel(120).View().Content)
			},
		},
		{
			name: "Cosmos ansi 80 columns",
			path: "ui_cosmos_80.ansi",
			got: func() string {
				return quoteANSI(cosmosGoldenModel(80).View().Content)
			},
		},
		{
			name: "Cosmos ansi 120 columns",
			path: "ui_cosmos_120.ansi",
			got: func() string {
				return quoteANSI(cosmosGoldenModel(120).View().Content)
			},
		},
		{
			name: "Cosmos sky-enabled ansi 80 columns",
			path: "ui_cosmos_sky_80.ansi",
			got: func() string {
				return quoteANSI(cosmosSkyGoldenModel(80).View().Content)
			},
		},
		{
			name: "Cosmos classic sky ansi 80 columns",
			path: "ui_cosmos_classic_80.ansi",
			got: func() string {
				return quoteANSI(cosmosClassicGoldenModel(80).View().Content)
			},
		},
		{
			name: "Cosmos chronoscope replay ansi 80 columns",
			path: "ui_cosmos_replay_80.ansi",
			got: func() string {
				return quoteANSI(cosmosReplayGoldenModel(80).View().Content)
			},
		},
		{
			name: "Cosmos system focus ansi 80 columns",
			path: "ui_cosmos_focus_80.ansi",
			got: func() string {
				return quoteANSI(cosmosFocusGoldenModel(80).View().Content)
			},
		},
		{
			name: "Cosmos reticle ansi 80 columns",
			path: "ui_cosmos_select_80.ansi",
			got: func() string {
				return quoteANSI(cosmosSelectGoldenModel(80).View().Content)
			},
		},
		{
			name: "Cosmos seeded live node ansi 80 columns",
			path: "ui_cosmos_seeded_80.ansi",
			got: func() string {
				return quoteANSI(cosmosSeededGoldenModel(80).View().Content)
			},
		},
		{
			name: "plain",
			path: "ui_plain.txt",
			got: func() string {
				return RenderPlain(fixtureSnapshot(120))
			},
		},
		{
			name: "tsv",
			path: "ui.tsv",
			got: func() string {
				return RenderTSV(fixtureSnapshot(120))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := test.got()
			path := filepath.Join("..", "..", "testdata", "golden", test.path)
			if os.Getenv("PFM_UPDATE_GOLDENS") == "1" {
				if err := os.WriteFile(path, []byte(actual), 0o644); err != nil {
					t.Fatalf("regenerate %s: %v", path, err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v\nactual:\n%s", path, err, actual)
			}
			if !bytes.Equal(want, []byte(actual)) {
				t.Fatalf(
					"%s mismatch\nwant:\n%s\nactual:\n%s",
					firstDifference(want, []byte(actual)),
					want,
					actual,
				)
			}
		})
	}
}

func openCodeGoldenSnapshot(width int) Model {
	snapshot := fixtureSnapshot(width)
	snapshot.Rows = []compose.Row{{
		Kind: compose.ResumeOpenCode, ID: "oc-golden", Name: "OpenCode session",
		Project: "alpha", CWD: "/work/alpha", PromptCount: 3,
		ActivityNS: fixtureNowNS - int64(time.Minute),
	}}
	snapshot.MergeNewChat = false
	return NewModel(snapshot)
}

func engineOnlyGoldenSnapshot(engine pfmengine.ID, width int) Model {
	snapshot := fixtureSnapshot(width)
	rows := make([]compose.Row, 0, len(snapshot.Rows))
	for index := range snapshot.Rows {
		row := &snapshot.Rows[index]
		if compose.EngineForKind(row.Kind) == engine {
			rows = append(rows, *row)
		}
	}
	snapshot.Rows = rows
	snapshot.MergeNewChat = false
	if engine == pfmengine.Codex {
		snapshot.PrimaryAccount = 0
		snapshot.AccountIDs = nil
		snapshot.AccountEmojis = nil
	} else {
		snapshot.CodexPrimaryAccount = 0
		snapshot.CodexAccountIDs = nil
		snapshot.CodexAccountEmojis = nil
	}
	return NewModel(snapshot)
}

func statsGoldenModel(width int) Model {
	model := NewModel(fixtureSnapshot(width))
	model.tab = TabStats
	model.stats = pfmstats.Snapshot{
		Ready: true,
		Header: pfmstats.Header{
			CPUPercent: 12, CPUValid: true, PSIPercent: 3,
			RAMPercent: 44, SwapPercent: 16,
		},
		Chats: []pfmstats.Chat{
			{
				Socket: "active", Name: "BUILD:active", Engine: "claude",
				CPUPercent: 10, CPUValid: true, RSSBytes: 2 << 20, RAMPercent: 3.5,
				TokenCount: 1_250_000, TokensKnown: true,
				TokensPerMinute: 625_000, TokenRateValid: true, GearCount: 2,
				Spark: []int64{0, 120_000, 640_000, 900_000, 450_000, 20_000, 80_000},
			},
			{
				Socket: "idle", Name: "REVIEW:idle", Engine: "codex",
				CPUPercent: 0, CPUValid: true, RSSBytes: 1 << 20, RAMPercent: 1.5,
				TokenCount: 750_000, TokensKnown: true,
				TokensPerMinute: 0, TokenRateValid: true,
				Spark: []int64{0, 0, 0, 0},
			},
			{
				Socket: "new", Name: "NEW:first-sample", Engine: "claude",
				RSSBytes: 512 << 10, RAMPercent: 0.5,
				TokenCount: 5_000, TokensKnown: true,
			},
		},
	}
	return model
}

func limitsGoldenModel(width int) Model {
	model := NewModel(fixtureSnapshot(width))
	model.tab = TabLimits
	model.height = 24
	now := time.Unix(0, fixtureNowNS)
	model.stats = pfmstats.Snapshot{Ready: true, Limits: []pfmstats.AccountLimits{
		{
			Account:     1,
			Emoji:       "🥇",
			Engine:      pfmengine.Claude,
			Label:       "account 1",
			Plan:        "Max 20x",
			ConfirmedAt: now.Add(-12 * time.Second),
			Windows: []pfmstats.Window{
				{Name: "5h", UsedPct: 52.4, ResetAt: now.Add(2*time.Hour + 14*time.Minute)},
				{Name: "7d-fable", UsedPct: 95, ResetAt: now.Add(14 * time.Minute)},
			},
		},
		{
			Account:     2,
			Emoji:       "🥈",
			Engine:      pfmengine.Claude,
			Label:       "account 2",
			Plan:        "Pro",
			ConfirmedAt: now.Add(-40 * time.Second),
			Windows: []pfmstats.Window{
				{Name: "5h", UsedPct: 55, ResetAt: now.Add(4 * time.Hour)},
				{Name: "7d", UsedPct: 100, ResetAt: now.Add(-time.Minute)},
			},
		},
		{
			Engine:      pfmengine.Codex,
			Label:       "Codex",
			Plan:        "pro",
			ConfirmedAt: now.Add(-2 * time.Minute),
			Windows: []pfmstats.Window{
				{Name: "7d", UsedPct: 31, ResetAt: now.Add(6*24*time.Hour + 20*time.Hour)},
			},
		},
		{
			Account: 4,
			Emoji:   "🍀",
			Engine:  pfmengine.Claude,
			Label:   "account 4",
			Status:  "Claude credential rejected (HTTP 403)",
		},
		{Account: 3, Engine: pfmengine.Claude, Label: "account 3", Status: "skipped account 3: no valid credentials"},
	}}
	return model
}

// cosmosGoldenSnapshot builds the fixed-clock cosmos snapshot both the
// no-sky and the sky-enabled goldens render. The inject event is pinned
// 300ms before "now" on purpose: with cosmosCometDuration(fleetdb.KindInject)
// at 1500ms, that lands the comet genuinely mid-flight for any caller that
// renders with the sky on, rather than pinning a frame the gate could pass
// whether the glow code ran or was never wired.
func cosmosGoldenSnapshot(width int, noSky bool) Snapshot {
	snapshot := fixtureSnapshot(width)
	snapshot.NoSky = noSky
	// UTC, deliberately, not time.Local. Everything INSIDE the cosmos box is
	// computed as a difference (now - LastNS) and survives a shifted clock,
	// but the sky widget in the header margin is handed this ABSOLUTE value
	// (render.go passes TimeNS: model.nowNS straight into sky.Frame), so a
	// local-zone fixture renders a different starfield on every machine in a
	// different timezone. That made the sky-enabled golden pass on the host
	// and fail in the dev container. A golden that depends on where it runs
	// pins the machine, not the code.
	cosmosNowNS := time.Date(2027, time.January, 15, 8, 0, 0, 0, time.UTC).UnixNano()
	clockShift := cosmosNowNS - snapshot.NowNS
	snapshot.NowNS = cosmosNowNS
	for index := range snapshot.Rows {
		if snapshot.Rows[index].ActivityNS != 0 {
			snapshot.Rows[index].ActivityNS += clockShift
		}
	}
	snapshot.Cosmos = compose.BuildCosmos(
		snapshot.Rows,
		cosmosGoldenEvents(cosmosNowNS, snapshot.Rows),
		snapshot.NowNS,
		false,
	)
	return snapshot
}

// cosmosGoldenSnapshotSeeded is cosmosGoldenSnapshot's live=true sibling —
// the live-build shape (ui/model.go's own BuildCosmos call), not the replay
// shape every other cosmos golden above pins. It renders through the SAME
// fixture rows and the SAME cosmosGoldenEvents ledger, so the only
// difference from ui_cosmos_80.ansi is the seeded node: fixtureSnapshot's
// row[2] (LiveSplit, "🚀🧭🛠️📦🧪✨ fleet") is never a sender or receiver in
// cosmosGoldenEvents, so under live=false it renders as no node at all —
// exactly the traffic-free-live-row-is-absent law this change replaced. With
// live=true it must appear on its home ring with zero edges.
func cosmosGoldenSnapshotSeeded(width int) Snapshot {
	snapshot := fixtureSnapshot(width)
	snapshot.NoSky = true
	cosmosNowNS := time.Date(2027, time.January, 15, 8, 0, 0, 0, time.UTC).UnixNano()
	clockShift := cosmosNowNS - snapshot.NowNS
	snapshot.NowNS = cosmosNowNS
	for index := range snapshot.Rows {
		if snapshot.Rows[index].ActivityNS != 0 {
			snapshot.Rows[index].ActivityNS += clockShift
		}
	}
	snapshot.Cosmos = compose.BuildCosmos(
		snapshot.Rows,
		cosmosGoldenEvents(cosmosNowNS, snapshot.Rows),
		snapshot.NowNS,
		true,
	)
	return snapshot
}

// cosmosGoldenEvents is the fixed ledger every cosmos golden is cut from: one
// inject 300ms old (mid-comet) and one spawn ten minutes old.
func cosmosGoldenEvents(nowNS int64, rows []compose.Row) []fleetdb.CommsEvent {
	return []fleetdb.CommsEvent{
		{
			ID:   2,
			AtNS: nowNS - int64(300*time.Millisecond), Kind: fleetdb.KindInject,
			SenderUUID: rows[0].ID, Target: rows[1].Name,
			Message: "QA: cosmos goldens are pinned",
		},
		{
			ID:   1,
			AtNS: nowNS - int64(10*time.Minute), Kind: fleetdb.KindSpawn,
			SenderUUID: rows[0].ID, Target: rows[4].Name,
			Message: "begin the child seat",
		},
	}
}

// cosmosReplayGoldenModel pins the chronoscope: `[` puts the playhead five
// minutes back, where the spawn (ten minutes old) has happened and the
// inject (300ms old) has not — a different sky from the live one, cut by
// the same BuildCosmos from the same events, with the ⏪ chip naming the
// moment. The timeline is seeded the way a sample seeds it, because the
// scrub keys refuse by name until the ledger has been sampled once.
func cosmosReplayGoldenModel(width int) Model {
	model := cosmosGoldenModel(width)
	model.cosmosEvents = cosmosGoldenEvents(model.cosmosNowNS, model.rows)
	model.rebuildCosmosTimeline()
	updated, _ := model.Update(printableKey('['))
	return updated.(Model)
}

// cosmosFocusGoldenModel pins `s`: the first star (alpha) takes the whole
// canvas and the spawn edge into beta is drawn leaving the frame.
func cosmosFocusGoldenModel(width int) Model {
	model := cosmosGoldenModel(width)
	updated, _ := model.Update(printableKey('s'))
	return updated.(Model)
}

// cosmosSelectGoldenModel pins the reticle: ↓ selects the first node, the
// halo rings it, and the query row carries its card.
func cosmosSelectGoldenModel(width int) Model {
	model := cosmosGoldenModel(width)
	updated, _ := model.Update(specialKey(tea.KeyDown))
	return updated.(Model)
}

func cosmosGoldenModel(width int) Model {
	model := NewModel(cosmosGoldenSnapshot(width, true))
	model.tab = TabCosmos
	return model
}

// cosmosSeededGoldenModel renders cosmosGoldenSnapshotSeeded: the same
// no-sky cosmos as cosmosGoldenModel, but built with live=true, so the
// quiet LiveSplit row (fixtureSnapshot's row[2], never named by
// cosmosGoldenEvents) now draws as a zero-edge node on its own ring instead
// of being absent.
func cosmosSeededGoldenModel(width int) Model {
	model := NewModel(cosmosGoldenSnapshotSeeded(width))
	model.tab = TabCosmos
	return model
}

// cosmosSkyGoldenModel is cosmosGoldenModel with the sky ON — the only
// golden that actually exercises model.skyEnabled's comet, head burst,
// shockwave rings, and in-flight rail lighting. Without it those code paths
// are pinned by nothing: golden_test.go's NoSky-only cosmos goldens pass
// identically whether the glow renders or was never wired at all.
func cosmosSkyGoldenModel(width int) Model {
	model := NewModel(cosmosGoldenSnapshot(width, false))
	model.tab = TabCosmos
	return model
}

// cosmosClassicGoldenModel is cosmosGoldenModel after the "o" toggle: the
// one golden that pins the classic sky — the first cosmos, one shared ring
// with no stars — so the collapsed branch is exercised rather than trusted.
func cosmosClassicGoldenModel(width int) Model {
	model := cosmosGoldenModel(width)
	model.toggleClassicSky()
	return model
}

// TestCosmosClassicSkyToggle pins the "o" key contract on the cosmos tab:
// one press collapses the orbital sky into the first cosmos — no ✹ suns,
// every chat on the shared ring — and a second press restores the systems.
func TestCosmosClassicSkyToggle(t *testing.T) {
	model := cosmosGoldenModel(80)
	pressed, _ := applyKey(t, model, printableKey('o'))
	if !pressed.classicSky {
		t.Fatalf("o on the cosmos tab did not switch to the classic sky")
	}
	restored, _ := applyKey(t, pressed, printableKey('o'))
	if restored.classicSky {
		t.Fatalf("second o did not restore the orbital sky")
	}
	orbital := cosmosGoldenModel(80).View().Content
	classic := cosmosClassicGoldenModel(80).View().Content
	if !strings.ContainsRune(orbital, '✹') {
		t.Fatalf("orbital sky renders no ✹ sun — the fixture lost its stars")
	}
	if strings.ContainsRune(classic, '✹') {
		t.Fatalf("classic sky still renders a ✹ sun")
	}
	if orbital == classic {
		t.Fatalf("switching to the classic sky changed nothing in the frame")
	}
}

func TestAgentPaletteIsOrangeAndDistinctFromCodex(t *testing.T) {
	agent := fmt.Sprint(agentStyle.GetForeground())
	codex := fmt.Sprint(codexStyle.GetForeground())
	if agent != "{251 146 60 255}" {
		t.Fatalf("agent foreground = %q, want vivid orange", agent)
	}
	if agent == codex {
		t.Fatalf("agent and Codex foregrounds are both %q", agent)
	}
}

func TestTokyoNightOwnsTheHeaderBackground(t *testing.T) {
	configureStyles(theme.Load("tokyo-night"))
	t.Cleanup(func() { configureStyles(theme.Load("default")) })
	if got, want := fmt.Sprint(headerStyle.GetBackground()), "{65 72 104 255}"; got != want {
		t.Fatalf("Tokyo Night header background=%s, want %s", got, want)
	}
}

func TestAccountMedalUsesConfigOwnedDefaultsAndFailsClosedOnMissingRosterEntry(t *testing.T) {
	configuredAccountEmojis = nil
	if got := accountMedal(4); got != "🍀" {
		t.Fatalf("default account 4 medal=%q, want config default", got)
	}
	configuredAccountEmojis = map[int]string{1: "🥇"}
	t.Cleanup(func() { configuredAccountEmojis = nil })
	if got := accountMedal(2); got != "·" {
		t.Fatalf("missing configured account medal=%q, want honest unknown marker", got)
	}
}

func TestFancyRenderNeverWrapsAtFixedWidths(t *testing.T) {
	for _, width := range []int{80, 120} {
		snapshot := fixtureSnapshot(width)
		content := NewModel(snapshot).View().Content
		lines := strings.Split(content, "\n")
		if len(lines) != snapshot.Height {
			t.Fatalf("width %d rendered %d lines, want %d", width, len(lines), snapshot.Height)
		}
		for index, line := range lines {
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("width %d line %d has %d cells", width, index, got)
			}
		}
	}
}

func TestFancyRenderHasNoPreviewAtAnyWidth(t *testing.T) {
	for _, width := range []int{40, 80, 120, 180} {
		content := ansi.Strip(NewModel(fixtureSnapshot(width)).View().Content)
		if strings.Contains(content, " preview ") ||
			strings.Contains(content, "last prompt") {
			t.Fatalf("width %d still renders the retired preview panel:\n%s", width, content)
		}
	}
}

func TestFancyRenderHasNoProjectRotationControl(t *testing.T) {
	content := ansi.Strip(NewModel(fixtureSnapshot(120)).View().Content)
	for _, retired := range []string{"project rotation", "⌃R"} {
		if strings.Contains(content, retired) {
			t.Fatalf("picker still renders retired %q control:\n%s", retired, content)
		}
	}
}

func TestHeaderSeparatesKilledEmptyAndRefreshStatus(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.KilledCount = 12
	snapshot.SuppressedCount = 153
	snapshot.Refreshing = true
	header := ansi.Strip(NewModel(snapshot).renderHeader(120))
	for _, want := range []string{"12 hidden", "153 empty", "⟳ refreshing"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header %q does not contain %q", header, want)
		}
	}
}

func TestRowColumnsHaveIdenticalDisplayPositions(t *testing.T) {
	names := []string{
		"RR",
		"123456789012345678901234567890",
		"123456789012345678901234567890X",
		"🚀🚀🚀🚀🚀 emoji",
		"界面列对齐测试名字",
		"\x1b[31mANSI name\x1b[0m",
	}
	var positions []int
	for _, name := range names {
		row := fixtureSnapshot(120).Rows[1]
		row.Name = name
		row.PromptCount = 37
		row.Size = 1536
		line := ansi.Strip(NewModel(fixtureSnapshot(120)).renderRow(
			row,
			false,
			78,
		))
		got := []int{
			displayIndex(line, "⬢"),
			displayIndex(line, "37p"),
			displayIndex(line, "1.5K"),
		}
		if positions == nil {
			positions = got
			continue
		}
		if !slices.Equal(got, positions) {
			t.Fatalf(
				"name %q columns=%v, want %v in %q",
				name,
				got,
				positions,
				line,
			)
		}
	}
}

func TestPlainAndTSVPickersShareRenderers(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	var plain bytes.Buffer
	outcome, err := (PlainPicker{Writer: &plain}).Pick(
		context.Background(),
		snapshot,
	)
	if err != nil || plain.String() != RenderPlain(snapshot) ||
		outcome.Kind != OutcomeNone {
		t.Fatalf("plain outcome=%#v err=%v", outcome, err)
	}
	var tsv bytes.Buffer
	outcome, err = (TSVPicker{Writer: &tsv}).Pick(
		context.Background(),
		snapshot,
	)
	if err != nil || tsv.String() != RenderTSV(snapshot) ||
		outcome.Kind != OutcomeNone {
		t.Fatalf("TSV outcome=%#v err=%v", outcome, err)
	}
}

func quoteANSI(value string) string {
	lines := strings.Split(value, "\n")
	var output strings.Builder
	for _, line := range lines {
		fmt.Fprintln(&output, strconv.QuoteToGraphic(line))
	}
	return output.String()
}

func displayIndex(value, marker string) int {
	index := strings.Index(value, marker)
	if index < 0 {
		return -1
	}
	return lipgloss.Width(value[:index])
}

func firstDifference(want, got []byte) string {
	limit := min(len(want), len(got))
	for index := 0; index < limit; index++ {
		if want[index] != got[index] {
			return fmt.Sprintf(
				"byte %d: want %#x got %#x (lengths %d/%d)",
				index,
				want[index],
				got[index],
				len(want),
				len(got),
			)
		}
	}
	return fmt.Sprintf("lengths %d/%d", len(want), len(got))
}
