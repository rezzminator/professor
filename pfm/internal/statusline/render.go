package statusline

import (
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/sky"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

const (
	green   = "\x1b[1;32m"
	yellow  = "\x1b[1;33m"
	red     = "\x1b[1;31m"
	cyan    = "\x1b[1;36m"
	blue    = "\x1b[1;34m"
	magenta = "\x1b[1;35m"
	dim     = "\x1b[2m"
	white   = "\x1b[1;37m"
	reset   = "\x1b[0m"
	sep     = " " + dim + "│" + reset + " "
)

type input struct {
	Model struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir  string `json:"current_dir"`
		GitWorktree string `json:"git_worktree"`
	} `json:"workspace"`
	CWD           string `json:"cwd"`
	ContextWindow struct {
		UsedPercentage    float64 `json:"used_percentage"`
		TotalInputTokens  int64   `json:"total_input_tokens"`
		TotalOutputTokens int64   `json:"total_output_tokens"`
		CurrentUsage      struct {
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			InputTokens              int64 `json:"input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD      float64 `json:"total_cost_usd"`
		TotalDurationMS   int64   `json:"total_duration_ms"`
		TotalLinesAdded   int64   `json:"total_lines_added"`
		TotalLinesRemoved int64   `json:"total_lines_removed"`
	} `json:"cost"`
	Vim struct {
		Mode string `json:"mode"`
	} `json:"vim"`
	Agent struct {
		Name string `json:"name"`
	} `json:"agent"`
	Worktree struct {
		Name string `json:"name"`
	} `json:"worktree"`
	RateLimits rateLimits `json:"rate_limits"`
	Effort     struct {
		Level string `json:"level"`
	} `json:"effort"`
	Thinking struct {
		Enabled bool `json:"enabled"`
	} `json:"thinking"`
	SessionName    string `json:"session_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

type rateWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

type rateLimits struct {
	Windows map[string]rateWindow
	Scoped  []usagehook.ScopedLimit
}

func (limits *rateLimits) UnmarshalJSON(content []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return err
	}
	decoded := rateLimits{Windows: make(map[string]rateWindow, 2)}
	for _, key := range []string{"five_hour", "seven_day"} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		var window rateWindow
		if err := json.Unmarshal(value, &window); err != nil {
			return fmt.Errorf("decode %s rate limit: %w", key, err)
		}
		decoded.Windows[key] = window
	}
	if value, ok := raw["limits"]; ok {
		if err := json.Unmarshal(value, &decoded.Scoped); err != nil {
			return fmt.Errorf("decode scoped rate limits: %w", err)
		}
	}
	*limits = decoded
	return nil
}

func (limits rateLimits) windowsAt(now time.Time, runtime Runtime, account int) (map[string]rateWindow, error) {
	windows := make(map[string]rateWindow, len(limits.Windows)+1)
	for key, window := range limits.Windows {
		windows[key] = window
	}
	usage := usagehook.Usage{Limits: limits.Scoped}
	for _, named := range usage.NamedWindowsAt(now) {
		if named.Key != "seven_day_fable" || named.Window.Utilization == nil {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339, named.Window.ResetsAt)
		if err != nil {
			return nil, fmt.Errorf("decode %s reset: %w", named.Key, err)
		}
		windows[named.Key] = rateWindow{
			UsedPercentage: *named.Window.Utilization,
			ResetsAt:       resetAt.Unix(),
		}
	}
	// The harness's own `limits` array — when it sent one at all — is
	// authoritative for the Fable window; pfm's usage cache is a fallback for
	// a harness that omitted the array entirely (limits.Scoped is nil only
	// when the payload's "limits" key was absent, never when json decoded a
	// present-but-empty array), not a second opinion on a payload that DID
	// report and simply carries no Fable entry for this account.
	if limits.Scoped == nil && account > 0 {
		if fable, ok := usagehook.CachedFableWindow(
			runtime.CacheDir,
			runtime.UID,
			account,
			runtime.ConfigDir,
			now,
		); ok {
			if resetAt, err := time.Parse(time.RFC3339, fable.ResetsAt); err == nil {
				windows["seven_day_fable"] = rateWindow{
					UsedPercentage: *fable.Utilization,
					ResetsAt:       resetAt.Unix(),
				}
			}
			// A cache entry whose resets_at fails to parse degrades to
			// absent rather than failing the render — a cache-read problem
			// must never propagate out of Render (unlike a malformed harness
			// payload above, which is a hard decode error).
		}
	}
	return windows, nil
}

// Render is the native high-frequency path. It performs no network work and
// never waits for a refresher: stale caches only arm detached children through
// Runtime.Spawn.
func Render(ctx context.Context, raw []byte, runtime Runtime) (string, error) {
	runtime = runtime.normalized()
	var data input
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", fmt.Errorf("decode statusline input: %w", err)
	}
	if data.Model.DisplayName == "" {
		data.Model.DisplayName = pfmengine.MustLookup(pfmengine.Claude).Short
	}
	directory := data.Workspace.CurrentDir
	if directory == "" {
		directory = data.CWD
	}
	now := runtime.now()
	// account is resolved here, ahead of windowsAt, because the Fable
	// window's cache fallback needs to know which account's cache file to
	// read; accountBadge is pure and side-effect-free, so calling it before
	// its other use below duplicates no logic, only the (cheap) lookup.
	badge, account := accountBadge(runtime)
	resolvedLimits, err := data.RateLimits.windowsAt(now, runtime, account)
	if err != nil {
		return "", fmt.Errorf("decode statusline rate limits: %w", err)
	}
	data.RateLimits.Windows = resolvedLimits
	data.RateLimits.Scoped = nil
	writeBreadcrumb(runtime, data.TranscriptPath)
	convergeWindowName(ctx, runtime, data)

	harvestRateLimits(runtime, now, account, data)

	modelSymbol := "●"
	switch {
	case strings.Contains(data.Model.DisplayName, "Fable"):
		modelSymbol = "✦"
	case strings.Contains(data.Model.DisplayName, "Opus"):
		modelSymbol = "◆"
	case strings.Contains(data.Model.DisplayName, "Sonnet"):
		modelSymbol = "◇"
	case strings.Contains(data.Model.DisplayName, "Haiku"):
		modelSymbol = "○"
	}
	directoryName := filepath.Base(filepath.Clean(directory))
	if directory == "" || directoryName == string(filepath.Separator) {
		directoryName = "~"
	}

	l1 := badge + cyan + modelSymbol + " " + data.Model.DisplayName + reset
	if data.SessionName != "" {
		l1 += sep + white + "🔖 " + data.SessionName + reset
	}
	if effort := effortSegment(runtime, data); effort != "" {
		l1 += sep + effort
	}
	l1 += sep + blue + directoryName + reset
	if data.Worktree.Name != "" {
		l1 += sep + magenta + "🌳 " + data.Worktree.Name + reset
	} else if data.Workspace.GitWorktree != "" {
		l1 += sep + magenta + "🌳 " + data.Workspace.GitWorktree + reset
	}
	if git := gitSegment(ctx, runtime, directory); git != "" {
		l1 += sep + git
	}
	if data.Agent.Name != "" {
		l1 += sep + magenta + "⚡" + data.Agent.Name + reset
	}
	if data.Vim.Mode != "" {
		color := green
		if data.Vim.Mode == "NORMAL" {
			color = cyan
		}
		l1 += sep + color + data.Vim.Mode + reset
	}
	counts := fleetCounts(runtime)
	l1 += sep + sky.SnapshotCounts(counts)

	gauge, l2, contextTokens := renderContextLine(runtime, data, directory, now)
	if data.Cost.TotalCostUSD > 0 && runtime.Engine != pfmengine.Codex {
		color := dim
		if data.Cost.TotalCostUSD >= 10 {
			color = red
		} else if data.Cost.TotalCostUSD >= 2 {
			color = yellow
		}
		l2 += sep + color + "💰" + fmt.Sprintf("$%.2f", data.Cost.TotalCostUSD) + reset
	}
	// ⏳ (U+23F3, East-Asian-Width W) over ⏱ (U+23F1, width N): every cell
	// model — tmux, xterm.js, the harness — sizes the hourglass at 2 cells,
	// while the stopwatch is 1 cell wide on paper and 2 cells wide in ink.
	l2 += sep + dim + "⏳ " + formatDuration(data.Cost.TotalDurationMS) + reset

	l3 := ""
	if runtime.Engine == pfmengine.Codex {
		codexLine, replacement := codexSegment(runtime, now, contextTokens, l2, gauge.transcript)
		if replacement != "" {
			l2 = replacement
		}
		l3 = appendSegment(l3, codexLine)
	}
	l3 = appendRateSegments(l3, now, data)

	if l3 != "" {
		return reset + l1 + "\n" + reset + l2 + "\n" + reset + l3 + "\n", nil
	}
	return reset + l1 + "\n" + reset + l2 + "\n", nil
}

