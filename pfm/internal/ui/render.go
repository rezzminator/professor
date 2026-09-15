package ui

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/sky"
	pfmstats "hostops/pfm/internal/stats"
	"hostops/pfm/internal/theme"
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true)
	groupStyleA = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#5eead4"))
	groupStyleB = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7dd3fc"))
	borderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748b"))
	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#334155"))
	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94a3b8"))
	codexStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e879f9"))
	opencodeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#60a5fa"))
	agentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#fb923c"))
	statsHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#22d3ee"))
	statsClaudeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#5eead4"))
	statsEngineStyles map[pfmengine.ID]lipgloss.Style
	statsCPUStyle     = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#4ade80"))
	statsNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#67e8f9"))
	statsMemoryStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#60a5fa"))
	statsTokenStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#facc15"))
	statsGearStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#fb923c"))
	statsImageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c084fc"))
	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#fde047"))
	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#67e8f9"))
	limitGradient        []color.Color
	limitPercentStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff"))
	limitErrorStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#fb7185"))
	professorUpdateStyle = lipgloss.NewStyle().Bold(true).Blink(true).
				Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#facc15"))
	professorUpdateSelectedStyle = lipgloss.NewStyle().Bold(true).Blink(true).
					Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color("#fde047"))
	configuredCosmosPalette theme.Palette
)

func configureStyles(palette theme.Palette) {
	configuredCosmosPalette = palette
	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Header)).
		Background(lipgloss.Color(palette.HeaderBg))
	groupStyleA = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.GroupA))
	groupStyleB = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.GroupB))
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Border))
	selectedStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Header)).
		Background(lipgloss.Color(palette.Selected))
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Dim))
	codexStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.EngineRow[pfmengine.Codex]))
	opencodeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.EngineRow[pfmengine.Opencode]))
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.AgentRow))
	statsHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.StatsHeader))
	statsClaudeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsEngine[pfmengine.Claude]))
	statsEngineStyles = make(map[pfmengine.ID]lipgloss.Style, len(pfmengine.All()))
	for _, id := range pfmengine.All() {
		statsEngineStyles[id] = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsEngine[id]))
	}
	statsCPUStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsCPU))
	statsNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsName))
	statsMemoryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsRAM))
	statsTokenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsToken))
	statsGearStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsGear))
	statsImageStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.StatsImage))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Warn))
	labelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Label))
	limitGradient = lipgloss.Blend1D(
		101,
		lipgloss.Color(palette.LimitGreen),
		lipgloss.Color(palette.LimitAmber),
		lipgloss.Color(palette.LimitRed),
	)
	limitPercentStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.Header))
	limitErrorStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(palette.LimitRed))
	professorUpdateStyle = lipgloss.NewStyle().Bold(true).Blink(true).
		Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color(palette.LimitAmber))
	professorUpdateSelectedStyle = lipgloss.NewStyle().Bold(true).Blink(true).
		Foreground(lipgloss.Color("#111827")).Background(lipgloss.Color(palette.LimitAmber))
}

// View renders only the visible viewport, so frame cost is independent of the
// total fleet size.
func (model Model) View() tea.View {
	view := tea.NewView(model.render())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeNone
	view.WindowTitle = "pfm"
	return view
}

func (model Model) render() string {
	width := maxInt(40, model.width)
	height := maxInt(12, model.height)
	header := model.renderHeader(width)
	query := model.renderQuery(width)
	footer := model.renderFooter(width)
	bodyHeight := maxInt(4, height-6)
	// Only one of these ever reaches the frame; building renderListPanel
	// unconditionally then discarding it on every Stats/Limits/Cosmos frame
	// paid for a full fleet-row layout nobody saw (2026-09-08: the Limits
	// tab's own idle backoff still redraws at whatever the sky/clock tick
	// cadence is, so this was a wasted list build on every one of those
	// frames too).
	var body string
	switch model.tab {
	case TabStats:
		body = model.renderStatsPanel(width, bodyHeight)
	case TabLimits:
		body = model.renderLimitsPanel(width, bodyHeight)
	case TabCosmos:
		body = model.renderCosmosPanel(width, bodyHeight)
	default:
		body = model.renderListPanel(width, bodyHeight)
	}
	return strings.Join([]string{header, query, body, footer}, "\n")
}

func (model Model) renderTabs(width int) string {
	chat := " Chats "
	stats := " Stats "
	limits := " Limits "
	cosmos := " cosmos "
	switch model.tab {
	case TabChats:
		chat = selectedStyle.Render(chat)
	case TabStats:
		stats = selectedStyle.Render(stats)
	case TabLimits:
		limits = selectedStyle.Render(limits)
	case TabCosmos:
		cosmos = selectedStyle.Render(cosmos)
	}
	return fillLine(" tabs  "+chat+" "+stats+" "+limits+" "+cosmos+"   tab/shift+tab", width)
}

