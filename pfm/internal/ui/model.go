package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/sky"
	"hostops/pfm/internal/spawn"
	pfmstats "hostops/pfm/internal/stats"
	"hostops/pfm/internal/theme"
)

const (
	defaultWidth  = 120
	defaultHeight = 28
	keyDown       = "down"
	keyCtrlP      = "ctrl+p"
	keyCtrlN      = "ctrl+n"
	// statsRefreshInterval is the Limits/Stats cadence while watched. Stats
	// (resourcesOnly — live CPU/memory bars) stays flat; Limits decays via
	// statsCadence because its former 2s loop kept re-arming the Codex provider
	// sampler (`codex app-server`; see stats.CodexLiveLimitsTTL).
	statsRefreshInterval = 2 * time.Second
	// statsRefreshMaxInterval caps Limits' tickCadence backoff at 30s rather
	// than parking; SampleLive returns its last-good card without blocking.
	statsRefreshMaxInterval = 30 * time.Second
	cosmosRefreshInterval   = 2 * time.Second
	clockRefreshInterval    = 5 * time.Second
	// skyTickBaseInterval is the watched ambient sky/cosmos cadence: ~8fps,
	// enough for motion. Unlike tab-gated samplers, this tick once rendered
	// for the picker's entire life, even when idle or on another tab.
	//
	// tickCadence backs it off steeply; at skyTickParkThreshold the loop stops
	// rescheduling entirely. wakeSky restarts it immediately on input.
	skyTickBaseInterval  = 125 * time.Millisecond
	skyTickParkThreshold = 1 * time.Second
)

type projectGroup struct {
	name    string
	indices []int
}

type nameGroup struct {
	name  string
	count int
}

type orderedSearch struct {
	search []string
	order  []int
}

// cosmosSeat is one chat's place on its ring: the angle it is easing toward
// and the angle it is at, plus the ring it sits on as a radius factor of
// its system (1.0 is the outermost track; a crowded system's inner rings sit
// closer to their sun, see cosmosRingFactor). Orbit is the revolution phase
// accumulated tick by tick — never derived from the wall clock, so a seat
// that changes ring, and with it angular speed, glides instead of jumping.
type cosmosSeat struct {
	Angle      float64
	Target     float64
	Ring       float64
	RingTarget float64
	Orbit      float64
}

func (source orderedSearch) String(index int) string {
	return source.search[source.order[index]]
}

func (source orderedSearch) Len() int {
	return len(source.order)
}

// Model is a pure Bubble Tea state machine over a cached compose snapshot.
// Its commands only request Bubble Tea termination; all fleet I/O belongs to
// the Picker caller.
type Model struct {
	rows                 []compose.Row
	search               []string
	groups               []projectGroup
	nameGroups           map[int]nameGroup
	order                []int
	filtered             []int
	cursor               int
	width                int
	height               int
	nowNS                int64
	view                 compose.View
	killedCount          int
	suppressedCount      int
	refreshing           bool
	primary              int
	initialPrimary       int
	accountIDs           []int
	codexPrimary         int
	initialCodexPrimary  int
	codexAccountIDs      []int
	openCodePrimary      int
	openCodeAccountIDs   []int
	cache1H              bool
	tab                  Tab
	statsSubtab          StatsSubtab
	statsFocus           StatsFocus
	statsSort            StatsSort
	statsCursor          int
	statsDockerCursor    int
	limitsOffset         int
	stats                pfmstats.Snapshot
	statsSampler         StatsSampler
	samplingContext      context.Context
	limitsContext        context.Context
	limitsCancel         context.CancelFunc
	statsGeneration      uint64
	statsLoading         bool
	statsError           string
	cosmos               compose.CosmosGraph
	cosmosSampler        CosmosSampler
	cosmosEvents         []fleetdb.CommsEvent
	cosmosSeats          map[string]*cosmosSeat
	cosmosNowNS          int64
	cosmosLoading        bool
	cosmosTickGeneration uint64
	skyEnabled           bool
	cosmosSafe           bool
	// classicSky is the cosmos tab's "o" toggle: true collapses the orbital
	// hierarchy back to the first cosmos — every chat on one shared ring, no
	// stars, no revolution. The zero value keeps the solar systems on.
	classicSky bool
	// The chronoscope. cosmosPast is the replay graph while the playhead is
	// off "now" — nil means live. model.cosmos stays the LIVE graph the whole
	// time regardless — the live sample keeps refreshing it whether or not
	// the playhead has scrubbed away, so returning to now is instant and
	// rebuildCosmosPast always cuts the past against current rows; the sky
	// renders whichever viewGraph returns. cosmosViewNS is the ledger clock
	// the sky renders AT (equal to cosmosNowNS when live); cosmosTimeline is
	// the sampled window sorted oldest-first, the source every past graph is
	// cut from.
	cosmosPast      *compose.CosmosGraph
	cosmosViewNS    int64
	cosmosPlaying   bool
	cosmosTimeline  []fleetdb.CommsEvent
	cosmosPastCount int
	cosmosPastCutNS int64
	// cosmosSelected is the node key under the navigator's reticle ("" for
	// none); cosmosFocus is the home whose system fills the canvas ("" for
	// the whole galaxy); cosmosStatus is the one-line receipt the query row
	// shows when a cosmos key was refused — a swallowed keystroke reads
	// exactly like a key that did nothing on purpose.
	cosmosSelected string
	cosmosFocus    string
	cosmosStatus   string
	skyEvents      []sky.Event
	mergeNewChat   bool
	actionIndex    int
	newChatEngine  pfmengine.ID
	// activity is stamped on every real keystroke. The background refresh
	// stream reads it to decide whether anyone is still watching.
	activity *ActivityClock
	// skyCadence backs off the ambient sky/cosmos header tick the same way
	// the background refresh backs off — see tickCadence in activity.go.
	// skyParked is true once the tick has stopped rescheduling itself
	// entirely (skyCadence reached skyTickParkThreshold); wakeSky clears it.
	skyCadence tickCadence
	skyParked  bool
	// statsCadence backs off the Limits tab's sample cadence the same way
	// skyCadence backs off the ambient tick — see the comment on
	// statsRefreshGrowth. The Stats tab does not use it; it schedules at the
	// flat statsRefreshInterval instead (see statsWaitCmd's caller).
	statsCadence  tickCadence
	query         textinput.Model
	outcome       OutcomeKind
	outcomeRow    compose.Row
	outcomeEngine pfmengine.ID
	initialKilled map[string]bool
	killChanges   map[string]KillChange
	// applyKill performs a ⌃X the instant it is typed. killStatus is the
	// receipt: what landed, or why the keystroke was refused. A ⌃X NEVER goes
	// silent — a keystroke that appears to do nothing reads as a kill that
	// took, and the row's return on the next open reads as data loss.
	applyKill          func(KillChange) error
	applyDeactivate    func(compose.Row) error
	deactivatedSockets map[string]bool
	killStatus         string
}