func effortSegment(_ Runtime, data input) string {
	if data.Effort.Level == "" {
		return ""
	}
	color, emoji := cyan, "🔆"
	switch data.Effort.Level {
	case "low":
		color, emoji = dim, "🔹"
	case "medium":
		color, emoji = green, "🔶"
	case "high":
		color, emoji = yellow, "💠"
	case "xhigh":
		color, emoji = magenta, "💎"
	case "max":
		color, emoji = red, "👑"
	}
	if !data.Thinking.Enabled {
		return dim + "💤 " + data.Effort.Level + " (off)" + reset
	}
	return color + emoji + " " + data.Effort.Level + reset
}

func formatDuration(milliseconds int64) string {
	seconds := milliseconds / 1000
	switch {
	case seconds >= 3600:
		return fmt.Sprintf("%dh%dm", seconds/3600, seconds%3600/60)
	case seconds >= 60:
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func urgencyEmoji(percent int) string {
	switch {
	case percent >= 95:
		return "🚨"
	case percent >= 80:
		return "🔥"
	case percent >= 50:
		return "⚡"
	default:
		return "🟢"
	}
}

func percentColor(percent int) string {
	if percent >= 80 {
		return red
	}
	if percent >= 50 {
		return yellow
	}
	return green
}

func makeBar(percent, width int) string {
	filled := percent * width / 100
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	// ▰▱ (U+25B0/U+25B1) on purpose — block elements ▓░ are WebGL custom
	// glyphs in VS Code's terminal and render stale under repaint; the
	// pfm-statusline patcher's GAUGE regex matches these runs.
	return percentColor(percent) + strings.Repeat("▰", filled) + dim +
		strings.Repeat("▱", width-filled) + reset
}

func formatContextTokens(tokens int64) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%d.%dM", tokens/1_000_000, tokens%1_000_000/100_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%d.%dK", tokens/1_000, tokens%1_000/100)
	default:
		return strconv.FormatInt(tokens, 10)
	}
}