func (model Model) renderHeader(width int) string {
	contentWidth := width
	if model.skyEnabled {
		contentWidth = maxInt(1, width-18)
	}
	cache := "🪫 5m"
	if model.cache1H {
		cache = "⚡ 1h"
	}
	refresh := ""
	if model.refreshing {
		refresh = " · ⟳ refreshing"
	}
	headerAccount := model.primary
	headerMedal := accountMedal(headerAccount)
	if len(model.accountIDs) == 0 {
		if len(model.codexAccountIDs) != 0 {
			headerAccount = model.codexPrimary
			headerMedal = codexAccountMedal(headerAccount)
		} else if len(model.opencodeAccountIDs) != 0 {
			headerAccount = model.opencodePrimary
			headerMedal = accountMedal(headerAccount)
		}
	}
	text := fmt.Sprintf(
		" pfm  %s account %d · %s · %d rows · %d hidden · %d empty%s",
		headerMedal,
		headerAccount,
		cache,
		len(model.rows),
		model.killedCount,
		model.suppressedCount,
		refresh,
	)
	lines := []string{
		headerStyle.Render(fillLine(text, contentWidth)),
		model.renderTabs(contentWidth),
	}
	switch model.tab {
	case TabStats:
		lines = append(lines, model.renderStatsHeader(contentWidth))
	case TabLimits:
		lines = append(lines, dimStyle.Render(fillLine(
			" Limits · live usage windows across every account",
			contentWidth,
		)))
	case TabCosmos:
		lines = append(lines, dimStyle.Render(fillLine(model.renderCosmosSubheader(), contentWidth)))
	default:
		lines = append(lines, dimStyle.Render(fillLine(
			" Chats · fuzzy search and all existing chat controls",
			contentWidth,
		)))
	}
	if !model.skyEnabled {
		return strings.Join(lines, "\n")
	}
	counts := liveEngineCounts(model.rows)
	widget := sky.Frame(sky.Options{
		Counts:   counts,
		Width:    18,
		Height:   3,
		TimeNS:   model.nowNS,
		Events:   model.skyEvents,
		Colorize: true,
	})
	for index := range lines {
		lines[index] = fillLine(lines[index], contentWidth) + widget[index]
	}
	return strings.Join(lines, "\n")
}

func (model Model) renderQuery(width int) string {
	if model.tab == TabStats {
		return model.renderStatsSubtabs(width)
	}
	if model.tab == TabLimits {
		return dimStyle.Render(fillLine(" limits  live usage windows · ↑↓ scroll", width))
	}
	if model.tab == TabCosmos {
		// Precedence is the receipt of the LAST keystroke first: a refused key
		// names itself before anything else, then the reticle's card, then
		// the ledger's own state.
		switch {
		case model.cosmosStatus != "":
			return warnStyle.Render(
				fillLine(" cosmos  "+ansiTruncateRunes(model.cosmosStatus, maxInt(0, width-9)), width),
			)
		case model.cosmosSelected != "":
			return dimStyle.Render(
				fillLine(" cosmos  "+ansiTruncateRunes(model.cosmosSelectionHUD(), maxInt(0, width-9)), width),
			)
		}
		status := "live comms ledger"
		if model.cosmosLoading {
			status = "sampling…"
		}
		return dimStyle.Render(fillLine(" cosmos  "+status+" · newest 24h", width))
	}
	input := model.query.View()
	status := fmt.Sprintf("%d/%d visible", len(model.filtered), len(model.order))
	// ⌃X takes the status line either way: the receipt when it landed, the
	// reason when it was refused. It acts immediately, so its outcome has to
	// be as immediate — and as visible — as the keystroke.
	if model.killStatus != "" {
		status = model.killStatus
	}
	available := maxInt(8, width-lipgloss.Width(status)-2)
	input = ansi.Truncate(input, available, "…")
	line := input + strings.Repeat(
		" ",
		maxInt(1, width-lipgloss.Width(input)-lipgloss.Width(status)),
	) + dimStyle.Render(status)
	return fillLine(line, width)
}

func (model Model) renderStatsSubtabs(width int) string {
	chats := " Chats "
	docker := " Docker "
	if model.statsSubtab == StatsChats {
		chats = selectedStyle.Render(chats)
	} else {
		docker = selectedStyle.Render(docker)
	}
	prefix := " subtabs  "
	if model.statsFocus == StatsFocusSubtab {
		prefix = "›subtabs  "
	}
	return fillLine(prefix+chats+" "+docker+"   ←/→", width)
}

func (model Model) renderStatsHeader(width int) string {
	value := func(percent float64, valid bool) string {
		if !valid {
			return "…"
		}
		return fmt.Sprintf("%.0f%%", percent)
	}
	header := model.stats.Header
	line := " " + statsCPUStyle.Render("CPU "+value(header.CPUPercent, header.CPUValid)) +
		" · " + statsTokenStyle.Render(fmt.Sprintf("PSI %.0f%%", header.PSIPercent)) +
		" · " + statsMemoryStyle.Render(fmt.Sprintf("RAM %.0f%%", header.RAMPercent)) +
		" · " + statsImageStyle.Render(fmt.Sprintf("SWAP %.0f%%", header.SwapPercent))
	if model.statsError != "" {
		line += " · " + warnStyle.Render("sample failed: "+model.statsError)
	} else {
		if len(model.stats.Warnings) > 0 {
			warning := model.stats.Warnings[0]
			if len(model.stats.Warnings) > 1 {
				warning += fmt.Sprintf(" (+%d)", len(model.stats.Warnings)-1)
			}
			line += " · " + warnStyle.Render("warning: "+warning)
		}
		if model.statsLoading {
			line += " · " + dimStyle.Render("sampling…")
		}
	}
	return fillLine(line, width)
}