// NewModel builds the first frame entirely from cached state.
func NewModel(snapshot Snapshot) Model {
	configureStyles(theme.Load(snapshot.Theme))
	configuredAccountEmojis = copyEmojis(snapshot.AccountEmojis)
	configuredCodexAccountEmojis = copyEmojis(snapshot.CodexAccountEmojis)
	input := textinput.New()
	input.Prompt = "find › "
	input.Placeholder = "type project or name"
	input.CharLimit = 200
	input.SetVirtualCursor(true)
	input.SetValue(snapshot.InitialQuery)
	_ = input.Focus()

	model := Model{
		rows:                append([]compose.Row(nil), snapshot.Rows...),
		width:               positiveOr(snapshot.Width, defaultWidth),
		height:              positiveOr(snapshot.Height, defaultHeight),
		nowNS:               snapshot.NowNS,
		view:                snapshot.View,
		killedCount:         snapshot.KilledCount,
		suppressedCount:     snapshot.SuppressedCount,
		refreshing:          snapshot.Refreshing,
		primary:             validAccount(snapshot.PrimaryAccount, snapshot.AccountIDs),
		initialPrimary:      validAccount(snapshot.PrimaryAccount, snapshot.AccountIDs),
		accountIDs:          normalizedAccountIDs(snapshot.AccountIDs),
		codexPrimary:        validAccount(snapshot.CodexPrimaryAccount, snapshot.CodexAccountIDs),
		initialCodexPrimary: validAccount(snapshot.CodexPrimaryAccount, snapshot.CodexAccountIDs),
		codexAccountIDs:     normalizedAccountIDs(snapshot.CodexAccountIDs),
		openCodePrimary:     validAccount(snapshot.OpenCodePrimaryAccount, snapshot.OpenCodeAccountIDs),
		openCodeAccountIDs:  normalizedAccountIDs(snapshot.OpenCodeAccountIDs),
		cache1H:             snapshot.Cache1H,
		query:               input,
		initialKilled:       make(map[string]bool),
		killChanges:         make(map[string]KillChange),
		applyKill:           snapshot.ApplyKill,
		applyDeactivate:     snapshot.ApplyDeactivate,
		deactivatedSockets:  make(map[string]bool),
		statsSampler:        snapshot.StatsSampler,
		samplingContext:     snapshot.SamplingContext,
		cosmosSampler:       snapshot.CosmosSampler,
		cosmosSeats:         make(map[string]*cosmosSeat),
		cosmosNowNS:         snapshot.NowNS,
		cosmosViewNS:        snapshot.NowNS,
		skyEnabled:          !snapshot.NoSky,
		cosmosSafe:          snapshot.CosmosSafe,
		activity:            snapshot.Activity,
		skyCadence: newTickCadence(
			snapshot.Activity,
			skyTickBaseInterval,
			skyTickParkThreshold,
		),
		statsCadence: newTickCadence(
			snapshot.Activity,
			statsRefreshInterval,
			statsRefreshMaxInterval,
		),
		mergeNewChat: snapshot.MergeNewChat,
		newChatEngine: defaultNewChatEngine(
			snapshot.AccountIDs,
			snapshot.CodexAccountIDs,
			snapshot.OpenCodeAccountIDs,
		),
	}
	if model.samplingContext == nil {
		model.samplingContext = context.Background()
	}
	for index := range model.rows {
		row := &model.rows[index]
		if row.ID != "" {
			model.initialKilled[row.ID] = row.Killed
		}
	}
	model.applyCosmosGraph(snapshot.Cosmos)
	model.rebuild(snapshot.InitialCursorID, 0)
	model.mergeCosmosSeats()
	return model
}

var (
	configuredAccountEmojis      map[int]string
	configuredCodexAccountEmojis map[int]string
)

func defaultNewChatEngine(claude, codex, openCode []int) pfmengine.ID {
	if len(normalizedAccountIDs(claude)) != 0 ||
		(len(normalizedAccountIDs(codex)) == 0 && len(normalizedAccountIDs(openCode)) == 0) {
		return pfmengine.Claude
	}
	if len(normalizedAccountIDs(codex)) != 0 {
		return pfmengine.Codex
	}
	return pfmengine.OpenCode
}

func copyEmojis(values map[int]string) map[int]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[int]string, len(values))
	for id, emoji := range values {
		result[id] = emoji
	}
	return result
}

func positiveOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func validAccount(account int, roster []int) int {
	ids := normalizedAccountIDs(roster)
	for _, id := range ids {
		if account == id {
			return account
		}
	}
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

func normalizedAccountIDs(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	result := make([]int, 0, len(values))
	seen := make(map[int]bool, len(values))
	for _, value := range values {
		if value > 0 && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

// Init starts the wall clock and optional animation. Provider and resource
// sampling starts only when its tab is selected.
func (model Model) Init() tea.Cmd {
	if !model.skyEnabled {
		return clockTickCmd()
	}
	return batchCommands(clockTickCmd(), skyTickCmd(skyTickBaseInterval))
}

// Update applies one message without touching the outside world.
func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width = positiveOr(message.Width, model.width)
		model.height = positiveOr(message.Height, model.height)
		model.query.SetWidth(maxInt(8, model.width/2))
		return model, nil
	case RefreshMsg:
		model.applyRefresh(message.Snapshot)
		return model, nil
	case clockTickMsg:
		if model.tab == TabCosmos && model.skyEnabled {
			model.advanceCosmos(message.nowNS)
		}
		model.adoptClock(message.nowNS)
		return model, clockTickCmd()
	case statsSampleMsg:
		if !isStatsSamplingTab(model.tab) || message.generation != model.statsGeneration {
			return model, nil
		}
		model.statsLoading = false
		if message.err != nil {
			model.statsError = message.err.Error()
		} else {
			model.statsError = ""
			model.applyStats(message.snapshot)
		}
		// The Stats tab's live CPU/memory bars stay on the flat cadence;
		// only Limits — a provider sampler, not a proc-table scan — backs
		// off while idle. See statsRefreshGrowth.
		interval := statsRefreshInterval
		if model.tab == TabLimits {
			interval = model.statsCadence.next()
		}
		return model, statsWaitCmd(message.generation, interval)
	case statsTickMsg:
		if !isStatsSamplingTab(model.tab) || message.generation != model.statsGeneration {
			return model, nil
		}
		command := model.startStatsSample()
		return model, command
	case cosmosSampleMsg:
		if model.tab != TabCosmos || message.generation != model.statsGeneration {
			return model, nil
		}
		model.cosmosLoading = false
		if message.err != nil {
			model.cosmos.Err = message.err.Error()
		} else {
			model.cosmosEvents = append(model.cosmosEvents[:0], message.events...)
			model.applyCosmosGraph(compose.BuildCosmos(model.rows, model.cosmosEvents, model.cosmosNowNS, true))
			if len(message.events) >= compose.CosmosEventCap {
				model.cosmos.Warnings = append(model.cosmos.Warnings, compose.CosmosTruncationWarning)
			}
			model.rebuildCosmosTimeline()
			model.rebuildCosmosPast()
			model.mergeCosmosSeats()
		}
		return model, cosmosWaitCmd(message.generation)
	case cosmosSampleTickMsg:
		if model.tab != TabCosmos || message.generation != model.statsGeneration {
			return model, nil
		}
		command := model.startCosmosSample()
		return model, command
	case cosmosTickMsg:
		if model.tab != TabCosmos || !model.skyEnabled || message.generation != model.cosmosTickGeneration {
			return model, nil
		}
		model.advanceCosmos(message.nowNS)
		return model, cosmosTickCmd(message.generation, model.cosmosTickInterval())
	case skyTickMsg:
		if !model.skyEnabled {
			return model, nil
		}
		model.nowNS = max(model.nowNS, message.nowNS)
		kept := model.skyEvents[:0]
		for _, event := range model.skyEvents {
			if !event.Expired(model.nowNS) {
				kept = append(kept, event)
			}
		}
		model.skyEvents = kept
		next := model.skyCadence.next()
		if next >= skyTickParkThreshold {
			// Parked: no further tea.Tick gets scheduled at all, so an
			// abandoned picker's ambient animation costs nothing rather
			// than merely ticking slower forever. wakeSky restarts it.
			model.skyParked = true
			return model, nil
		}
		return model, skyTickCmd(next)
	case tea.PasteMsg:
		model.activity.Stamp(time.Now())
		model.updateQuery(model.query.Value() + message.Content)
		command := model.wakeSky()
		return model, command
	case tea.KeyMsg:
		model.activity.Stamp(time.Now())
		wake := model.wakeSky()
		updated, cmd := model.updateKey(message)
		return updated, batchCommands(cmd, wake)
	default:
		return model, nil
	}
}

func (model Model) updateKey(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	if key == "esc" || key == "ctrl+c" {
		model.outcome = OutcomeCancelled
		return model, tea.Quit
	}
	switch key {
	case "left":
		if model.tab == TabChats {
			return model.navigateChatHorizontal(-1)
		}
		return model.navigateHorizontal(-1)
	case "right":
		if model.tab == TabChats {
			return model.navigateChatHorizontal(1)
		}
		return model.navigateHorizontal(1)
	case "tab":
		return model.switchTab(1)
	case "shift+tab":
		return model.switchTab(-1)
	}
	if model.tab == TabStats {
		return model.updateStatsKey(key)
	}
	if model.tab == TabLimits {
		return model.updateLimitsKey(key)
	}
	if model.tab == TabCosmos {
		return model.updateCosmosKey(key)
	}
	switch key {
	case "ctrl+x":
		model.toggleKilled()
		return model, nil
	case "ctrl+e":
		model.cache1H = !model.cache1H
		return model, nil
	case "ctrl+s":
		model.cycleSelectedAccount()
		return model, nil
	// ⌃O, not ⌃B: the picker always runs inside tmux and C-b is tmux's PREFIX,
	// so tmux swallowed the keystroke before the picker ever saw it. Any
	// replacement must stay clear of the tmux prefix.
	case "ctrl+o":
		if row, ok := model.selectedRow(); ok && row.Kind.IsLiveSeat() {
			model.outcome = OutcomeReboot
			model.outcomeRow = row
			return model, tea.Quit
		}
		return model, nil
	case "enter":
		if row, ok := model.selectedRow(); ok {
			if row.Kind == compose.ProfessorUpdate {
				model.outcome = OutcomeProfessorUpdate
				model.outcomeRow = row
				model.outcomeEngine = model.newChatEngine
				return model, tea.Quit
			}
			if model.mergeNewChat && isNewChatActionKind(row.Kind) {
				switch model.newChatEngine {
				case pfmengine.Codex:
					row.Kind = compose.NewCodex
					row.Name = "New " + pfmengine.MustLookup(pfmengine.Codex).Short + " chat"
				case pfmengine.Claude:
					row.Kind = compose.NewClaude
					row.Name = "New " + pfmengine.MustLookup(pfmengine.Claude).Short + " chat"
				case pfmengine.OpenCode:
					row.Kind = compose.NewOpenCode
					row.Name = "New " + pfmengine.MustLookup(pfmengine.OpenCode).Short + " chat"
				default:
					model.killStatus = "new chat is not available for " + pfmengine.MustLookup(
						model.newChatEngine,
					).Short
					return model, nil
				}
				row.Account = model.accountForKind(row.Kind)
				model.outcome = OutcomeSelected
				model.outcomeRow = row
				return model, tea.Quit
			}
			switch model.actionIndex {
			case 1:
				if row.Kind.IsLiveSeat() {
					model.outcome = OutcomeReboot
					model.outcomeRow = row
					return model, tea.Quit
				}
			case 2:
				model.cache1H = !model.cache1H
				return model, nil
			case 3:
				model.toggleKilled()
				return model, nil
			case 4:
				switch {
				case row.Kind == compose.LiveSplit:
					model.killStatus = "deactive refused — split live window; deactivate its chats individually"
				case row.Kind.IsLiveSeat() && row.Socket != "":
					model.deactivate(row)
				default:
					model.killStatus = "deactive refused — " + row.Name + " is not running"
				}
				return model, nil
			default:
				if isNewChatActionKind(row.Kind) {
					row.Account = model.accountForKind(row.Kind)
				}
				model.outcome = OutcomeSelected
				model.outcomeRow = row
				return model, tea.Quit
			}
		}
		return model, nil
	case "up", keyCtrlP:
		if count := len(model.filtered); count > 0 {
			model.cursor = (model.cursor - 1 + count) % count
		}
		model.actionIndex = 0
		return model, nil
	case keyDown, keyCtrlN:
		if count := len(model.filtered); count > 0 {
			model.cursor = (model.cursor + 1) % count
		}
		model.actionIndex = 0
		return model, nil
	case "pgup":
		model.cursor = maxInt(0, model.cursor-model.pageRows())
		model.actionIndex = 0
		return model, nil
	case "pgdown":
		model.cursor = min(
			maxInt(0, len(model.filtered)-1),
			model.cursor+model.pageRows(),
		)
		model.actionIndex = 0
		return model, nil
	case "home":
		model.cursor = 0
		model.actionIndex = 0
		return model, nil
	case "end":
		model.cursor = maxInt(0, len(model.filtered)-1)
		model.actionIndex = 0
		return model, nil
	}

	switch key {
	case "backspace", "ctrl+h":
		runes := []rune(model.query.Value())
		if len(runes) != 0 {
			model.updateQuery(string(runes[:len(runes)-1]))
		}
	case "ctrl+w":
		model.updateQuery(trimLastWord(model.query.Value()))
	case "ctrl+u":
		model.updateQuery("")
	default:
		if text := message.Key().Text; text != "" {
			model.updateQuery(model.query.Value() + text)
		}
	}
	return model, nil
}

func (model Model) navigateHorizontal(direction int) (tea.Model, tea.Cmd) {
	if model.tab == TabStats && model.statsFocus == StatsFocusSubtab {
		model.statsSubtab = StatsSubtab(
			(int(model.statsSubtab) + int(statsSubtabCount) + direction) % int(statsSubtabCount),
		)
		return model, nil
	}
	if model.tab == TabStats && model.statsFocus == StatsFocusContent {
		return model, nil
	}
	previous := model.tab
	model.tab = Tab((int(model.tab) + int(tabCount) + direction) % int(tabCount))
	if model.tab == TabStats {
		model.statsFocus = StatsFocusSubtab
	}
	command := model.samplingTabTransition(previous)
	return model, command
}

func (model Model) switchTab(direction int) (tea.Model, tea.Cmd) {
	previous := model.tab
	model.tab = Tab((int(model.tab) + int(tabCount) + direction) % int(tabCount))
	if model.tab == TabStats {
		model.statsFocus = StatsFocusSubtab
	}
	command := model.samplingTabTransition(previous)
	return model, command
}

// samplingTabTransition owns cancellation-by-generation for the live tabs.
// Switching between them starts the newly appropriate sampler at once;
// a late result from the tab just left carries the old generation and is
// ignored. Leaving both tabs also clears the in-flight latch so a quick return
// can start fresh instead of waiting for an obsolete command to finish.
func (model *Model) samplingTabTransition(previous Tab) tea.Cmd {
	if previous != model.tab {
		if model.limitsCancel != nil {
			model.limitsCancel()
			model.limitsContext, model.limitsCancel = nil, nil
		}
		if model.tab == TabLimits {
			model.limitsContext, model.limitsCancel = context.WithCancel(model.samplingContext)
		}
	}
	if previous != model.tab && (previous == TabCosmos || model.tab == TabCosmos) {
		model.cosmosTickGeneration++
	}
	wasSampling := isSamplingTab(previous)
	isSampling := isSamplingTab(model.tab)
	switch {
	case wasSampling && !isSampling:
		model.statsGeneration++
		model.statsLoading = false
		model.cosmosLoading = false
		return nil
	case !wasSampling && isSampling:
		return model.startFocusedSample()
	case wasSampling && isSampling && previous != model.tab:
		model.statsLoading = false
		model.cosmosLoading = false
		return model.startFocusedSample()
	default:
		return nil
	}
}

func isSamplingTab(tab Tab) bool {
	return isStatsSamplingTab(tab) || tab == TabCosmos
}

func isStatsSamplingTab(tab Tab) bool { return tab == TabStats || tab == TabLimits }

func (model *Model) startFocusedSample() tea.Cmd {
	if model.tab == TabCosmos {
		commands := []tea.Cmd{model.startCosmosSample()}
		if model.skyEnabled {
			commands = append(commands, cosmosTickCmd(model.cosmosTickGeneration, model.cosmosTickInterval()))
		}
		return batchCommands(commands...)
	}
	return model.startStatsSample()
}

func batchCommands(commands ...tea.Cmd) tea.Cmd {
	kept := commands[:0]
	for _, command := range commands {
		if command != nil {
			kept = append(kept, command)
		}
	}
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return kept[0]
	default:
		return tea.Batch(kept...)
	}
}