func appendSegment(line, segment string) string {
	if segment == "" {
		return line
	}
	if line == "" {
		return segment
	}
	return line + sep + segment
}

func appendRateSegments(line string, now time.Time, data input) string {
	// A response carrying no windows at all is an engine that does not report
	// these quotas (Codex renders its own from a different source) — not a
	// missing reading, so it renders nothing.
	if len(data.RateLimits.Windows) == 0 {
		return line
	}
	// Otherwise every known window renders, always. Skipping a window at 0%
	// made an unused quota and a quota whose data never arrived look
	// identical — both simply absent — so the one state worth seeing (a limit
	// the response stopped reporting) arrived as silence. A window missing
	// from a block that DID report renders with an explicit unknown marker.
	for _, descriptor := range usagehook.AllWindows() {
		window, present := data.RateLimits.Windows[descriptor.Key]
		if !present {
			line = appendSegment(line, makeBar(0, 5)+" "+dim+descriptor.Label+"-used:—"+reset)
			continue
		}
		used := int(window.UsedPercentage)
		segment := makeBar(used, 5) + " " + percentColor(used) +
			descriptor.Label + "-used:" + strconv.Itoa(used) + "%" + reset
		remaining := window.ResetsAt - now.Unix()
		if remaining > 0 {
			if descriptor.Key == "five_hour" {
				segment += fmt.Sprintf(" %s↻%dh%dm%s", dim, remaining/3600, remaining%3600/60, reset)
			} else {
				segment += fmt.Sprintf(" %s↻%dd%dh%s", dim, remaining/86400, remaining%86400/3600, reset)
			}
		}
		line = appendSegment(line, segment)
	}
	return line
}

