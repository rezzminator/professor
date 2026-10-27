package statusline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
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
		ContextWindowSize int64   `json:"context_window_size"`
		PercentageKnown   bool    `json:"-"`
		TotalInputTokens  int64   `json:"total_input_tokens"`
		TotalOutputTokens int64   `json:"total_output_tokens"`
		CurrentUsage      struct {
			Known                    bool  `json:"-"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			InputTokens              int64 `json:"input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`
	// PromptCache is Claude Code's own cache tracker (2.1.25x+): the TTL its
	// newest request used and when that cache expires. Absent before the
	// first request and on older builds.
	PromptCache *promptCache `json:"prompt_cache"`
	Cost        struct {
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
	Cached         bool    `json:"-"`
	Failure        bool    `json:"-"`
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
	// Every window the harness sends is kept (five_hour, seven_day, a
	// gateway's spend_limit, the undocumented per-model weeklies) so the quota
	// snapshot can carry them all. Any window may be absent or null, and a key
	// that is no window at all (model_scoped is an array) is skipped: only a
	// malformed five_hour or seven_day, the two the render reads, fails it.
	decoded := rateLimits{Windows: make(map[string]rateWindow, len(raw))}
	for key, value := range raw {
		if key == "limits" || string(value) == "null" {
			continue
		}
		var window rateWindow
		if err := json.Unmarshal(value, &window); err != nil {
			if key == "five_hour" || key == "seven_day" {
				return fmt.Errorf("decode %s rate limit: %w", key, err)
			}
			continue
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
	// A reported window is authoritative, even at zero. Only missing
	// windows use the shared usage cache; an explicit scoped array also owns
	// the absence of Fable, independently of missing main windows.
	missing := make(map[string]bool)
	for _, descriptor := range usagehook.AllWindows() {
		if _, present := windows[descriptor.Key]; !present &&
			(descriptor.Key != "seven_day_fable" || limits.Scoped == nil) {
			missing[descriptor.Key] = true
		}
	}
	if account > 0 && len(missing) > 0 {
		fillCachedRateWindows(now, runtime, account, windows, missing)
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
	if err := validateInput(data); err != nil {
		return "", fmt.Errorf("invalid statusline input: %w", err)
	}
	if data.Model.ID == "" {
		data.Model.ID = runtime.getenv("ANTHROPIC_MODEL")
	}
	if data.Model.DisplayName == "" ||
		(openAIModel(data.Model.ID) && data.Model.DisplayName == pfmengine.MustLookup(pfmengine.Claude).Short) {
		data.Model.DisplayName = modelFamily(data.Model.ID)
		if data.Model.DisplayName == "" {
			data.Model.DisplayName = pfmengine.MustLookup(runtime.Engine).Short
		}
	}
	directory := data.Workspace.CurrentDir
	if directory == "" {
		directory = data.CWD
	}
	now := runtime.now()
	// account is resolved here, ahead of windowsAt, because the usage
	// windows' cache fallback needs to know which account's cache file to
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

	recordRateLimits(runtime, now, account, data)

	directoryName := filepath.Base(filepath.Clean(directory))
	if directory == "" || directoryName == string(filepath.Separator) {
		directoryName = "~"
	}

	gauge, l2, _ := renderContextLine(runtime, data, directory, now)
	if data.Model.ID == "" && gauge.model != "" {
		data.Model.ID = gauge.model
		data.Model.DisplayName = modelFamily(gauge.model)
	}
	l1 := badge + modelSegment(data, sessionAutoEffort(runtime, data))
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
	if data.SessionName != "" {
		l1 += sep + cLabel + "🔖 " + data.SessionName + reset
	}
	l1 += sep + fleetSegment(runtime)

	// ⏳ (U+23F3, East-Asian-Width W) over ⏱ (U+23F1, width N): every cell
	// model — tmux, xterm.js, the harness — sizes the hourglass at 2 cells,
	// while the stopwatch is 1 cell wide on paper and 2 cells wide in ink.
	l2 += sep + cElapsed + "⏳ " + formatDuration(data.Cost.TotalDurationMS) + reset

	l3 := ""
	switch runtime.Engine {
	case pfmengine.Claude:
		l3 = claudeQuotaRows(runtime, now, data, l1, l2)
	case pfmengine.Codex:
		l3 = appendRateSegments(appendSegment(l3, codexQuotaSegment(runtime, now)), now, data)
	default:
		l3 = appendRateSegments(l3, now, data)
	}

	if l3 != "" {
		return reset + l1 + "\n" + reset + l2 + "\n" + reset + l3 + "\n", nil
	}
	return reset + l1 + "\n" + reset + l2 + "\n", nil
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

// reportsKnownWindow is false for an engine that does not report these quotas
// (Codex renders its own from a different source; a gateway reports only its
// spend_limit) — not a missing reading, so it renders nothing.
func reportsKnownWindow(data input) bool {
	for _, descriptor := range usagehook.AllWindows() {
		if _, present := data.RateLimits.Windows[descriptor.Key]; present {
			return true
		}
	}
	return false
}

// rateWindowRow reads one known window. Every known window renders, always:
// skipping a window at 0% made an unused quota and a quota whose data never
// arrived look identical — both simply absent — so the one state worth seeing
// (a limit the response stopped reporting) arrived as silence. A window missing
// from a block that DID report renders with an explicit unknown marker.
func rateWindowRow(now time.Time, descriptor usagehook.WindowDescriptor, data input) quotaRow {
	window, present := data.RateLimits.Windows[descriptor.Key]
	if !present {
		return quotaRow{label: dim + descriptor.Label + "-used:—" + reset}
	}
	if window.Failure {
		return quotaRow{label: red + descriptor.Label + "-used:ERR" + reset}
	}
	used := gaugePercent(window.UsedPercentage)
	label := percentColor(used) + descriptor.Label + "-used:" + strconv.Itoa(used) + "%" + reset
	remaining := window.ResetsAt - now.Unix()
	if remaining > 0 {
		if descriptor.Key == "five_hour" {
			label += fmt.Sprintf(" %s↻%dh%dm%s", dim, remaining/3600, remaining%3600/60, reset)
		} else {
			label += fmt.Sprintf(" %s↻%dd%dh%s", dim, remaining/86400, remaining%86400/3600, reset)
		}
	}
	return quotaRow{percent: used, hasData: true, label: label}
}

// appendRateSegments renders the known windows as one compact line (the
// non-Claude engines).
func appendRateSegments(line string, now time.Time, data input) string {
	if !reportsKnownWindow(data) {
		return line
	}
	for _, descriptor := range usagehook.AllWindows() {
		line = appendSegment(line, compactRow(rateWindowRow(now, descriptor, data)))
	}
	return line
}

// claudeQuotaRows is the Claude quota block, every row the same width: 5h and
// 7d-fable share the top row at half width each, the 7d under it and the Codex
// weekly last at full width. That width is the widest sibling row (the compact
// forms count as siblings), bounded by the terminal; a terminal with no content
// column drops the Codex row and leaves the quotas at their compact width.
func claudeQuotaRows(runtime Runtime, now time.Time, data input, l1, l2 string) string {
	codex := codexWeeklyRow(runtime, now)
	widths := []int{ansi.StringWidth(codex.label)}
	var five, week, fable quotaRow
	known := reportsKnownWindow(data)
	if known {
		reading := map[string]quotaRow{}
		for _, descriptor := range usagehook.AllWindows() {
			reading[descriptor.Key] = rateWindowRow(now, descriptor, data)
		}
		five, week, fable = reading[fiveHourCacheKey], reading[sevenDayCacheKey], reading[fableCacheKey]
		widths = append(
			widths,
			ansi.StringWidth(compactRow(five)+sep+compactRow(fable)),
			ansi.StringWidth(compactRow(week)),
		)
	}
	columns, ok := rowColumns(runtime, widths, l1, l2)
	var rows []string
	switch {
	case known && ok:
		rows = append(rows, halfRows(five, fable, columns), barRow(week, columns))
	case known:
		rows = append(rows, compactRow(five)+sep+compactRow(fable), compactRow(week))
	}
	if ok {
		rows = append(rows, barRow(codex, columns))
	}
	return strings.Join(rows, "\n"+reset)
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

func fleetCountsWithError(runtime Runtime) (map[pfmengine.ID]int, error) {
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
		return counts, nil
	}
	entries, err := os.ReadDir(runtime.TmuxDir)
	if err != nil {
		return nil, fmt.Errorf("read fleet sockets %s: %w", runtime.TmuxDir, err)
	}
	for _, entry := range entries {
		count(entry.Name())
	}
	return counts, nil
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
// Cost discipline: a converged chat forks NOTHING. The last applied exact name
// is cached per session beside the cache-window anchor, and a render whose
// name still matches the cache returns before touching tmux at all.
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
	// The cache holds the server ($TMUX: socket, server pid, session) beside
	// the name: a resumed chat keeps its session id and name on a NEW server,
	// which lacks the window name and @pfm_chat_name until this render sets them.
	stamp := runtime.getenv("TMUX") + "\n" + data.SessionName
	if cached, err := os.ReadFile(cachePath); err == nil && string(cached) == stamp {
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
	// The cache keys on the EXACT name: a name that differs only past the clip
	// is still a rename for the Herdr reporter, so the combined rename-window +
	// chat-name command runs even when the window already carries the label.
	rename := gather.WindowRename{
		Socket: socket, WindowID: pane, CurrentName: current, TargetName: label,
		ChatName: data.SessionName,
	}
	client := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(runtime.TmuxDir)}
	if err := client.RenameWindow(commandContext, rename); err != nil {
		return
	}
	// Written only after the window provably carries the name, so a failed
	// rename is retried on the next render instead of being cached as done.
	_ = atomicfile.Write(cachePath, []byte(stamp), 0o600)
}

// pfmSocket resolves the fleet socket this render is running inside, or
// reports that it is not in one. It applies the same probe-socket allowance
// fleetCountsWithError does so a jailed fixture can exercise the path.
func pfmSocket(runtime Runtime) (string, bool) {
	tmux := runtime.getenv("TMUX")
	if tmux == "" {
		return "", false
	}
	socketPath := pfmtmux.SocketPath(tmux)
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
	socket := pfmtmux.SocketPath(tmux)
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

func recordRateLimits(runtime Runtime, now time.Time, account int, data input) {
	// Every window carrying a reset time is recorded under `windows`, 0% used
	// included: 0% is a real reading, and a seat that has just started is
	// exactly the one whose usage door should answer from this file instead of
	// the endpoint. windowsAt has already folded the scoped `limits` array
	// into this map, so the Fable window arrives here like the flat ones — and
	// a reader that has fallen back to this file (a signed-out or unreachable
	// account) can render no window this writer did not carry. A payload whose
	// windows have all reset writes nothing. Cache-derived readings must not
	// acquire a fresh confirmation timestamp on every draw.
	windows := make(map[string]rateWindow, len(data.RateLimits.Windows))
	live := false
	for key, window := range data.RateLimits.Windows {
		if window.ResetsAt <= 0 || window.Cached || window.Failure {
			continue
		}
		windows[key] = window
		live = live || window.ResetsAt > now.Unix()
	}
	if !live {
		return
	}
	fiveWindow := windows["five_hour"]
	sevenWindow := windows["seven_day"]
	fableWindow := windows["seven_day_fable"]
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
		"windows":             windows,
		"five_hour_used":      int64(fiveWindow.UsedPercentage),
		"seven_day_used":      int64(sevenWindow.UsedPercentage),
		"five_hour_resets_at": fiveWindow.ResetsAt,
		"seven_day_resets_at": sevenWindow.ResetsAt,
		"ts":                  now.Unix(),
	}
	// The flat keys stay beside the map for the reader an older pfm build
	// runs: a snapshot lives up to an hour, and that reader knows no map.
	if fableWindow.ResetsAt > now.Unix() {
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
	attemptPath := cachePath + ".attempt"
	if runtime.Spawn == nil || statuslineFileAge(cachePath, runtime.now()) <= ttl ||
		statuslineFileAge(attemptPath, runtime.now()) <= ttl {
		return
	}
	lockPath := strings.TrimSuffix(cachePath, ".json") + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return
	}
	held, err := takeStatuslineRefreshLock(lockPath, runtime.now(), ttl)
	if err != nil {
		obs.Logger(context.Background()).
			Warn("take statusline refresh lock", "kind", kind, "path", lockPath, obs.FieldErr, err.Error())
		return
	}
	if !held {
		return
	}
	// Bound failed/missing-Codex retries too; an absent cache must not spawn
	// another app-server on every Claude statusline update after a failed fetch.
	if err := atomicfile.Write(attemptPath, nil, 0o600); err != nil {
		_ = os.Remove(lockPath)
		obs.Logger(context.Background()).
			Warn("record statusline refresh attempt", "kind", kind, "path", attemptPath, obs.FieldErr, err.Error())
		return
	}
	if err := os.Chtimes(attemptPath, runtime.now(), runtime.now()); err != nil {
		_ = os.Remove(lockPath)
		obs.Logger(context.Background()).
			Warn("timestamp statusline refresh attempt", "kind", kind, "path", attemptPath, obs.FieldErr, err.Error())
		return
	}
	if err := runtime.Spawn(kind); err != nil {
		_ = os.Remove(lockPath)
		obs.Logger(context.Background()).Warn("start statusline refresh", "kind", kind, obs.FieldErr, err.Error())
	}
}

const codexQuotaLabel = "ChatGPT quota "

func codexQuotaSegment(runtime Runtime, now time.Time) string {
	path := filepath.Join(runtime.CacheDir, fmt.Sprintf("cc-gpt-usage-%d.json", runtime.UID))
	armRefresh(runtime, RefreshKindCodex, path, 5*time.Minute)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cWarn + codexQuotaLabel + "missing" + reset
	}
	if err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: read Codex quota %s: %v\n", path, err)
		return cBad + codexQuotaLabel + catalogError + reset
	}
	var rawUsage struct {
		Primary   json.RawMessage `json:"primary"`
		Secondary json.RawMessage `json:"secondary"`
	}
	if err := json.Unmarshal(body, &rawUsage); err != nil {
		return cBad + codexQuotaLabel + catalogError + reset
	}
	for _, raw := range []json.RawMessage{rawUsage.Primary, rawUsage.Secondary} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var fields struct {
			Percent *float64 `json:"usedPercent"`
		}
		if json.Unmarshal(raw, &fields) != nil || fields.Percent == nil {
			return cBad + codexQuotaLabel + catalogError + reset
		}
	}
	var usage codexUsageCache
	if err := json.Unmarshal(body, &usage); err != nil {
		return cBad + codexQuotaLabel + catalogError + reset
	}
	if usage.Primary == nil && usage.Secondary == nil {
		return cWarn + codexQuotaLabel + "unknown" + reset
	}
	segment := ""
	for _, window := range []*codexWindow{usage.Primary, usage.Secondary} {
		if window == nil {
			continue
		}
		if window.UsedPercent < 0 || window.UsedPercent > 100 || window.ResetsAt <= 0 ||
			window.WindowDurationMins <= 0 {
			return cBad + codexQuotaLabel + catalogError + reset
		}
		percent := gaugePercent(window.UsedPercent)
		segment = appendSegment(
			segment,
			makeBar(
				percent,
				5,
			)+" "+percentColor(
				percent,
			)+windowLabel(
				window.WindowDurationMins,
			)+"-used:"+strconv.Itoa(
				percent,
			)+"%"+reset+resetCountdown(
				now,
				window.ResetsAt,
			),
		)
	}
	if statuslineFileAge(path, now) > 5*time.Minute {
		segment += " " + cWarn + "stale" + reset
	}
	if usage.PlanType != "" {
		segment = appendSegment(segment, dim+"ChatGPT "+usage.PlanType+reset)
	}
	return segment
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