func (model Model) navigateChatHorizontal(direction int) (tea.Model, tea.Cmd) {
	if row, ok := model.selectedRow(); ok && model.mergeNewChat &&
		(isNewChatActionKind(row.Kind) || row.Kind == compose.ProfessorUpdate) {
		model.newChatEngine = adjacentID(model.newChatEngine, direction, model.newChatEngines())
		return model, nil
	}
	model.actionIndex = (model.actionIndex + len(carouselActions) + direction) % len(carouselActions)
	return model, nil
}

func adjacentEngine(current pfmengine.ID, direction int) pfmengine.ID {
	ids := spawn.RegisteredLaunchers()
	if len(ids) == 0 {
		// Unit packages do not execute cmd/pfm's composition root. Keep the two
		// shipped launchers available there without registering production
		// behaviour outside that root.
		ids = []pfmengine.ID{pfmengine.Claude, pfmengine.Codex}
	}
	return adjacentID(current, direction, ids)
}

func adjacentID(current pfmengine.ID, direction int, ids []pfmengine.ID) pfmengine.ID {
	if len(ids) == 0 {
		return ""
	}
	for index, id := range ids {
		if id == current {
			if direction < 0 {
				return ids[(index+len(ids)-1)%len(ids)]
			}
			return ids[(index+1)%len(ids)]
		}
	}
	return ids[0]
}

func (model Model) newChatEngines() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(pfmengine.All()))
	appendUnique := func(id pfmengine.ID) {
		for _, present := range ids {
			if present == id {
				return
			}
		}
		ids = append(ids, id)
	}
	for index := range model.rows {
		row := &model.rows[index]
		switch row.Kind {
		case compose.NewClaude:
			appendUnique(pfmengine.Claude)
		case compose.NewCodex:
			appendUnique(pfmengine.Codex)
		case compose.NewOpenCode:
			appendUnique(pfmengine.OpenCode)
		}
	}
	for _, id := range spawn.RegisteredLaunchers() {
		if id != pfmengine.Claude && id != pfmengine.Codex {
			appendUnique(id)
		}
	}
	return ids
}

// AdjacentEngine returns the previous or next registered engine in stable
// picker order. Unknown inputs start at the first registered engine.
func AdjacentEngine(current pfmengine.ID, direction int) pfmengine.ID {
	return adjacentEngine(current, direction)
}

func (model *Model) cycleSelectedAccount() {
	row, ok := model.selectedRow()
	if !ok {
		return
	}
	// The update banner is a global action, not a chat belonging to the
	// carousel engine shown inside it. Keep its account key aligned with the
	// always-visible Claude account in the header; the chosen engine still
	// resolves its own launch account when Enter is pressed.
	if row.Kind == compose.ProfessorUpdate {
		model.primary = nextAccount(model.primary, model.accountIDs)
		return
	}
	engine := compose.EngineForKind(row.Kind)
	if model.mergeNewChat && isNewChatActionKind(row.Kind) {
		engine = model.newChatEngine
	}
	if engine == pfmengine.Codex {
		model.codexPrimary = nextAccount(model.codexPrimary, model.codexAccountIDs)
		return
	}
	if engine == pfmengine.OpenCode {
		model.openCodePrimary = nextAccount(model.openCodePrimary, model.openCodeAccountIDs)
		return
	}
	model.primary = nextAccount(model.primary, model.accountIDs)
}