func gitSegment(ctx context.Context, runtime Runtime, directory string) string {
	if directory == "" {
		return ""
	}
	if err := os.MkdirAll(runtime.CacheDir, 0o700); err != nil {
		return ""
	}
	digest := md5.Sum([]byte(directory))
	cachePath := filepath.Join(runtime.CacheDir, "cc-sl-"+hex.EncodeToString(digest[:]))
	stale := true
	if info, err := os.Stat(cachePath); err == nil {
		stale = runtime.now().Sub(info.ModTime()) > 5*time.Second
	}
	if stale {
		commandContext, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		branch, err := runtime.Command.Output(
			commandContext,
			"git",
			"-C", directory, "--no-optional-locks", "symbolic-ref", "--short", "HEAD",
		)
		cancel()
		content := ""
		if err == nil && strings.TrimSpace(string(branch)) != "" {
			staged := gitDiffCount(ctx, runtime, directory, true)
			modified := gitDiffCount(ctx, runtime, directory, false)
			content = fmt.Sprintf("%s|%d|%d", strings.TrimSpace(string(branch)), staged, modified)
		}
		_ = atomicfile.Write(cachePath, []byte(content), 0o600)
	}
	body, err := os.ReadFile(cachePath)
	if err != nil || len(body) == 0 {
		return ""
	}
	fields := strings.Split(strings.TrimSpace(string(body)), "|")
	if len(fields) != 3 || fields[0] == "" {
		return ""
	}
	staged, _ := strconv.Atoi(fields[1])
	modified, _ := strconv.Atoi(fields[2])
	color, suffix := green, ""
	if staged > 0 {
		suffix += fmt.Sprintf(" %s+%d", green, staged)
		color = yellow
	}
	if modified > 0 {
		suffix += fmt.Sprintf(" %s~%d", yellow, modified)
		color = yellow
	}
	return color + "🌿 " + fields[0] + suffix + reset
}

func gitDiffCount(ctx context.Context, runtime Runtime, directory string, staged bool) int {
	args := []string{"-C", directory, "--no-optional-locks", "diff"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--numstat")
	commandContext, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	output, err := runtime.Command.Output(commandContext, "git", args...)
	if err != nil {
		return 0
	}
	return len(strings.FieldsFunc(strings.TrimSpace(string(output)), func(r rune) bool { return r == '\n' }))
}

func accountBadge(runtime Runtime) (string, int) {
	if strings.TrimSpace(runtime.ConfigDir) == "" {
		return "", 0
	}
	configDir := canonicalRuntimePath(runtime.ConfigDir)
	account := 0
	for directory, id := range runtime.AccountDirs {
		if configDir == canonicalRuntimePath(directory) {
			account = id
			break
		}
	}
	if account == 0 {
		if len(runtime.AccountDirs) == 0 {
			if legacy, err := strconv.Atoi(filepath.Base(configDir)); err == nil && legacy > 0 {
				return accountBadgeForID(runtime, legacy)
			}
		}
		return "", 0
	}
	return accountBadgeForID(runtime, account)
}

func canonicalRuntimePath(path string) string {
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved)
	}
	return cleaned
}

func accountBadgeForID(runtime Runtime, account int) (string, int) {
	if runtime.AccountEmojis != nil {
		if emoji := runtime.AccountEmojis[account]; emoji != "" && emoji != "·" {
			return emoji + " ", account
		}
		return "", 0
	}
	if emoji := pfmconfig.DefaultEmoji(account); emoji != "·" {
		return emoji + " ", account
	}
	return "", 0
}

func fleetCounts(runtime Runtime) map[pfmengine.ID]int {
	allowProbe := runtime.getenv("PFM_TEST_PROBE_SOCKETS") == "1"
	counts := make(map[pfmengine.ID]int)
	count := func(name string) {
		id, ok := pfmengine.FromSocket(name)
		if !ok && allowProbe {
			id, ok = pfmengine.FromSocket(strings.TrimPrefix(name, "probe-"))
		}
		if ok {
			counts[id]++
		}
	}
	if body, err := os.ReadFile(filepath.Join(runtime.ProcRoot, "net", "unix")); err == nil {
		seen := map[string]bool{}
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}
			path := fields[len(fields)-1]
			if filepath.Clean(filepath.Dir(path)) != filepath.Clean(runtime.TmuxDir) {
				continue
			}
			name := filepath.Base(path)
			if seen[name] {
				continue
			}
			seen[name] = true
			count(name)
		}
		return counts
	}
	entries, err := os.ReadDir(runtime.TmuxDir)
	if err != nil {
		return counts
	}
	for _, entry := range entries {
		count(entry.Name())
	}
	return counts
}

// windowConvergeTimeout bounds the whole convergence. The statusline renders
// every 3 seconds and a render must never wait on tmux; a server that does not
// answer inside this budget simply keeps its name until the next label change
// or the name-sync backstop.
const windowConvergeTimeout = 750 * time.Millisecond