func (model Model) renderFooter(width int) string {
	if model.tab == TabStats {
		first := " ↑↓ focus/rows  ←→ focused tab  c CPU sort  m RAM sort"
		second := " esc cancel · live samples every 2s only while Stats is focused"
		return dimStyle.Render(fillLine(first, width)) + "\n" +
			dimStyle.Render(fillLine(second, width))
	}
	if model.tab == TabLimits {
		first := " ↑↓ scroll  pgup/pgdown page  home/end jump"
		second := " tab/shift+tab cycle tabs · esc cancel · live samples every 2s while focused"
		return dimStyle.Render(fillLine(first, width)) + "\n" +
			dimStyle.Render(fillLine(second, width))
	}
	if model.tab == TabCosmos {
		first := " ↑↓ select · enter open · s system · o classic sky · tab/shift+tab cycle tabs · esc cancel"
		second := " [ ] ±5m · { } ±1h · space play · n now · ledger samples every 2s while focused"
		if width < 96 {
			first = " ↑↓ select · enter open · s system · o classic · esc cancel"
			second = " [ ] ±5m · { } ±1h · space play · n now"
		}
		return dimStyle.Render(fillLine(first, width)) + "\n" +
			dimStyle.Render(fillLine(second, width))
	}
	first := " ↑↓ move  enter open  esc cancel  type to fuzzy-find"
	second := " ⌃X kill  ⌃E 1h  ⌃S account  ⌃O reboot"
	if width < 96 {
		first = " ↑↓ move · enter open · esc cancel · type find"
		second = " ⌃X kill · ⌃E 1h · ⌃S acct · ⌃O reboot"
	}
	return dimStyle.Render(fillLine(first, width)) + "\n" +
		dimStyle.Render(fillLine(second, width))
}