func nextAccount(current int, ids []int) int {
	if len(ids) == 0 {
		return 0
	}
	for index, id := range ids {
		if id == current {
			return ids[(index+1)%len(ids)]
		}
	}
	return ids[0]
}

func (model Model) accountForKind(kind compose.Kind) int {
	return model.accountForEngine(compose.EngineForKind(kind))
}

func (model Model) updateStatsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case keyDown, keyCtrlN:
		switch model.statsFocus {
		case StatsFocusTop:
			model.statsFocus = StatsFocusSubtab
		case StatsFocusSubtab:
			model.statsFocus = StatsFocusContent
		case StatsFocusContent:
			switch {
			case model.statsSubtab == StatsChats && model.statsCursor+1 < len(model.stats.Chats):
				model.statsCursor++
			case model.statsSubtab == StatsDocker && model.statsDockerCursor+1 < len(model.stats.Docker):
				model.statsDockerCursor++
			}
		}
	case "up", keyCtrlP:
		switch model.statsFocus {
		case StatsFocusSubtab:
			model.statsFocus = StatsFocusTop
		case StatsFocusContent:
			switch {
			case model.statsSubtab == StatsChats && model.statsCursor > 0:
				model.statsCursor--
			case model.statsSubtab == StatsDocker && model.statsDockerCursor > 0:
				model.statsDockerCursor--
			default:
				model.statsFocus = StatsFocusSubtab
			}
		}
	case "c":
		model.statsSort = StatsSortCPU
		model.sortStats(model.selectedStatsKey())
	case "m":
		model.statsSort = StatsSortRAM
		model.sortStats(model.selectedStatsKey())
	}
	return model, nil
}

func (model Model) updateLimitsKey(key string) (tea.Model, tea.Cmd) {
	innerWidth, innerHeight := model.limitViewportDimensions()
	maximum := maxInt(0, len(model.renderLimitCards(innerWidth))-innerHeight)
	switch key {
	case keyDown, keyCtrlN:
		model.limitsOffset = min(maximum, model.limitsOffset+1)
	case "up", keyCtrlP:
		model.limitsOffset = maxInt(0, model.limitsOffset-1)
	case "pgdown":
		model.limitsOffset = min(maximum, model.limitsOffset+innerHeight)
	case "pgup":
		model.limitsOffset = maxInt(0, model.limitsOffset-innerHeight)
	case "home":
		model.limitsOffset = 0
	case "end":
		model.limitsOffset = maximum
	}
	return model, nil
}

func (model Model) limitViewportDimensions() (int, int) {
	width := maxInt(40, model.width)
	height := maxInt(12, model.height)
	bodyHeight := maxInt(4, height-6)
	return maxInt(1, width-2), maxInt(1, bodyHeight-2)
}

type statsSampleMsg struct {
	generation uint64
	snapshot   pfmstats.Snapshot
	err        error
}

type statsTickMsg struct{ generation uint64 }

type skyTickMsg struct{ nowNS int64 }

type clockTickMsg struct{ nowNS int64 }

// This inexpensive clock keeps ages and reset timers live even when fleet
// scans and animation are parked, including under --no-sky.
func clockTickCmd() tea.Cmd {
	return tea.Tick(clockRefreshInterval, func(now time.Time) tea.Msg {
		return clockTickMsg{nowNS: now.UnixNano()}
	})
}

func (model *Model) adoptClock(nowNS int64) {
	if nowNS > model.nowNS {
		model.nowNS = nowNS
	}
	model.adoptCosmosClock(nowNS)
}

// skyTickCmd schedules the next ambient sky/cosmos header tick after
// interval, which the caller computes from model.skyCadence so an untouched
// picker's animation loop decays instead of rendering at a flat 8fps for as
// long as the pane stays open.
func skyTickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(now time.Time) tea.Msg {
		return skyTickMsg{nowNS: now.UnixNano()}
	})
}

// wakeSky un-parks the ambient sky/cosmos tick the instant a keystroke or
// paste arrives, rather than waiting for a pending tea.Tick that may not
// fire for a while (skyCadence, once parked, schedules nothing further to
// wait on). It returns nil whenever there is nothing to wake — no sky, or
// the tick was never parked — so callers can freely batch it alongside
// whatever command their own handling already produced.
func (model *Model) wakeSky() tea.Cmd {
	if !model.skyEnabled || !model.skyParked {
		return nil
	}
	model.skyParked = false
	model.skyCadence = newTickCadence(model.activity, skyTickBaseInterval, skyTickParkThreshold)
	return skyTickCmd(skyTickBaseInterval)
}

func trackedChatSockets(rows []compose.Row) map[string]bool {
	sockets := make(map[string]bool)
	for index := range rows {
		row := &rows[index]
		if row.Socket != "" && isNameGroupRow(row.Kind) {
			sockets[row.Socket] = true
		}
	}
	return sockets
}

func liveEngineCounts(rows []compose.Row) map[pfmengine.ID]int {
	counts := make(map[pfmengine.ID]int)
	for index := range rows {
		row := &rows[index]
		switch row.Kind {
		case compose.LiveCodex:
			counts[pfmengine.Codex]++
		case compose.LiveSplit:
			counts[pfmengine.Claude] += maxInt(1, row.SplitCount)
		case compose.LiveClaude, compose.Agent, compose.Booting:
			counts[pfmengine.Claude]++
		}
	}
	return counts
}

func (model *Model) startStatsSample() tea.Cmd {
	if model.statsSampler == nil || model.statsLoading {
		return nil
	}
	model.statsGeneration++
	generation := model.statsGeneration
	model.statsLoading = true
	rows := append([]compose.Row(nil), model.rows...)
	sampler := model.statsSampler
	resourcesOnly := model.tab == TabStats
	limitsContext := model.limitsContext
	if limitsContext == nil {
		limitsContext = model.samplingContext
	}
	return func() tea.Msg {
		var snapshot pfmstats.Snapshot
		var err error
		if liveSampler, ok := sampler.(interface {
			SampleLiveLimits(context.Context) pfmstats.Snapshot
		}); !resourcesOnly && ok {
			snapshot = liveSampler.SampleLiveLimits(limitsContext)
		} else if limitsSampler, ok := sampler.(interface {
			SampleLimits() pfmstats.Snapshot
		}); !resourcesOnly && ok {
			snapshot = limitsSampler.SampleLimits()
		} else if resourceSampler, ok := sampler.(interface {
			SampleResources([]compose.Row) (pfmstats.Snapshot, error)
		}); resourcesOnly && ok {
			snapshot, err = resourceSampler.SampleResources(rows)
		} else {
			snapshot, err = sampler.Sample(rows)
		}
		return statsSampleMsg{generation: generation, snapshot: snapshot, err: err}
	}
}