// convergeWindowName applies a claude /rename to the chat's tmux WINDOW name
// the moment its own statusline renders the new 🔖 label.
//
// The chain /rename -> 🔖 label -> window name -> terminal tab used to have
// exactly one scheduler behind it: the 15-minute name-sync poll. Codex renames
// fired instantly (a path unit watches session_index.jsonl) and claude renames
// did not, so one runtime looked broken to the person using it. The label is
// known here continuously; only its application was on a timer.
//
// It is the SAME operation name-sync performs — gather.RenameWindow on
// gather.WindowNameFor(label) — so the two can never disagree about the name,
// and the timer remains the backstop for everything this path cannot see (a
// chat whose statusline is not rendering, a name a second writer took back).
//
// Cost discipline: a converged chat forks NOTHING. The last applied label is
// cached per session beside the cache-window anchor, and a render whose label
// still matches the cache returns before touching tmux at all.
func convergeWindowName(ctx context.Context, runtime Runtime, data input) {
	// Claude only: a codex window follows its thread's indexed name, applied
	// by name-sync's own half, and a subagent's statusline is not the window's
	// identity — the main chat owns that name.
	if runtime.Engine != pfmengine.Claude || data.Agent.Name != "" {
		return
	}
	label := gather.WindowNameFor(data.SessionName)
	if label == "" {
		return
	}
	socket, ok := pfmSocket(runtime)
	if !ok {
		return
	}
	pane := runtime.getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	key := data.SessionID
	if key == "" {
		key = socket
	}
	cachePath := filepath.Join(runtime.CacheDir, "cc-sl-window-"+key)
	if cached, err := os.ReadFile(cachePath); err == nil && string(cached) == label {
		return
	}
	commandContext, cancel := context.WithTimeout(ctx, windowConvergeTimeout)
	defer cancel()
	socketPath := filepath.Join(runtime.TmuxDir, socket)
	// window_panes gates the rename the same way gather's claude half does: a
	// window hosting two /chat:branch siblings cannot carry both labels, so it
	// keeps the name it has rather than take whichever sibling rendered last.
	read, err := runtime.Command.Output(
		commandContext, "tmux", "-S", socketPath,
		"display-message", "-t", pane, "-p", "#{window_panes} #{window_name}",
	)
	if err != nil {
		return
	}
	// A SPACE separator, split at the first one: display-message renders a
	// control byte as its octal escape (a \x1f separator arrives as the four
	// literal characters \037), and a pane count never contains a space while
	// a window name may.
	panes, current, found := strings.Cut(strings.TrimRight(string(read), "\n"), " ")
	if !found || panes != "1" {
		return
	}
	if current != label {
		rename := gather.WindowRename{
			Socket: socket, WindowID: pane, CurrentName: current, TargetName: label,
		}
		client := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(runtime.TmuxDir)}
		if err := client.RenameWindow(commandContext, rename); err != nil {
			return
		}
	}
	// Written only after the window provably carries the label, so a failed
	// rename is retried on the next render instead of being cached as done.
	_ = atomicfile.Write(cachePath, []byte(label), 0o600)
}

// pfmSocket resolves the fleet socket this render is running inside, or
// reports that it is not in one. It applies the same probe-socket allowance
// fleetCounts does so a jailed fixture can exercise the path.
func pfmSocket(runtime Runtime) (string, bool) {
	tmux := runtime.getenv("TMUX")
	if tmux == "" {
		return "", false
	}
	socketPath := strings.SplitN(tmux, ",", 2)[0]
	if filepath.Clean(filepath.Dir(socketPath)) != filepath.Clean(runtime.TmuxDir) {
		return "", false
	}
	socket := filepath.Base(socketPath)
	if id, ok := pfmengine.FromSocket(socket); ok && id == pfmengine.Claude {
		return socket, true
	}
	if runtime.getenv("PFM_TEST_PROBE_SOCKETS") == "1" {
		if id, ok := pfmengine.FromSocket(strings.TrimPrefix(socket, "probe-")); ok && id == pfmengine.Claude {
			return socket, true
		}
	}
	return "", false
}