func (model Model) renderStatsPanel(width, height int) string {
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	lines := make([]string, 0, innerHeight)
	if model.statsSubtab == StatsChats {
		nameWidth := minInt(32, maxInt(8, innerWidth-72))
		header := fmt.Sprintf(
			"  %-*s %-7s %7s %8s %6s %9s %8s %5s %12s",
			nameWidth, "NAME", "ENGINE", "CPU%", "RSS", "RAM%", "TOKENS", "TOK/MIN", "GEAR", "USAGE",
		)
		lines = append(lines, statsHeaderStyle.Render(fillLine(header, innerWidth)))
		for index := range model.stats.Chats {
			chat := &model.stats.Chats[index]
			if len(lines) >= innerHeight {
				break
			}
			cpu := "…"
			if chat.CPUValid {
				cpu = fmt.Sprintf("%.1f%%", chat.CPUPercent)
			}
			gear := "-"
			if chat.GearCount > 0 {
				gear = fmt.Sprintf("⚙%d", chat.GearCount)
			}
			tokens := "…"
			if chat.TokensKnown {
				tokens = formatTokens(chat.TokenCount)
			}
			tokensPerMinute := "…"
			if chat.TokenRateValid {
				tokensPerMinute = formatTokenRate(chat.TokensPerMinute)
			}
			plain := fmt.Sprintf(
				"  %-*s %-7s %7s %8s %5.1f%% %9s %8s %5s %12s",
				nameWidth, clipRunes(cleanField(chat.Name), nameWidth), chat.Engine, cpu,
				formatSize(int64(chat.RSSBytes)), chat.RAMPercent,
				tokens, tokensPerMinute, gear, usageSpark(chat.Spark),
			)
			plain = fillLine(plain, innerWidth)
			if model.statsFocus == StatsFocusContent && index == model.statsCursor {
				lines = append(lines, selectedStyle.Render("›"+ansi.Truncate(plain[1:], maxInt(0, innerWidth-1), "")))
				continue
			}
			engineStyle := statsClaudeStyle
			if id, err := pfmengine.Parse(chat.Engine); err == nil {
				if style, ok := statsEngineStyles[id]; ok {
					engineStyle = style
				}
			}
			line := "  " + statsNameStyle.Render(fmt.Sprintf(
				"%-*s", nameWidth, clipRunes(cleanField(chat.Name), nameWidth),
			)) + " " + engineStyle.Render(fmt.Sprintf("%-7s", chat.Engine)) +
				" " + statsCPUStyle.Render(fmt.Sprintf("%7s", cpu)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%8s", formatSize(int64(chat.RSSBytes)))) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%5.1f%%", chat.RAMPercent)) +
				" " + statsTokenStyle.Render(fmt.Sprintf("%9s", tokens)) +
				" " + statsTokenStyle.Render(fmt.Sprintf("%8s", tokensPerMinute)) +
				" " + statsGearStyle.Render(fmt.Sprintf("%5s", gear)) +
				" " + statsTokenStyle.Render(fmt.Sprintf("%12s", usageSpark(chat.Spark)))
			lines = append(lines, fillLine(line, innerWidth))
		}
	} else {
		available := maxInt(16, innerWidth-36)
		nameWidth := minInt(24, maxInt(8, available/3))
		imageWidth := maxInt(8, available-nameWidth)
		header := fmt.Sprintf(
			"  %-*s %-*s %7s %8s %8s %6s",
			nameWidth, "NAME", imageWidth, "IMAGE", "CPU%", "MEMORY", "LIMIT", "MEM%",
		)
		lines = append(lines, statsHeaderStyle.Render(fillLine(header, innerWidth)))
		for index, container := range model.stats.Docker {
			if len(lines) >= innerHeight {
				break
			}
			cpu := "…"
			if container.CPUValid {
				cpu = fmt.Sprintf("%.1f%%", container.CPUPercent)
			}
			limit := "max"
			if container.LimitBytes > 0 {
				limit = formatSize(int64(container.LimitBytes))
			}
			plain := fillLine(fmt.Sprintf(
				"  %-*s %-*s %7s %8s %8s %5.1f%%",
				nameWidth, clipRunes(cleanField(container.Name), nameWidth),
				imageWidth, clipRunes(cleanField(container.Image), imageWidth), cpu,
				formatSize(int64(container.MemoryBytes)), limit,
				container.MemoryPercent,
			), innerWidth)
			if model.statsFocus == StatsFocusContent && index == model.statsDockerCursor {
				lines = append(lines, selectedStyle.Render("›"+ansi.Truncate(plain[1:], maxInt(0, innerWidth-1), "")))
				continue
			}
			line := "  " + statsNameStyle.Render(fmt.Sprintf(
				"%-*s", nameWidth, clipRunes(cleanField(container.Name), nameWidth),
			)) + " " + statsImageStyle.Render(fmt.Sprintf(
				"%-*s", imageWidth, clipRunes(cleanField(container.Image), imageWidth),
			)) + " " + statsCPUStyle.Render(fmt.Sprintf("%7s", cpu)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%8s", formatSize(int64(container.MemoryBytes)))) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%8s", limit)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%5.1f%%", container.MemoryPercent))
			lines = append(lines, fillLine(line, innerWidth))
		}
	}
	if len(lines) == 1 && len(lines) < innerHeight {
		message := "  waiting for first sample…"
		if model.stats.Ready {
			message = "  no live rows"
		}
		lines = append(lines, dimStyle.Render(fillLine(message, innerWidth)))
	}
	for len(lines) < innerHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	return framePanel(" stats ", lines, width)
}

func (model Model) renderLimitsPanel(width, height int) string {
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	allLines := model.renderLimitCards(innerWidth)
	maximum := maxInt(0, len(allLines)-innerHeight)
	offset := minInt(maxInt(0, model.limitsOffset), maximum)
	end := minInt(len(allLines), offset+innerHeight)
	lines := append([]string(nil), allLines[offset:end]...)
	title := " limits "
	if maximum > 0 {
		title = fmt.Sprintf(" limits %d-%d/%d ", offset+1, end, len(allLines))
	}
	if len(lines) == 0 && len(lines) < innerHeight {
		message := "  waiting for first sample…"
		if model.stats.Ready {
			message = "  no live rows"
		}
		lines = append(lines, dimStyle.Render(fillLine(message, innerWidth)))
	}
	for len(lines) < innerHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	return framePanel(title, lines, width)
}

func (model Model) renderLimitCards(innerWidth int) []string {
	now := time.Unix(0, model.nowNS)
	lines := make([]string, 0, len(model.stats.Limits)*4)
	skips := make([]string, 0)
	appendLine := func(line string) {
		lines = append(lines, line)
	}
	for index := range model.stats.Limits {
		account := &model.stats.Limits[index]
		if account.Unsupported {
			// The engine has no limits concept at all — a card would be pure
			// noise, not an error the operator can act on.
			continue
		}
		if account.Absent {
			message := cleanField(account.Status)
			if message == "" {
				message = cleanField(account.Label)
			}
			appendLine(dimStyle.Render(fillLine("  "+message, innerWidth)))
			continue
		}
		if strings.HasPrefix(account.Status, "skipped ") {
			skips = append(skips, cleanField(account.Status))
			continue
		}
		appendLine(borderStyle.Render(fillLine("  "+strings.Repeat("─", maxInt(0, innerWidth-2)), innerWidth)))
		appendLine(statsHeaderStyle.Render(fillLine("  "+limitAccountHeader(*account, now), innerWidth)))
		if account.Status != "" {
			appendLine(dimStyle.Render(fillLine("  ⚠ "+cleanField(account.Status), innerWidth)))
			if len(account.Windows) == 0 {
				continue
			}
		}
		renderedWindows := 0
		for _, window := range account.Windows {
			if strings.HasPrefix(cleanField(window.Name), "unknown[") {
				continue
			}
			// Codex reports reserve/spark side-windows nobody steers by; only
			// its first (primary) window earns a row.
			if account.Engine == pfmengine.Codex && renderedWindows >= 1 {
				break
			}
			appendLine(renderLimitWindow(now, window, innerWidth))
			renderedWindows++
		}
		if renderedWindows == 0 {
			appendLine(dimStyle.Render(fillLine("  ⚠ limits unavailable", innerWidth)))
		}
	}
	if len(skips) > 0 {
		appendLine(dimStyle.Render(fillLine("  "+strings.Join(skips, " · "), innerWidth)))
	}
	return lines
}

