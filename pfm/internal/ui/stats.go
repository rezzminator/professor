package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// statsProbeError fills a seat's resource cells when its probe failed, so an error never reads as an idle seat.
const statsProbeError = "err"

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
	// Before the first sample lands every reading is pending, not zero.
	sampled := model.statsSampled()
	psi := value(header.PSIPercent, sampled)
	if sampled && header.PSIUnavailable {
		psi = "n/a"
	}
	line := " " + statsCPUStyle.Render("CPU "+value(header.CPUPercent, header.CPUValid)) +
		" · " + statsTokenStyle.Render("PSI "+psi) +
		" · " + statsMemoryStyle.Render("RAM "+value(header.RAMPercent, sampled)) +
		" · " + statsImageStyle.Render("SWAP "+value(header.SwapPercent, sampled))
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

func (model Model) renderStatsPanel(width, height int) string {
	innerWidth := maxInt(1, width-2)
	innerHeight := maxInt(1, height-2)
	lines := make([]string, 0, innerHeight)
	title := " stats "
	// page shows the screenful holding the cursor: a list longer than the
	// panel scrolls a page at a time and the title counts what is shown.
	page := func(cursor, total int) int {
		visible := maxInt(1, innerHeight-1)
		offset := max(0, min(cursor, total-1)) / visible * visible
		if total > visible {
			title = fmt.Sprintf(" stats %d-%d/%d ", offset+1, min(total, offset+visible), total)
		}
		return offset
	}
	cpuLabel, memoryLabel := "CPU%", "RSS"
	if model.statsSort == StatsSortRAM {
		memoryLabel += "↓"
	} else {
		cpuLabel += "↓"
	}
	if model.statsSubtab == StatsChats {
		names := make([]string, len(model.stats.Chats))
		for index := range model.stats.Chats {
			names[index] = model.stats.Chats[index].Name
		}
		nameWidth := statsNameWidth(names, maxInt(8, innerWidth-72))
		header := fmt.Sprintf(
			"  %-*s %-7s %7s %8s %6s %9s %8s %5s %12s",
			nameWidth, "NAME", "ENGINE", cpuLabel, memoryLabel, "RAM%", "TOKENS", "TOK/MIN", "GEAR", "USAGE",
		)
		lines = append(lines, statsHeaderStyle.Render(fillLine(header, innerWidth)))
		for index := page(model.statsCursor, len(model.stats.Chats)); index < len(model.stats.Chats); index++ {
			chat := &model.stats.Chats[index]
			if len(lines) >= innerHeight {
				break
			}
			cpu := "…"
			if chat.CPUValid {
				cpu = fmt.Sprintf("%.1f%%", chat.CPUPercent)
			}
			rss, ram := formatSize(int64(chat.RSSBytes)), fmt.Sprintf("%5.1f%%", chat.RAMPercent)
			switch {
			case chat.ProbeFailed:
				cpu, rss, ram = statsProbeError, statsProbeError, statsProbeError
			case chat.NoProcess:
				cpu, rss, ram = "—", "—", "—"
			}
			gear := "-"
			if chat.GearCount > 0 {
				gear = fmt.Sprintf("⚙%d", chat.GearCount)
			}
			tokens := "…"
			if chat.TokensKnown {
				tokens = formatUsageTokens(chat.TokenCount)
			}
			tokensPerMinute := "…"
			if chat.TokenRateValid {
				tokensPerMinute = formatTokenRate(chat.TokensPerMinute)
			}
			plain := fmt.Sprintf(
				"  %-*s %-7s %7s %8s %6s %9s %8s %5s %12s",
				nameWidth, clipRunesEllipsis(cleanField(chat.Name), nameWidth), chat.Engine, cpu,
				rss, ram, tokens, tokensPerMinute, gear, usageSpark(chat.Spark),
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
				"%-*s", nameWidth, clipRunesEllipsis(cleanField(chat.Name), nameWidth),
			)) + " " + engineStyle.Render(fmt.Sprintf("%-7s", chat.Engine)) +
				" " + statsCPUStyle.Render(fmt.Sprintf("%7s", cpu)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%8s", rss)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%6s", ram)) +
				" " + statsTokenStyle.Render(fmt.Sprintf("%9s", tokens)) +
				" " + statsTokenStyle.Render(fmt.Sprintf("%8s", tokensPerMinute)) +
				" " + statsGearStyle.Render(fmt.Sprintf("%5s", gear)) +
				" " + statsTokenStyle.Render(fmt.Sprintf("%12s", usageSpark(chat.Spark)))
			lines = append(lines, fillLine(line, innerWidth))
		}
	} else {
		available := maxInt(16, innerWidth-36)
		names := make([]string, len(model.stats.Docker))
		for index := range model.stats.Docker {
			names[index] = model.stats.Docker[index].Name
		}
		// Names identify; images only describe, so the name column takes
		// the room it needs (up to two thirds) and the image the rest.
		nameWidth := statsNameWidth(names, maxInt(8, available*2/3))
		imageWidth := maxInt(8, available-nameWidth)
		if model.statsSort == StatsSortRAM {
			memoryLabel = "MEMORY↓"
		} else {
			memoryLabel = "MEMORY"
		}
		header := fmt.Sprintf(
			"  %-*s %-*s %7s %8s %8s %6s",
			nameWidth, "NAME", imageWidth, "IMAGE", cpuLabel, memoryLabel, "LIMIT", "MEM%",
		)
		lines = append(lines, statsHeaderStyle.Render(fillLine(header, innerWidth)))
		for index := page(model.statsDockerCursor, len(model.stats.Docker)); index < len(model.stats.Docker); index++ {
			container := &model.stats.Docker[index]
			if len(lines) >= innerHeight {
				break
			}
			cpu := "…"
			if container.CPUValid {
				cpu = fmt.Sprintf("%.1f%%", container.CPUPercent)
			}
			limit := "max"
			memoryPercent := fmt.Sprintf("%5.1f%%", container.MemoryPercent)
			if container.LimitBytes > 0 {
				limit = formatSize(int64(container.LimitBytes))
			} else if model.stats.Header.MemoryBytes == 0 {
				// An unlimited container's share of an unread host RAM is unknown.
				memoryPercent = "—"
			}
			plain := fillLine(fmt.Sprintf(
				"  %-*s %-*s %7s %8s %8s %6s",
				nameWidth, clipRunesEllipsis(cleanField(container.Name), nameWidth),
				imageWidth, clipRunesEllipsis(cleanField(container.Image), imageWidth), cpu,
				formatSize(int64(container.MemoryBytes)), limit,
				memoryPercent,
			), innerWidth)
			if model.statsFocus == StatsFocusContent && index == model.statsDockerCursor {
				lines = append(lines, selectedStyle.Render("›"+ansi.Truncate(plain[1:], maxInt(0, innerWidth-1), "")))
				continue
			}
			line := "  " + statsNameStyle.Render(fmt.Sprintf(
				"%-*s", nameWidth, clipRunesEllipsis(cleanField(container.Name), nameWidth),
			)) + " " + statsImageStyle.Render(fmt.Sprintf(
				"%-*s", imageWidth, clipRunesEllipsis(cleanField(container.Image), imageWidth),
			)) + " " + statsCPUStyle.Render(fmt.Sprintf("%7s", cpu)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%8s", formatSize(int64(container.MemoryBytes)))) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%8s", limit)) +
				" " + statsMemoryStyle.Render(fmt.Sprintf("%6s", memoryPercent))
			lines = append(lines, fillLine(line, innerWidth))
		}
	}
	if len(lines) == 1 && len(lines) < innerHeight {
		message := "  waiting for first sample…"
		switch {
		case model.statsSubtab == StatsDocker && model.stats.DockerUnsupported:
			message = "  Docker stats read Linux cgroups; this host has none"
		case model.statsSampled():
			message = "  no live rows"
		}
		lines = append(lines, dimStyle.Render(fillLine(message, innerWidth)))
	}
	for len(lines) < innerHeight {
		lines = append(lines, strings.Repeat(" ", innerWidth))
	}
	return framePanel(title, lines, width)
}

// statsSampled reports whether any sample has landed: before one, every
// reading and every empty list is pending, never zero or none.
func (model Model) statsSampled() bool {
	return model.stats.Ready || model.stats.SampleTime != 0
}

// statsNameWidth sizes a NAME column to its longest name, between the
// header's width and limit, so short names leave no dead gap and long ones
// truncate only when the line truly has no room.
func statsNameWidth(names []string, limit int) int {
	width := 8
	for _, name := range names {
		width = max(width, len([]rune(cleanField(name))))
	}
	return min(width, limit)
}