func writeBreadcrumb(runtime Runtime, transcriptPath string) {
	tmux := runtime.getenv("TMUX")
	if tmux == "" || transcriptPath == "" {
		return
	}
	socket := strings.SplitN(tmux, ",", 2)[0]
	socket = filepath.Base(socket)
	if socket == "." || socket == "" {
		return
	}
	if err := os.MkdirAll(runtime.SIDDir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(runtime.SIDDir, 0o700)
	_ = atomicfile.Write(filepath.Join(runtime.SIDDir, socket), []byte(transcriptPath), 0o600)
	if pane := runtime.getenv("TMUX_PANE"); pane != "" {
		_ = atomicfile.Write(filepath.Join(runtime.SIDDir, socket+"."+pane), []byte(transcriptPath), 0o600)
	}
}

func harvestRateLimits(runtime Runtime, now time.Time, account int, data input) {
	fiveWindow := data.RateLimits.Windows["five_hour"]
	sevenWindow := data.RateLimits.Windows["seven_day"]
	five := int(fiveWindow.UsedPercentage)
	seven := int(sevenWindow.UsedPercentage)
	fableWindow, hasFable := data.RateLimits.Windows["seven_day_fable"]
	hasFable = hasFable && fableWindow.ResetsAt > now.Unix()
	if ((five <= 0 && seven <= 0) || fiveWindow.ResetsAt <= now.Unix()) && !hasFable {
		return
	}
	if err := os.MkdirAll(runtime.RateLimitDir, 0o700); err != nil {
		return
	}
	sessionID := data.SessionID
	if sessionID == "" {
		sessionID = "anon"
	}
	payload := map[string]any{
		"acct":                int64(account),
		"config_dir":          filepath.Clean(runtime.ConfigDir),
		"five_hour_used":      int64(five),
		"seven_day_used":      int64(seven),
		"five_hour_resets_at": fiveWindow.ResetsAt,
		"seven_day_resets_at": sevenWindow.ResetsAt,
		"ts":                  now.Unix(),
	}
	// windowsAt has already folded the scoped `limits` array into this map, so
	// the Fable window is available here exactly like the two flat ones. It is
	// recorded whenever it is present and still in the future — 0% used is a
	// real reading, not a missing one — because a reader that has fallen back
	// to this file can render no Fable window this writer did not carry. That
	// fallback is no longer the only door (usagehook reads the OS keychain
	// directly now), but it is still the one a signed-out or unreachable
	// account depends on.
	if hasFable {
		payload["fable_used"] = int64(fableWindow.UsedPercentage)
		payload["fable_resets_at"] = fableWindow.ResetsAt
	}
	body, err := json.Marshal(payload)
	if err == nil {
		body = append(body, '\n')
		_ = atomicfile.Write(
			filepath.Join(runtime.RateLimitDir, fmt.Sprintf("acct-%d.%s.json", account, sessionID)),
			body,
			0o600,
		)
	}
	entries, _ := os.ReadDir(runtime.RateLimitDir)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), fmt.Sprintf("acct-%d.", account)) ||
			!strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && now.Sub(info.ModTime()) > time.Hour {
			_ = os.Remove(filepath.Join(runtime.RateLimitDir, entry.Name()))
		}
	}
}

func cacheWindowSegment(runtime Runtime, now time.Time, transcriptPath string) string {
	ttl := time.Hour
	label := "1h"
	if runtime.getenv("FORCE_PROMPT_CACHING_5M") == "1" {
		ttl = 5 * time.Minute
		label = "5m"
	}
	// A transcript we could not read is NOT a chat without a cache window, and
	// the two must never share a rendering. Returning "" here made the segment
	// disappear, which is indistinguishable from a statusline that has no cache
	// timer at all — so the one state worth shouting about, a chat running with
	// transcript saving off, arrived as silence. That chat cannot be resumed and
	// its window cannot be measured; the statusline is where the user finds out.
	//
	// "!" is deliberately not "?": "?" means the transcript WAS read and simply
	// carries no user turn to anchor on, which is a fact about the chat. "!" is
	// a fact about us — we could not look.
	if transcriptPath == "" {
		return sep + red + "💾" + label + "!" + reset
	}
	info, err := os.Stat(transcriptPath)
	if err != nil || info.IsDir() {
		return sep + red + "💾" + label + "!" + reset
	}
	cachePath := filepath.Join(
		runtime.CacheDir,
		"cc-sl-anchor-"+strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl"),
	)
	key := fmt.Sprintf("%d:%d", info.ModTime().Unix(), info.Size())
	cachedKey, anchor := readAnchorCache(cachePath)
	if cachedKey != key {
		anchor = cacheAnchor(transcriptPath)
		encoded := "-"
		if !anchor.IsZero() {
			encoded = strconv.FormatInt(anchor.Unix(), 10)
		}
		_ = atomicfile.Write(cachePath, []byte(key+" "+encoded), 0o600)
	}
	if anchor.IsZero() {
		if label == "1h" {
			return sep + yellow + "💾1h∞" + reset
		}
		return sep + yellow + "💾" + label + "?" + reset
	}
	remaining := ttl - now.Sub(anchor)
	if remaining > 0 {
		return sep + green + "💾" + label + "✓" + formatCacheTime(remaining, false) + reset
	}
	return sep + red + "💾" + label + "✗" + formatCacheTime(-remaining, true) + reset
}