func limitAccountHeader(account pfmstats.AccountLimits, now time.Time) string {
	emoji := cleanField(account.Emoji)
	identity := "account " + strconv.Itoa(account.Account)
	if account.Engine == pfmengine.Codex {
		if emoji == "" {
			emoji = "⬢"
		}
		identity = cleanField(account.Label)
		if identity == "" {
			identity = pfmengine.MustLookup(pfmengine.Codex).Short
		}
	} else if emoji == "" {
		emoji = "·"
	}
	plan := titleWord(cleanField(account.Plan))
	if plan == "" {
		if descriptor, err := pfmengine.Lookup(account.Engine); err == nil {
			plan = descriptor.Short
		} else if account.Engine != "" {
			plan = titleWord(string(account.Engine))
		} else {
			plan = pfmengine.MustLookup(pfmengine.Claude).Short
		}
	}
	confirmation := "provider confirmation unavailable"
	if !account.ConfirmedAt.IsZero() {
		confirmation = "provider confirmed " + limitAge(now, account.ConfirmedAt) + " ago"
	}
	return fmt.Sprintf("%s %s · %s · %s", emoji, identity, plan, confirmation)
}

// renderLimitWindow draws one usage row on the single scale every engine
// shares: the bar fills by usage and the number reads "% used". A Codex row
// on an inverted "% left" scale drew the same short bar for opposite meanings.
func renderLimitWindow(now time.Time, window pfmstats.Window, innerWidth int) string {
	const nameWidth = 10
	showReset := innerWidth >= 60
	reserved := 27
	if showReset {
		reserved += 18
	}
	barWidth := minInt(40, maxInt(1, innerWidth-reserved))
	name := fmt.Sprintf("%-*s", nameWidth, clipRunes(cleanField(window.Name), nameWidth))
	bar := limitBar(window.UsedPct, barWidth)
	percent := fmt.Sprintf("%.0f%% used", window.UsedPct)
	if window.UsedPct < 0 {
		// pfmstats.UnknownUsedPct — no trustworthy reading (an expired window
		// awaiting its refetch). Blank the bar's columns so the rows stay
		// aligned and print an em dash where the number would be; the reset
		// note carries the explanation.
		bar = strings.Repeat(" ", lipgloss.Width(bar))
		percent = "—"
	}
	percentStyle := limitPercentStyle
	if window.UsedPct >= 95 {
		percentStyle = limitErrorStyle
	}
	line := "  " + name + "  " + bar + "  " + percentStyle.Render(fmt.Sprintf("%9s", percent))
	if showReset {
		reset, urgent := limitReset(now, window)
		style := dimStyle
		if urgent {
			style = warnStyle
		}
		line += "   " + style.Render(reset)
	}
	return fillLine(line, innerWidth)
}

func limitBar(percent float64, width int) string {
	// Geometric shapes (U+25B0/U+25B1) and ASCII brackets on purpose: block
	// elements (U+2580–U+259F — █ ░ ▉ ▕) are drawn by VS Code's WebGL
	// terminal as "custom glyphs" from its own atlas rather than the font,
	// and a live 2s refresh of them left stale and blank cells on screen
	// (2026-09-11). Every glyph here goes through the ordinary font path.
	width = maxInt(1, width)
	percent = math.Max(0, math.Min(100, percent))
	style := limitUsageStyle(percent)
	if percent >= 100 && width >= 4 {
		return style.Render("[" + strings.Repeat("▰", width-4) + "FULL" + "]")
	}
	filled := int(math.Round(percent / 100 * float64(width)))
	if filled > width {
		filled = width
	}
	return style.Render("[" + strings.Repeat("▰", filled) + strings.Repeat("▱", width-filled) + "]")
}

func limitUsageStyle(percent float64) lipgloss.Style {
	if len(limitGradient) == 0 {
		return lipgloss.NewStyle()
	}
	index := int(math.Round(math.Max(0, math.Min(100, percent))))
	return lipgloss.NewStyle().Foreground(limitGradient[index])
}