func statsWaitCmd(generation uint64, interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return statsTickMsg{generation: generation}
	})
}

func (model *Model) applyStats(snapshot pfmstats.Snapshot) {
	follow := model.selectedStatsKey()
	model.stats = snapshot
	model.adoptClock(snapshot.SampleTime)
	model.sortStats(follow)
	innerWidth, innerHeight := model.limitViewportDimensions()
	model.limitsOffset = min(model.limitsOffset, maxInt(0, len(model.renderLimitCards(innerWidth))-innerHeight))
}

func (model *Model) sortStats(follow string) {
	sort.SliceStable(model.stats.Chats, func(left, right int) bool {
		if model.statsSort == StatsSortRAM {
			if model.stats.Chats[left].RSSBytes != model.stats.Chats[right].RSSBytes {
				return model.stats.Chats[left].RSSBytes > model.stats.Chats[right].RSSBytes
			}
		} else if model.stats.Chats[left].CPUPercent != model.stats.Chats[right].CPUPercent {
			return model.stats.Chats[left].CPUPercent > model.stats.Chats[right].CPUPercent
		}
		return model.stats.Chats[left].Socket < model.stats.Chats[right].Socket
	})
	if len(model.stats.Chats) == 0 {
		model.statsCursor = 0
		return
	}
	if follow != "" {
		for index := range model.stats.Chats {
			if model.stats.Chats[index].Socket == follow {
				model.statsCursor = index
				return
			}
		}
	}
	model.statsCursor = min(model.statsCursor, len(model.stats.Chats)-1)
}

func (model Model) selectedStatsKey() string {
	if model.statsCursor < 0 || model.statsCursor >= len(model.stats.Chats) {
		return ""
	}
	return model.stats.Chats[model.statsCursor].Socket
}

func (model *Model) updateQuery(value string) {
	follow := model.selectedKey()
	if runes := []rune(value); len(runes) > model.query.CharLimit &&
		model.query.CharLimit > 0 {
		value = string(runes[:model.query.CharLimit])
	}
	model.query.SetValue(value)
	model.refilter(follow, 0)
}

func trimLastWord(value string) string {
	value = strings.TrimRight(value, " \t")
	index := strings.LastIndexAny(value, " \t")
	if index < 0 {
		return ""
	}
	return value[:index+1]
}

func (model *Model) applyRefresh(snapshot Snapshot) {
	follow := model.selectedKey()
	fallback := model.cursor
	rows := snapshot.Rows
	updatePresent := false
	for index := range rows {
		row := &rows[index]
		if row.Kind == compose.ProfessorUpdate {
			updatePresent = true
			break
		}
	}
	if !updatePresent {
		for index := range model.rows {
			row := &model.rows[index]
			if row.Kind == compose.ProfessorUpdate {
				rows = append([]compose.Row{*row}, rows...)
				break
			}
		}
	}
	if len(model.deactivatedSockets) != 0 {
		live := trackedChatSockets(rows)
		for socket := range model.deactivatedSockets {
			if !live[socket] {
				delete(model.deactivatedSockets, socket)
			}
		}
		filtered := make([]compose.Row, 0, len(rows))
		for index := range rows {
			row := &rows[index]
			if row.Kind.IsLiveSeat() && model.deactivatedSockets[row.Socket] {
				continue
			}
			filtered = append(filtered, *row)
		}
		rows = filtered
	}
	if model.skyEnabled {
		before := trackedChatSockets(model.rows)
		after := trackedChatSockets(rows)
		eventTime := snapshot.NowNS
		if eventTime == 0 {
			eventTime = model.nowNS
		}
		for socket := range after {
			if !before[socket] {
				model.skyEvents = append(model.skyEvents, sky.Event{
					Kind: sky.EventBirth, StartNS: eventTime,
				})
			}
		}
		for socket := range before {
			if !after[socket] {
				model.skyEvents = append(model.skyEvents, sky.Event{
					Kind: sky.EventDeath, StartNS: eventTime,
				})
			}
		}
	}
	model.rows = append(model.rows[:0], rows...)
	model.adoptClock(snapshot.NowNS)
	if snapshot.Cosmos.Err != "" {
		model.cosmos.Err = snapshot.Cosmos.Err
		model.cosmos.Warnings = append(model.cosmos.Warnings[:0], snapshot.Cosmos.Warnings...)
	} else {
		model.applyCosmosGraph(snapshot.Cosmos)
	}
	// A refresh may have changed which rows exist or are killed: the past
	// graph's ghosts are cut against the rows of NOW, so it is re-cut too.
	model.rebuildCosmosPast()
	model.mergeCosmosSeats()
	model.view = snapshot.View
	model.killedCount = snapshot.KilledCount
	model.suppressedCount = snapshot.SuppressedCount
	model.refreshing = snapshot.Refreshing
	for index := range model.rows {
		id := model.rows[index].ID
		if id != "" {
			if _, found := model.initialKilled[id]; !found {
				model.initialKilled[id] = model.rows[index].Killed
			}
		}
		if change, ok := model.killChanges[id]; ok {
			if model.rows[index].Killed != change.Killed {
				model.adjustKilledCount(change.Killed)
			}
			model.rows[index].Killed = change.Killed
		}
	}
	model.rebuild(follow, fallback)
}

func (model *Model) deactivate(row compose.Row) {
	if model.applyDeactivate == nil {
		model.killStatus = "deactive refused — this picker cannot stop chats"
		return
	}
	if err := model.applyDeactivate(row); err != nil {
		model.killStatus = fmt.Sprintf("deactive failed — %s: %v", row.Name, err)
		return
	}
	model.deactivatedSockets[row.Socket] = true
	model.killStatus = "deactivated — " + row.Name + " — ready to resume"
	key := compose.RowKey(row)
	fallback := model.cursor
	kept := model.rows[:0]
	for index := range model.rows {
		candidate := &model.rows[index]
		if compose.RowKey(*candidate) != key {
			kept = append(kept, *candidate)
		}
	}
	model.rows = kept
	model.actionIndex = 0
	model.rebuild("", fallback)
}