func readAnchorCache(path string) (string, time.Time) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}
	}
	fields := strings.Fields(string(body))
	if len(fields) != 2 || fields[1] == "-" {
		if len(fields) == 2 {
			return fields[0], time.Time{}
		}
		return "", time.Time{}
	}
	epoch, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", time.Time{}
	}
	return fields[0], time.Unix(epoch, 0)
}

// cacheAnchor is the moment the prompt cache was last written or refreshed:
// the newest main-chain record that WAS an API request or its reply — an
// assistant record, or a user record other than a local slash command's
// transcript echo. Local commands (/rc, /cost, the /compact receipt …) write
// user-typed records without making a request, and anchoring on them showed a
// cache twelve hours cold as "expired 9m ago". Sidechains refresh their own
// cache, never the main chat's.
func cacheAnchor(path string) time.Time {
	for _, size := range []int64{65_536, 1_048_576} {
		body, err := readTail(path, size)
		if err != nil {
			continue
		}
		var newest time.Time
		scanner := bufio.NewScanner(strings.NewReader(string(body)))
		scanner.Buffer(make([]byte, 64*1024), int(size)+1)
		for scanner.Scan() {
			var record struct {
				Type      string `json:"type"`
				Sidechain bool   `json:"isSidechain"`
				Timestamp string `json:"timestamp"`
				Message   struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Sidechain || record.Timestamp == "" {
				continue
			}
			switch record.Type {
			case "assistant":
			case "user":
				if localCommandRecord(record.Message.Content) {
					continue
				}
			default:
				continue
			}
			parsed, parseErr := time.Parse(time.RFC3339Nano, record.Timestamp)
			if parseErr == nil && parsed.After(newest) {
				newest = parsed
			}
		}
		if !newest.IsZero() {
			return newest
		}
	}
	return time.Time{}
}

// localCommandRecord recognises the transcript echo of a local slash command:
// string content opening with one of Claude Code's local-command tags. Array
// content is a real turn (tool results, content blocks) and always counts.
func localCommandRecord(content json.RawMessage) bool {
	var text string
	if json.Unmarshal(content, &text) != nil {
		return false
	}
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<local-command-") || strings.HasPrefix(trimmed, "<command-name>")
}

func readTail(path string, size int64) (tail []byte, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close tail file %s: %w", path, err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	start := info.Size() - size
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

func formatCacheTime(duration time.Duration, expired bool) string {
	seconds := int64(duration / time.Second)
	if seconds < 0 {
		seconds = -seconds
	}
	if seconds >= 3600 {
		if expired {
			return fmt.Sprintf("%dh:%dm", seconds/3600, seconds%3600/60)
		}
		return fmt.Sprintf("%dh:%dm:%ds", seconds/3600, seconds%3600/60, seconds%60)
	}
	if seconds >= 60 {
		return fmt.Sprintf("%dm:%ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func statuslineFileAge(path string, now time.Time) time.Duration {
	info, err := os.Stat(path)
	if err != nil {
		return 100 * 365 * 24 * time.Hour
	}
	return now.Sub(info.ModTime())
}

func armRefresh(runtime Runtime, kind RefreshKind, cachePath string, ttl time.Duration) {
	if runtime.Spawn == nil || statuslineFileAge(cachePath, runtime.now()) <= ttl {
		return
	}
	lockPath := strings.TrimSuffix(cachePath, ".json") + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return
	}
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	_ = lock.Close()
	if err := runtime.Spawn(kind); err != nil {
		_ = os.Remove(lockPath)
	}
}

type codexUsageCache struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	PlanType  string       `json:"planType"`
}

type codexWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int64   `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

func codexSegment(
	runtime Runtime,
	now time.Time,
	contextTokens int64,
	currentL2 string,
	transcriptGauge bool,
) (string, string) {
	model := runtime.getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "gpt-5.6-sol"
	}
	model = strings.TrimSuffix(model, "[1m]")
	segment := green + "🍀 " + model + reset
	procTCP, err := os.ReadFile(filepath.Join(runtime.ProcRoot, "net", "tcp"))
	if err == nil && strings.Contains(strings.ToUpper(string(procTCP)), ":494D ") {
		segment += sep + dim + "⇅ proxy" + reset
	} else {
		segment += sep + red + "⇅ proxy DOWN" + reset
	}
	requests, authReject := codexRequestCount(runtime, now)
	if requests > 0 {
		segment += sep + dim + "↻ " + strconv.Itoa(requests) + " today" + reset
	}
	if authReject {
		segment += sep + red + "⚠ auth-reject streak — WS upgrade refused; CCP_CODEX_TRANSPORT=http" + reset
	}
	usagePath := filepath.Join(runtime.CacheDir, fmt.Sprintf("cc-gpt-usage-%d.json", runtime.UID))
	armRefresh(runtime, RefreshKindCodex, usagePath, 5*time.Minute)
	usageBody, usageErr := os.ReadFile(usagePath)
	var usage codexUsageCache
	if usageErr == nil && json.Unmarshal(usageBody, &usage) == nil {
		for _, window := range []*codexWindow{usage.Primary, usage.Secondary} {
			if window == nil || window.UsedPercent < 0 {
				continue
			}
			percent := int(window.UsedPercent)
			segment += sep + makeBar(percent, 5) + " " + percentColor(percent) +
				windowLabel(window.WindowDurationMins) + "-used:" + strconv.Itoa(percent) + "%" + reset +
				resetCountdown(now, window.ResetsAt)
		}
		if usage.PlanType != "" {
			segment += sep + dim + "ChatGPT " + usage.PlanType + reset
		}
	} else {
		segment += sep + dim + "ChatGPT subscription" + reset
	}

	replacement := ""
	window := int64(0)
	baseModel := strings.TrimSuffix(model, "-fast")
	if strings.HasPrefix(baseModel, "gpt-5.6-") {
		window = 272_000
	} else {
		window, _ = strconv.ParseInt(runtime.getenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW"), 10, 64)
	}
	if !transcriptGauge && window > 0 && contextTokens > 0 {
		percent := int(contextTokens * 100 / window)
		if percent > 100 {
			percent = 100
		}
		rest := ""
		if index := strings.Index(currentL2, sep); index >= 0 {
			rest = currentL2[index:]
		}
		replacement = urgencyEmoji(percent) + " " + makeBar(percent, 10) + " " +
			percentColor(percent) + strconv.Itoa(percent) + "%" + reset + " " + dim +
			"of " + formatContextTokens(window) + reset + rest
	}
	return segment, replacement
}

func codexRequestCount(runtime Runtime, now time.Time) (int, bool) {
	cachePath := filepath.Join(runtime.CacheDir, "cc-sl-gptreq")
	if statuslineFileAge(cachePath, now) > 30*time.Second {
		logPath := filepath.Join(runtime.Home, ".local", "state", "claude-code-proxy", "proxy.log")
		file, err := os.Open(logPath)
		if err == nil {
			defer func() {
				if err := file.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "statusline: close %s proxy log %s: %v\n",
						pfmengine.MustLookup(pfmengine.Codex).Short, logPath, err)
				}
			}()
			today := now.UTC().Format("2006-01-02")
			count := 0
			last := make([]int, 0, 3)
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				var row struct {
					Message string `json:"msg"`
					Time    string `json:"t"`
					Status  int    `json:"status"`
				}
				if json.Unmarshal(scanner.Bytes(), &row) != nil {
					continue
				}
				if row.Message != "request_completed" {
					continue
				}
				if strings.HasPrefix(row.Time, today) {
					count++
				}
				last = append(last, row.Status)
				if len(last) > 3 {
					last = last[len(last)-3:]
				}
			}
			reject := len(last) == 3
			for _, status := range last {
				reject = reject && (status == 401 || status == 403)
			}
			_ = atomicfile.Write(cachePath, []byte(fmt.Sprintf("%d\t%d\n", count, boolDigit(reject))), 0o600)
		}
	}
	body, err := os.ReadFile(cachePath)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(body))
	if len(fields) != 2 {
		return 0, false
	}
	count, _ := strconv.Atoi(fields[0])
	reject, _ := strconv.Atoi(fields[1])
	return count, reject == 1
}

func boolDigit(value bool) int {
	if value {
		return 1
	}
	return 0
}

func windowLabel(minutes int64) string {
	switch {
	case minutes >= 1440:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes >= 60:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func resetCountdown(now time.Time, resetsAt int64) string {
	remaining := resetsAt - now.Unix()
	if remaining <= 0 {
		return ""
	}
	if remaining >= 86400 {
		return fmt.Sprintf(" %s↻%dd%dh%s", dim, remaining/86400, remaining%86400/3600, reset)
	}
	return fmt.Sprintf(" %s↻%dh%dm%s", dim, remaining/3600, remaining%3600/60, reset)
}