func limitReset(now time.Time, window pfmstats.Window) (string, bool) {
	if window.ResetNote != "" || window.ResetAt.IsZero() {
		note := cleanField(window.ResetNote)
		if note == "" {
			note = "reset unavailable"
		}
		return "↻ " + note, false
	}
	remaining := window.ResetAt.Sub(now)
	if remaining <= 0 {
		return "↻ refreshing…", false
	}
	totalMinutes := int64(remaining / time.Minute)
	if totalMinutes < 1 {
		return "↻ <1m", true
	}
	if totalMinutes < 60 {
		return fmt.Sprintf("↻ %dm", totalMinutes), true
	}
	hours := totalMinutes / 60
	if hours < 24 {
		return fmt.Sprintf("↻ %dh %dm", hours, totalMinutes%60), false
	}
	return fmt.Sprintf("↻ %dd %dh", hours/24, hours%24), false
}

func limitAge(now, confirmed time.Time) string {
	age := now.Sub(confirmed)
	if age < 0 {
		age = 0
	}
	seconds := int64(age / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

func titleWord(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (model Model) renderListPanel(width, height int) string {
	title := fmt.Sprintf(" fleet %d ", len(model.filtered))
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	lines := make([]string, 0, innerHeight)
	if len(model.filtered) == 0 {
		lines = append(lines, dimStyle.Render(fillLine("  no matches", innerWidth)))
	} else {
		start := maxInt(0, model.cursor-innerHeight/2)
		previousProject := ""
		previousNameGroup := ""
		for position := start; position < len(model.filtered) &&
			len(lines) < innerHeight; position++ {
			row := model.rows[model.filtered[position]]
			project := cleanField(row.Project)
			if project == "" {
				project = "?"
			}
			nameGroup, grouped := model.nameGroups[model.filtered[position]]
			// A name group folded across projects (rebuildOrder already placed
			// every member contiguously, at the first project's slot) reads as
			// ONE panel: crossing into a member's own project here must not
			// reopen a second project banner or repeat the group's label.
			continuesGroup := grouped && nameGroup.name == previousNameGroup
			if project != previousProject && !continuesGroup {
				if len(lines)+1 >= innerHeight && position == model.cursor {
					// The selected row always wins the final viewport line.
				} else {
					style := groupStyleA
					if model.projectOrdinal(project)%2 == 1 {
						style = groupStyleB
					}
					group := "╭─ " + clipRunes(project, maxInt(1, innerWidth-5))
					lines = append(
						lines,
						style.Render(fillLine(group, innerWidth)),
					)
				}
				previousNameGroup = ""
			}
			previousProject = project
			if len(lines) >= innerHeight {
				break
			}
			if grouped && nameGroup.name != previousNameGroup && len(lines) < innerHeight {
				lines = append(lines, labelStyle.Render(fillLine(
					"│  "+nameGroup.name+fmt.Sprintf(" (%d)", nameGroup.count),
					innerWidth,
				)))
			}
			if grouped {
				previousNameGroup = nameGroup.name
			} else {
				previousNameGroup = ""
			}
			if len(lines) >= innerHeight {
				break
			}
			lines = append(
				lines,
				model.renderGroupedRow(row, position == model.cursor, innerWidth, grouped),
			)
		}
	}
	for len(lines) < innerHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	return framePanel(title, lines, width)
}

func (model Model) projectOrdinal(project string) int {
	for index, group := range model.groups {
		if group.name == project {
			return index
		}
	}
	return 0
}

func (model Model) renderRow(
	row compose.Row,
	selected bool,
	width int,
) string {
	return model.renderGroupedRow(row, selected, width, false)
}

func (model Model) renderGroupedRow(
	row compose.Row,
	selected bool,
	width int,
	grouped bool,
) string {
	pointer := "│ "
	if selected {
		pointer = "› "
	}
	if grouped {
		pointer += "  "
	}
	name := cleanField(row.Name)
	if name == "" {
		name = "(unnamed)"
	}
	if model.mergeNewChat && (isNewChatKind(row.Kind) || row.Kind == compose.ProfessorUpdate) {
		ids := model.newChatEngines()
		labels := make([]string, 0, len(ids))
		for _, id := range ids {
			label := pfmengine.MustLookup(id).Short
			if id == model.newChatEngine {
				label = "[ " + label + " ]"
			}
			labels = append(labels, label)
		}
		if row.Kind == compose.ProfessorUpdate {
			for index, label := range labels {
				if strings.HasPrefix(label, "[ ") {
					labels[index] = "◖ " + strings.TrimSuffix(strings.TrimPrefix(label, "[ "), " ]") + " ◗"
				} else {
					labels[index] = "[ " + label + " ]"
				}
			}
			name += "  " + strings.Join(labels, " ")
		} else {
			name = strings.Join(labels, " ")
		}
	}
	if row.Kind == compose.ProfessorUpdate {
		sparkle := "✦"
		if (model.nowNS/int64(500*time.Millisecond))%2 != 0 {
			sparkle = "✧"
		}
		content := pointer + sparkle + " PROFESSOR UPDATE " + sparkle + "  " + name + "  Enter → guided upgrade"
		line := fillLine(ansi.Truncate(content, width, "…"), width)
		if selected {
			return professorUpdateSelectedStyle.Render(line)
		}
		return professorUpdateStyle.Render(line)
	}
	name = fixedDisplayColumn(name, 30)
	marker := rowMarker(row.Kind)
	badges := model.rowBadges(row)
	badges = fixedDisplayColumn(badges, 20)
	prompts := fmt.Sprintf("%dp", row.PromptCount)
	size := sizeBadge(row)
	left := pointer + marker + " " + name + " " + badges + " " +
		fmt.Sprintf("%4s %6s", prompts, size)
	age := formatAge(row, model.nowNS)
	if selected && (!model.mergeNewChat || (!isNewChatKind(row.Kind) && row.Kind != compose.ProfessorUpdate)) {
		age += "  " + carouselBoxes(model.actionIndex)
	}
	leftWidth := maxInt(1, width-lipgloss.Width(age)-1)
	left = ansi.Truncate(left, leftWidth, "…")
	line := left + strings.Repeat(
		" ",
		maxInt(1, width-lipgloss.Width(left)-lipgloss.Width(age)),
	) + age
	line = fillLine(line, width)
	if selected {
		if row.Kind == compose.ProfessorUpdate {
			return professorUpdateSelectedStyle.Render(line)
		}
		return selectedStyle.Render(line)
	}
	switch row.Kind {
	case compose.ProfessorUpdate:
		return professorUpdateStyle.Render(line)
	case compose.LiveCodex, compose.ResumeCodex, compose.NewCodex:
		return codexStyle.Render(line)
	case compose.ResumeOpencode, compose.NewOpencode:
		return opencodeStyle.Render(line)
	case compose.LiveClaude, compose.ResumeClaude, compose.NewClaude:
		return statsClaudeStyle.Render(line)
	case compose.Agent:
		return agentStyle.Render(line)
	default:
		return line
	}
}

// carouselActions is the action set a selected chat row offers, in cycle order.
// Each carries its label: an emoji alone leaves the row guessing what ⚡ does.
var carouselActions = []struct{ Glyph, Label string }{
	{"▶", "open"},
	{"⚡", "reboot"},
	{"🕐", "1h"},
	{"✖", "kill"},
	{"⏸", "deactive"},
}

// carouselBoxes draws every action as its own labelled box at the row's right
// edge, the current one filled with block edges instead of light brackets. The
// whole set is visible the moment a row is highlighted — a single box whose
// contents change conceals three of the four actions behind blind → presses, and
// the fill marks the selection without colour, which the row-wide selected
// style would otherwise swallow.
func carouselBoxes(index int) string {
	boxes := make([]string, 0, len(carouselActions))
	for position, action := range carouselActions {
		body := action.Glyph + " " + action.Label
		if position == index {
			boxes = append(boxes, "◖"+body+"◗")
			continue
		}
		boxes = append(boxes, "["+body+"]")
	}
	return strings.Join(boxes, " ")
}

func framePanel(title string, lines []string, width int) string {
	innerWidth := maxInt(1, width-2)
	topLabel := "─" + title
	top := "╭" + ansi.Truncate(topLabel, innerWidth, "…")
	top += strings.Repeat("─", maxInt(0, width-lipgloss.Width(top)-1)) + "╮"
	bottom := "╰" + strings.Repeat("─", innerWidth) + "╯"
	framed := make([]string, 0, len(lines)+2)
	framed = append(framed, borderStyle.Render(top))
	for _, line := range lines {
		framed = append(
			framed,
			borderStyle.Render("│")+
				fillLine(line, innerWidth)+
				borderStyle.Render("│"),
		)
	}
	framed = append(framed, borderStyle.Render(bottom))
	return strings.Join(framed, "\n")
}

func rowMarker(kind compose.Kind) string {
	switch kind {
	case compose.LiveClaude, compose.LiveCodex, compose.LiveSplit:
		return "●"
	case compose.Booting:
		return "◐"
	case compose.Agent:
		return "⚙"
	case compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpencode:
		return "↻"
	case compose.NewClaude, compose.NewCodex, compose.NewOpencode:
		return "✦"
	case compose.ProfessorUpdate:
		return "⬆"
	default:
		return "·"
	}
}

func (model Model) rowBadges(row compose.Row) string {
	badges := make([]string, 0, 7)
	switch row.Kind {
	case compose.LiveCodex, compose.ResumeCodex, compose.NewCodex:
		badges = append(badges, "⬢")
	case compose.ResumeOpencode, compose.NewOpencode:
		badges = append(badges, "◇")
	case compose.Agent:
		badges = append(badges, "⚙ agent")
	}
	if row.ServerCount > 1 {
		badges = append(
			badges,
			warnStyle.Render(fmt.Sprintf("⚠%dsrv", row.ServerCount)),
		)
	}
	if row.SplitCount > 1 {
		badges = append(badges, fmt.Sprintf("⊞%d", row.SplitCount))
	}
	if len(row.Accounts) != 0 {
		for _, account := range row.Accounts {
			badges = append(badges, accountMedal(account))
		}
	} else if row.Account != 0 {
		if compose.EngineForKind(row.Kind) == pfmengine.Codex {
			badges = append(badges, codexAccountMedal(row.Account))
		} else {
			badges = append(badges, accountMedal(row.Account))
		}
	}
	if row.C1H {
		badges = append(badges, "⚡")
	}
	if row.Attached {
		badges = append(badges, "⇄")
	}
	if row.Here {
		badges = append(badges, "←here")
	}
	if row.Killed {
		badges = append(badges, dimStyle.Render("·hidden"))
	}
	return strings.Join(badges, " ")
}

func formatAge(row compose.Row, nowNS int64) string {
	age := row.AgeNS
	if nowNS > 0 && row.ActivityNS > 0 {
		age = nowNS - row.ActivityNS
	}
	if age < 0 {
		age = 0
	}
	duration := time.Duration(age)
	switch {
	case duration < time.Minute:
		return fmt.Sprintf("%ds", int64(duration/time.Second))
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int64(duration/time.Minute))
	case duration < 24*time.Hour:
		return fmt.Sprintf("%dh", int64(duration/time.Hour))
	default:
		return fmt.Sprintf("%dd", int64(duration/(24*time.Hour)))
	}
}

func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", maxInt64(0, size))
	}
	units := []string{"K", "M", "G", "T"}
	value := float64(size)
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == "T" {
			if value < 10 {
				return fmt.Sprintf("%.1f%s", value, unit)
			}
			return fmt.Sprintf("%.0f%s", value, unit)
		}
	}
	return "0B"
}