func (model *Model) toggleKilled() {
	if len(model.filtered) == 0 {
		return
	}
	fallback := model.cursor
	index := model.filtered[model.cursor]
	row := model.rows[index]
	// Refusals are SPOKEN, never silent: a swallowed ⌃X looks identical to a
	// kill that landed, and the row's return on the next open then reads as
	// the store losing the order.
	// A booting row's ID is its crumbless socket, not a chat identity — a kill
	// keyed by it stops meaning anything once the crumb appears (compose's
	// applyKill carries the same exclusion on the read side).
	// A "_KILL…" label is refused for a different reason: the row IS killable,
	// but its label is what kills it, so an unkill written here would delete a
	// store row that is not holding it down and the chat would stay killed
	// anyway. Renaming it is the unkill.
	switch {
	case row.Kind == compose.ProfessorUpdate:
		model.killStatus = "⌃X refused — the Professor update banner is an action, not a chat"
		return
	case row.Kind == compose.Booting:
		model.killStatus = "⌃X refused — " + row.Name +
			" is still booting (no identity yet); retry once it settles"
		return
	case row.Kind == compose.LiveSplit:
		model.killStatus = "⌃X refused — split live window; kill its chats individually"
		return
	case row.NameKilled:
		model.killStatus = "⌃X refused — killed by its _KILL label; rename it to unkill"
		return
	case row.ID == "":
		model.killStatus = "⌃X refused — " + row.Name + " is not a chat yet"
		return
	}
	change := KillChange{
		ID:     row.ID,
		Engine: rowEngine(row.Kind),
		Killed: !row.Killed,
		Socket: row.Socket,
		PaneID: row.PaneID,
		Live:   row.Kind.IsLiveSeat(),
		Name:   row.Name,
	}
	// ⌃X lands NOW — the store write, and the kill when the row is live. It
	// used to be held until the picker quit, which meant only the exits that
	// launched something ever applied it: closing the list the natural way
	// discarded every mark that had just been typed.
	// A nil applier means nothing can persist this keystroke: refuse rather
	// than paint a cosmetic kill the next open would take back.
	if model.applyKill == nil {
		model.killStatus = "⌃X refused — this picker cannot write kills"
		return
	}
	if err := model.applyKill(change); err != nil {
		model.killStatus = fmt.Sprintf("⌃X failed — %s: %v", change.Name, err)
		return
	}
	switch {
	case change.Killed:
		model.killStatus = "hidden — " + change.Name
	default:
		model.killStatus = "unhidden — " + change.Name
	}
	follow := compose.RowKey(row)
	model.rows[index].Killed = change.Killed
	model.adjustKilledCount(change.Killed)
	model.killChanges[change.ID] = change
	model.rebuild(follow, fallback)
}

func (model *Model) adjustKilledCount(killed bool) {
	if killed {
		model.killedCount++
	} else if model.killedCount > 0 {
		model.killedCount--
	}
}

func (model *Model) rebuild(follow string, fallback int) {
	model.search = make([]string, len(model.rows))
	byProject := make(map[string]int)
	model.groups = model.groups[:0]
	for index := range model.rows {
		row := &model.rows[index]
		project := cleanField(row.Project)
		if project == "" {
			project = "?"
		}
		model.search[index] = strings.ToLower(
			project + " " + cleanField(row.Name),
		)
		groupIndex, found := byProject[project]
		if !found {
			groupIndex = len(model.groups)
			byProject[project] = groupIndex
			model.groups = append(model.groups, projectGroup{name: project})
		}
		model.groups[groupIndex].indices = append(
			model.groups[groupIndex].indices,
			index,
		)
	}
	model.rebuildOrder()
	model.refilter(follow, fallback)
}

func (model *Model) rebuildOrder() {
	model.order = model.order[:0]
	model.nameGroups = make(map[int]nameGroup)
	// members and emitted are scanned/shared across every project group, not
	// rebuilt per group, so a name prefix folds into one panel across the
	// whole fleet — a colon-prefixed row in project A groups with its
	// namesakes in project B, not just its own project's rows.
	members := make(map[string][]int)
	newChatEmitted := false
	for _, group := range model.groups {
		for _, index := range group.indices {
			row := model.rows[index]
			if !model.visibleInView(row) || !isNameGroupRow(row.Kind) {
				continue
			}
			if prefix, ok := nameGroupPrefix(row.Name); ok {
				members[prefix] = append(members[prefix], index)
			}
		}
	}
	emitted := make(map[string]bool)
	pinned := make(map[int]bool)
	if model.mergeNewChat {
		// The update notice is an extra global action above the ordinary new-chat
		// row. Neither row belongs to project activity order: pinning both keeps
		// an active Professor chat from slipping between them.
		for index := range model.rows {
			row := &model.rows[index]
			if row.Kind == compose.ProfessorUpdate && model.visibleInView(*row) {
				model.order = append(model.order, index)
				pinned[index] = true
			}
		}
		for index := range model.rows {
			row := &model.rows[index]
			if isNewChatActionKind(row.Kind) && model.visibleInView(*row) {
				model.order = append(model.order, index)
				pinned[index] = true
				newChatEmitted = true
				break
			}
		}
	}
	for _, group := range model.groups {
		for _, index := range group.indices {
			if pinned[index] {
				continue
			}
			if !model.visibleInView(model.rows[index]) {
				continue
			}
			if model.mergeNewChat && isNewChatActionKind(model.rows[index].Kind) {
				if newChatEmitted {
					continue
				}
				newChatEmitted = true
			}
			prefix, grouped := nameGroupPrefix(model.rows[index].Name)
			// ONE member is a group. A chat named GROUP:NAME has already
			// declared where it belongs, and the panel is what makes that
			// readable — so it gets the header and the indent immediately
			// rather than sitting flat until a second member shows up and
			// re-lays out the list underneath the reader.
			//
			// The membership test is the same one that built `members`: a row
			// that is not itself a member must never take the group path,
			// because that path emits the member list and skips the row it was
			// called for.
			grouped = grouped &&
				isNameGroupRow(model.rows[index].Kind) &&
				len(members[prefix]) >= 1
			if !grouped {
				model.order = append(model.order, index)
				continue
			}
			if emitted[prefix] {
				continue
			}
			emitted[prefix] = true
			for _, member := range members[prefix] {
				model.nameGroups[member] = nameGroup{name: prefix, count: len(members[prefix])}
				model.order = append(model.order, member)
			}
		}
	}
}

func isNewChatActionKind(kind compose.Kind) bool {
	return kind == compose.NewClaude || kind == compose.NewCodex || kind == compose.NewOpenCode
}