func formatTokens(tokens int64) string {
	if tokens < 0 {
		return "…"
	}
	return formatTokenNumber(float64(tokens))
}

// usageSpark draws the chat's recent token burn as one line of block runes:
// each column is a sample delta, height relative to the busiest sample in the
// window. An idle chart stays flat; a burst spikes. No history yet renders as
// "…" — the chart needs two samples before it can claim anything.
func usageSpark(deltas []int64) string {
	if len(deltas) == 0 {
		return "…"
	}
	// Scan-line ladder (Misc Technical U+23BA–U+23BD between _ and ¯) rather
	// than block elements ▁…█ or braille: both of those ranges are WebGL
	// "custom glyphs" in VS Code's terminal, drawn from its atlas instead of
	// the font, and painted stale/blank cells on live refresh (2026-09-11).
	// cmd/pfm/webgl_glyph_guard_test.go pins the banned ranges.
	blocks := []rune("_⎽⎼⎻⎺¯")
	busiest := deltas[0]
	for _, delta := range deltas[1:] {
		if delta > busiest {
			busiest = delta
		}
	}
	runes := make([]rune, 0, len(deltas))
	for _, delta := range deltas {
		if delta <= 0 || busiest <= 0 {
			runes = append(runes, blocks[0])
			continue
		}
		height := int(float64(delta)/float64(busiest)*float64(len(blocks)-1) + 0.5)
		runes = append(runes, blocks[minInt(height, len(blocks)-1)])
	}
	return string(runes)
}