// nameGroupPrefix reads a GROUP:NAME declaration off a chat name.
//
// The shape is exact on purpose: a non-empty prefix with NO whitespace in it,
// a colon, and a non-empty remainder that does not start with whitespace.
// "P:BUILDER" declares a group; "fix: the bug" is a sentence with a colon in
// it and declares nothing, and neither does "wave 3: rework".
//
// The strictness became load-bearing when a single member started opening a
// panel. Under the old two-member threshold a prose colon was mostly harmless
// — it took two of them to invent a group — so the rule could afford to be
// loose. It cannot now: every stray colon would become a header.
func nameGroupPrefix(name string) (string, bool) {
	prefix, rest, found := strings.Cut(cleanField(name), ":")
	if !found || prefix == "" || rest == "" {
		return "", false
	}
	if strings.ContainsFunc(prefix, unicode.IsSpace) {
		return "", false
	}
	if unicode.IsSpace(rune(rest[0])) {
		return "", false
	}
	return prefix, true
}

// isNameGroupRow admits every kind a GROUP:NAME can fold into a panel: live,
// Agent, Booting, and every resumable kind — a resumable SOLO:BUILD groups
// with its live namesakes exactly like a live row would.
func isNameGroupRow(kind compose.Kind) bool {
	return kind.IsAddressable() ||
		kind == compose.ResumeClaude || kind == compose.ResumeCodex || kind == compose.ResumeOpenCode
}

func (model *Model) refilter(follow string, fallback int) {
	query := strings.TrimSpace(model.query.Value())
	model.filtered = model.filtered[:0]
	if query == "" {
		model.filtered = append(model.filtered, model.order...)
	} else {
		query = strings.ToLower(query)
		matched := make([]bool, len(model.rows))
		fuzzyOrder := make([]int, 0, len(model.order))
		for _, rowIndex := range model.order {
			candidate := model.search[rowIndex]
			if strings.Contains(candidate, query) {
				matched[rowIndex] = true
			} else if runeSubsequence(query, candidate) {
				fuzzyOrder = append(fuzzyOrder, rowIndex)
			}
		}
		if len(fuzzyOrder) != 0 {
			source := orderedSearch{
				search: model.search,
				order:  fuzzyOrder,
			}
			for _, match := range fuzzy.FindFromNoSort(query, source) {
				matched[fuzzyOrder[match.Index]] = true
			}
		}
		for _, rowIndex := range model.order {
			if matched[rowIndex] {
				model.filtered = append(model.filtered, rowIndex)
			}
		}
	}
	if len(model.filtered) == 0 {
		model.cursor = 0
		return
	}
	if follow != "" {
		for cursor, index := range model.filtered {
			if compose.RowKey(model.rows[index]) == follow {
				model.cursor = cursor
				return
			}
		}
	}
	model.cursor = min(maxInt(fallback, 0), len(model.filtered)-1)
}

func runeSubsequence(pattern, candidate string) bool {
	if pattern == "" {
		return true
	}
	patternRunes := []rune(pattern)
	position := 0
	for _, candidateRune := range candidate {
		if candidateRune == patternRunes[position] {
			position++
			if position == len(patternRunes) {
				return true
			}
		}
	}
	return false
}

func (model Model) visibleInView(row compose.Row) bool {
	switch model.view {
	case compose.AllView:
		return true
	case compose.KilledView:
		return row.Killed
	default:
		return !row.Killed
	}
}

func (model Model) pageRows() int {
	return maxInt(1, model.height-8)
}

func (model Model) selectedRow() (compose.Row, bool) {
	if model.cursor < 0 || model.cursor >= len(model.filtered) {
		return compose.Row{}, false
	}
	return model.rows[model.filtered[model.cursor]], true
}

func (model Model) selectedKey() string {
	row, ok := model.selectedRow()
	if !ok {
		return ""
	}
	return compose.RowKey(row)
}

func rowEngine(kind compose.Kind) pfmengine.ID {
	return compose.EngineForKind(kind)
}

// Result returns the effects accumulated by the pure model. Cancelled reverts
// PrimaryAccount to what the picker opened with, since a ⌃S account switch is
// only a pending intent until the picker exits deliberately.
//
// KillChanges survives a cancel: it is no longer a request to apply anything,
// it is the RECEIPT of what ⌃X already did while the picker was open. Esc
// closes the list; it does not resurrect a chat that was killed and killed.
func (model Model) Result() Outcome {
	if model.outcome == OutcomeCancelled {
		return Outcome{
			Kind:                 OutcomeCancelled,
			PrimaryAccount:       model.initialPrimary,
			ClaudePrimaryAccount: model.initialPrimary,
			Cache1H:              model.cache1H,
			Query:                model.query.Value(),
			KillChanges:          model.appliedChanges(),
		}
	}
	primary := model.accountForKind(model.outcomeRow.Kind)
	if model.outcome == OutcomeProfessorUpdate {
		primary = model.accountForEngine(model.outcomeEngine)
	}
	return Outcome{
		Kind:                 model.outcome,
		Row:                  model.outcomeRow,
		Engine:               model.outcomeEngine,
		PrimaryAccount:       primary,
		ClaudePrimaryAccount: model.primary,
		Cache1H:              model.cache1H,
		Query:                model.query.Value(),
		KillChanges:          model.appliedChanges(),
	}
}

func (model Model) accountForEngine(engine pfmengine.ID) int {
	switch engine {
	case pfmengine.Codex:
		return model.codexPrimary
	case pfmengine.OpenCode:
		return model.openCodePrimary
	default:
		return model.primary
	}
}

func (model Model) appliedChanges() []KillChange {
	changes := make([]KillChange, 0, len(model.killChanges))
	for _, change := range model.killChanges {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(left, right int) bool {
		return changes[left].ID < changes[right].ID
	})
	return changes
}

// VisibleRows returns a copy for model tests and noninteractive adapters.
func (model Model) VisibleRows() []compose.Row {
	rows := make([]compose.Row, 0, len(model.filtered))
	for _, index := range model.filtered {
		rows = append(rows, model.rows[index])
	}
	return rows
}

func (model Model) Cursor() int                 { return model.cursor }
func (model Model) Query() string               { return model.query.Value() }
func (model Model) PrimaryAccount() int         { return model.primary }
func (model Model) Cache1H() bool               { return model.cache1H }
func (model Model) Tab() Tab                    { return model.tab }
func (model Model) SelectedKey() string         { return model.selectedKey() }
func (model Model) GroupCount() int             { return len(model.groups) }
func (model Model) OrderedRowCount() int        { return len(model.order) }
func (model Model) FilteredRowCount() int       { return len(model.filtered) }
func (model Model) ActionIndex() int            { return model.actionIndex }
func (model Model) NewChatEngine() pfmengine.ID { return model.newChatEngine }
func (model Model) ValidUTF8Query() bool        { return utf8.ValidString(model.Query()) }
func (model Model) HasVisibleSelection() bool { // compact invariant helper
	return len(model.filtered) == 0 ||
		(model.cursor >= 0 && model.cursor < len(model.filtered))
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