func formatTokenRate(tokens float64) string {
	if tokens < 0 || math.IsNaN(tokens) || math.IsInf(tokens, 0) {
		return "…"
	}
	if tokens == 0 {
		return "–"
	}
	return formatTokenNumber(tokens)
}

func formatTokenNumber(tokens float64) string {
	if tokens < 1000 {
		return fmt.Sprintf("%.0f", tokens)
	}
	units := []string{"K", "M", "B", "T"}
	value := tokens
	for _, unit := range units {
		value /= 1000
		if value < 1000 || unit == "T" {
			if value < 10 {
				return fmt.Sprintf("%.1f%s", value, unit)
			}
			return fmt.Sprintf("%.0f%s", value, unit)
		}
	}
	return "0"
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func accountMedal(account int) string {
	if configuredAccountEmojis != nil {
		if emoji := configuredAccountEmojis[account]; emoji != "" {
			return emoji
		}
		return "·"
	}
	return pfmconfig.DefaultEmoji(account)
}

func codexAccountMedal(account int) string {
	if configuredCodexAccountEmojis != nil {
		if emoji := configuredCodexAccountEmojis[account]; emoji != "" {
			return emoji
		}
		return "·"
	}
	return pfmconfig.DefaultEmoji(account)
}

func fillLine(value string, width int) string {
	value = ansi.Truncate(value, maxInt(0, width), "")
	padding := width - lipgloss.Width(value)
	if padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}

func fixedDisplayColumn(value string, width int) string {
	value = ansi.Truncate(value, maxInt(0, width), "…")
	if padding := width - lipgloss.Width(value); padding > 0 {
		value += strings.Repeat(" ", padding)
	}
	return value
}

func clipRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func cleanField(value string) string {
	return strings.TrimSpace(strings.Map(func(runeValue rune) rune {
		if unicode.IsControl(runeValue) {
			return ' '
		}
		return runeValue
	}, value))
}
