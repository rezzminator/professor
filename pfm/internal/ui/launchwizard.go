package ui

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// The new-chat wizard. ⏎ on a ✦ row walks the choices of a new chat inside that
// row, one step at a time:
//
//	engine   when the row can launch more than one
//	account  when the chosen engine has more than one configured
//	cache    when the chosen engine has a cache choice (Claude: 1h or 5m)
//
// ←→ moves within a step, ⏎ confirms it, esc goes back one step (and out of
// the wizard from the first). Every step opens on the choice the last launch
// made (fleetdb.LaunchMemory), falling back to the configured value. A step
// with nothing to choose is skipped; when every step is, ⏎ starts the chat.
type wizardStep uint8

const (
	wizardEngine wizardStep = iota + 1
	wizardAccount
	wizardCache
)

var wizardStepNames = map[wizardStep]string{
	wizardEngine: "engine", wizardAccount: "account", wizardCache: "cache",
}

// wizardState is the Model's wizard: the walk in progress and the launch
// memory its steps open on.
type wizardState struct {
	wizard        launchWizard
	remembered    fleetdb.LaunchMemory
	memoryError   string // a failed read of remembered, named on the ✦ row
	defaultEngine pfmengine.ID
}

// launchWizard is one walk. engine, account and cache are the choices so far;
// accountEngine and cacheAccount name what the account and cache were last
// defaulted for, so going back and forth keeps a choice until its cause moves.
type launchWizard struct {
	on            bool
	rowKey        string
	step          wizardStep
	trail         []wizardStep
	engine        pfmengine.ID
	account       int
	cache         bool
	pos           int
	accountEngine pfmengine.ID
	cacheAccount  int
}

// wizardChoice is one chip of a step.
type wizardChoice struct {
	label   string
	engine  pfmengine.ID
	account int
	cache   bool
}

func wizardRowKey(row compose.Row) string {
	return row.Workbench + "\x00" + row.CWD + "\x00" + row.Project
}

// wizardOn reports whether the wizard is open on this ✦ row.
func (model Model) wizardOn(row compose.Row) bool {
	return model.wizard.on && model.wizard.rowKey == wizardRowKey(row)
}

// hasCacheChoice reports whether engine offers a prompt-cache choice.
func hasCacheChoice(engine pfmengine.ID) bool { return engine == pfmengine.Claude }

func (model Model) accountsOf(engine pfmengine.ID) []int {
	switch engine {
	case pfmengine.Codex:
		return model.codexAccountIDs
	case pfmengine.OpenCode:
		return model.openCodeAccountIDs
	default:
		return model.accountIDs
	}
}

// idleEngine is the engine an idle ✦ row starts: the last launch's when this
// row can start it, else the configured default, else its first.
func (model Model) idleEngine(row compose.Row) pfmengine.ID {
	ids := model.newChatEnginesFor(row)
	if len(ids) == 0 {
		return model.defaultEngine
	}
	if remembered := pfmengine.ID(model.remembered.Engine); slices.Contains(ids, remembered) {
		return remembered
	}
	if slices.Contains(ids, model.defaultEngine) {
		return model.defaultEngine
	}
	return ids[0]
}

// defaultAccount is the account a step opens on for engine: the last launch's
// (a Claude launch persists as the primary account), else the roster's first.
func (model Model) defaultAccount(engine pfmengine.ID) int {
	return validAccount(model.accountForEngine(engine), model.accountsOf(engine))
}

// defaultCache is the prompt cache a Claude account opens on: its last
// launch's, else its configured value.
func (model Model) defaultCache(account int) bool {
	if choice, ok := model.cache1HByAccount[account]; ok {
		return choice
	}
	return model.cache1H
}

// applyLaunchMemory folds the remembered launch into the Model's defaults:
// the engine, the Codex and OpenCode accounts and each Claude account's
// cache. The Claude account is the primary account, which a launch already
// persists through fleet.SetPrimaryAccount.
func (model *Model) applyLaunchMemory(memory fleetdb.LaunchMemory) {
	model.remembered = memory
	model.cache1HByAccount = mergedCache(model.cache1HByAccount, memory.Cache1H)
	if choice, ok := model.cache1HByAccount[model.primary]; ok {
		model.cache1H = choice
	}
	if account, ok := memory.Accounts[string(pfmengine.Codex)]; ok {
		model.codexPrimary = validAccount(account, model.codexAccountIDs)
	}
	if account, ok := memory.Accounts[string(pfmengine.OpenCode)]; ok {
		model.openCodePrimary = validAccount(account, model.openCodeAccountIDs)
	}
}

func mergedCache(configured, remembered map[int]bool) map[int]bool {
	merged := make(map[int]bool, len(configured)+len(remembered))
	for account, choice := range configured {
		merged[account] = choice
	}
	for account, choice := range remembered {
		merged[account] = choice
	}
	return merged
}

// wizardApplies reports whether step asks anything for engine.
func (model Model) wizardApplies(row compose.Row, step wizardStep, engine pfmengine.ID) bool {
	switch step {
	case wizardEngine:
		return len(model.newChatEnginesFor(row)) > 1
	case wizardAccount:
		return len(model.accountsOf(engine)) > 1
	case wizardCache:
		return hasCacheChoice(engine)
	}
	return false
}

// wizardNext is the first applicable step after the given one.
func (model Model) wizardNext(row compose.Row, after wizardStep, engine pfmengine.ID) (wizardStep, bool) {
	for step := after + 1; step <= wizardCache; step++ {
		if model.wizardApplies(row, step, engine) {
			return step, true
		}
	}
	return 0, false
}

// wizardChoices lists the chips of a step.
func (model Model) wizardChoices(row compose.Row, step wizardStep, engine pfmengine.ID) []wizardChoice {
	switch step {
	case wizardEngine:
		ids := model.newChatEnginesFor(row)
		choices := make([]wizardChoice, 0, len(ids))
		for _, id := range ids {
			choices = append(choices, wizardChoice{label: pfmengine.MustLookup(id).Short, engine: id})
		}
		return choices
	case wizardAccount:
		ids := model.accountsOf(engine)
		choices := make([]wizardChoice, 0, len(ids))
		for _, id := range ids {
			choices = append(choices, wizardChoice{label: accountChip(engine, id), account: id})
		}
		return choices
	case wizardCache:
		return []wizardChoice{{label: "1h", cache: true}, {label: "5m", cache: false}}
	}
	return nil
}

// orderedChoices is a step's chips with its default first: the last launch's
// engine, that engine's last account, that account's last cache — so the
// first chip, already highlighted, is what ⏎ repeats.
func (model Model) orderedChoices(row compose.Row, wizard launchWizard) []wizardChoice {
	choices := model.wizardChoices(row, wizard.step, wizard.engine)
	lead := func(choice wizardChoice) bool {
		switch wizard.step {
		case wizardEngine:
			return choice.engine == model.idleEngine(row)
		case wizardAccount:
			return choice.account == model.defaultAccount(wizard.engine)
		case wizardCache:
			return choice.cache == model.defaultCache(wizard.account)
		}
		return false
	}
	for index, choice := range choices {
		if index > 0 && lead(choice) {
			copy(choices[1:index+1], choices[:index])
			choices[0] = choice
			break
		}
	}
	return choices
}

// accountChip is an account's emoji and id.
func accountChip(engine pfmengine.ID, account int) string {
	return accountEmoji(engine, account) + " " + strconv.Itoa(account)
}

func accountEmoji(engine pfmengine.ID, account int) string {
	switch engine {
	case pfmengine.Codex:
		return codexAccountMedal(account)
	case pfmengine.OpenCode:
		return pfmconfig.DefaultEmoji(account)
	default:
		return accountMedal(account)
	}
}

// seek points the highlighted chip at the step's current choice.
func (model Model) seek(row compose.Row, wizard launchWizard) launchWizard {
	choices := model.orderedChoices(row, wizard)
	wizard.pos = 0
	for index, choice := range choices {
		switch wizard.step {
		case wizardEngine:
			if choice.engine == wizard.engine {
				wizard.pos = index
			}
		case wizardAccount:
			if choice.account == wizard.account {
				wizard.pos = index
			}
		case wizardCache:
			if choice.cache == wizard.cache {
				wizard.pos = index
			}
		}
	}
	return wizard
}

// beginWizard opens the wizard on a ✦ row at ⏎.
func (model Model) beginWizard(row compose.Row) (tea.Model, tea.Cmd) {
	if len(model.newChatEnginesFor(row)) == 0 {
		model.killStatus = "new chat is not available here — no engine is set up for it"
		return model, nil
	}
	model.wizard = launchWizard{on: true, rowKey: wizardRowKey(row), engine: model.idleEngine(row)}
	return model.wizardEnter(row, 0)
}

// wizardEnter moves to the first applicable step after the given one, or
// starts the chat when none is left.
func (model Model) wizardEnter(row compose.Row, after wizardStep) (tea.Model, tea.Cmd) {
	wizard := model.wizard
	step, ok := model.wizardNext(row, after, wizard.engine)
	if !ok {
		return model.finishWizard(row)
	}
	wizard.step = step
	if step >= wizardAccount && wizard.accountEngine != wizard.engine {
		wizard.account, wizard.accountEngine = model.defaultAccount(wizard.engine), wizard.engine
	}
	if step == wizardCache && wizard.cacheAccount != wizard.account {
		wizard.cache, wizard.cacheAccount = model.defaultCache(wizard.account), wizard.account
	}
	model.wizard = model.seek(row, wizard)
	return model, nil
}

// finishWizard turns the walk into the launch outcome: the chosen engine's
// New* row, its account and, for Claude, its cache.
func (model Model) finishWizard(row compose.Row) (tea.Model, tea.Cmd) {
	wizard := model.wizard
	model.wizard = launchWizard{}
	if wizard.account > 0 {
		switch wizard.engine {
		case pfmengine.Codex:
			model.codexPrimary = wizard.account
		case pfmengine.OpenCode:
			model.openCodePrimary = wizard.account
		default:
			model.primary = wizard.account
		}
	}
	if hasCacheChoice(wizard.engine) {
		model.cache1H = wizard.cache
	}
	selected, ok := model.newChatRowFor(row, wizard.engine)
	if !ok {
		model.killStatus = "new chat is not available for " + string(wizard.engine)
		return model, nil
	}
	model.outcome = OutcomeSelected
	model.outcomeRow = selected
	return model, tea.Quit
}

// wizardKey handles the keys of an open wizard. It reports false for any
// other key, and for a wizard that no longer sits under the cursor: the
// caller then closes the wizard and handles the key as usual.
func (model Model) wizardKey(key string) (tea.Model, tea.Cmd, bool) {
	row, ok := model.selectedRow()
	if !ok || model.tab != TabChats || !isNewChatActionKind(row.Kind) || !model.wizardOn(row) {
		return model, nil, false
	}
	wizard := model.wizard
	switch key {
	case keyLeft, keyRight:
		choices := model.orderedChoices(row, wizard)
		if len(choices) == 0 {
			return model, nil, true
		}
		wizard.pos = (wizard.pos + len(choices) + map[string]int{keyLeft: -1, keyRight: 1}[key]) % len(choices)
		if wizard.step == wizardEngine {
			wizard.engine = choices[wizard.pos].engine
		}
		model.wizard = wizard
		return model, nil, true
	case keyEnter:
		choices := model.orderedChoices(row, wizard)
		if wizard.pos >= len(choices) {
			return model, nil, true
		}
		choice := choices[wizard.pos]
		switch wizard.step {
		case wizardEngine:
			wizard.engine = choice.engine
		case wizardAccount:
			wizard.account = choice.account
		case wizardCache:
			wizard.cache = choice.cache
		}
		wizard.trail = append(slices.Clone(wizard.trail), wizard.step)
		model.wizard = wizard
		next, cmd := model.wizardEnter(row, wizard.step)
		return next, cmd, true
	case keyEsc:
		if len(wizard.trail) == 0 {
			model.wizard = launchWizard{}
			return model, nil, true
		}
		wizard.step = wizard.trail[len(wizard.trail)-1]
		wizard.trail = slices.Clone(wizard.trail[:len(wizard.trail)-1])
		model.wizard = model.seek(row, wizard)
		return model, nil, true
	}
	return model, nil, false
}

// wizardPosition numbers the open step among those the chosen engine asks.
func (model Model) wizardPosition(row compose.Row) (index, total int) {
	for step := wizardEngine; step <= wizardCache; step++ {
		if !model.wizardApplies(row, step, model.wizard.engine) {
			continue
		}
		total++
		if step <= model.wizard.step {
			index = total
		}
	}
	return index, total
}

// wizardStatus says which step the open wizard is on, for the footer.
func (model Model) wizardStatus(row compose.Row) string {
	index, total := model.wizardPosition(row)
	return "new chat · step " + strconv.Itoa(index) + " of " + strconv.Itoa(total) +
		" · " + wizardStepNames[model.wizard.step]
}

// wizardStartsAtOnce reports whether ⏎ on the idle ✦ row starts the chat
// without asking anything.
func (model Model) wizardStartsAtOnce(row compose.Row) bool {
	_, asks := model.wizardNext(row, 0, model.idleEngine(row))
	return !asks
}

// wizardPurpose is the dossier's account of a ✦ row.
func (model Model) wizardPurpose(row compose.Row) string {
	short := pfmengine.MustLookup(model.effectiveNewChatEngine(row)).Short
	where := ""
	if row.CWD != "" {
		where = " in " + row.CWD
	}
	if row.Workbench != "" {
		where += " on its workbench prompt"
	}
	switch {
	case model.wizardOn(row):
		index, total := model.wizardPosition(row)
		return "Step " + strconv.Itoa(index) + " of " + strconv.Itoa(total) + " — choose the " +
			wizardStepNames[model.wizard.step] + " of a new chat" + where +
			". ←→ moves, ⏎ confirms, esc goes back."
	case model.wizardStartsAtOnce(row):
		return "Enter starts a new " + short + " chat" + where + "." + model.memoryNote()
	}
	return "Enter chooses the engine, account and cache of a new chat" + where +
		", each step opening on your last choice." + model.memoryNote()
}

// memoryNote names a launch memory that could not be read.
func (model Model) memoryNote() string {
	if model.memoryError == "" {
		return ""
	}
	return " Your last launch could not be read (" + model.memoryError + "): the steps open on the configured values."
}

// launchSpans paints a ✦ row's name cell: the remembered choice while idle,
// the open step's chips while the wizard walks.
func (model Model) launchSpans(row compose.Row, width int, bg string) []span {
	palette := configuredPalette
	engine := model.effectiveNewChatEngine(row)
	engineHex := palette.EngineRow[engine]
	if engineHex == "" {
		engineHex = palette.Accent
	}
	dim := tone{fg: palette.Dim, bg: bg}
	var spans []span
	if model.wizardOn(row) {
		spans = model.wizardSpans(row, width, bg, engineHex)
	} else {
		where := row.Project
		if where == "" {
			where = "here"
		}
		spans = []span{
			{text: "New chat", paint: tone{fg: engineHex, bg: bg, bold: true}},
			{text: " → ", paint: dim},
			{text: where, paint: tone{fg: palette.Header, bg: bg, bold: true}},
		}
		// The remembered choice rides along where it fits: whole, else the
		// engine; a memory that could not be read says so first.
		labels := model.rememberedLabels(engine)
		if model.memoryError != "" && len(labels) > 0 {
			labels = append([]string{labels[len(labels)-1] + " · last launch unreadable"}, labels...)
		}
		for _, remembered := range labels {
			if spansWidth(spans)+ansi.StringWidth("  "+remembered) <= width {
				spans = append(spans, span{text: "  " + remembered, paint: dim})
				break
			}
		}
	}
	used := spansWidth(spans)
	if used > width {
		plain := ""
		for _, part := range spans {
			plain += part.text
		}
		return []span{{text: padRightCells(plain, width), paint: tone{fg: engineHex, bg: bg, bold: true}}}
	}
	if used < width {
		spans = append(spans, span{text: strings.Repeat(" ", width-used), paint: tone{bg: bg}})
	}
	return spans
}

// rememberedLabels is the choice an idle ✦ row would start with, whole
// ("Claude · 🥈 · 1h") and then shortened to the engine.
func (model Model) rememberedLabels(engine pfmengine.ID) []string {
	if engine == "" {
		return nil
	}
	short := pfmengine.MustLookup(engine).Short
	parts := []string{short}
	account := model.defaultAccount(engine)
	if account > 0 {
		parts = append(parts, accountEmoji(engine, account))
	}
	if hasCacheChoice(engine) {
		parts = append(parts, cacheLabel(model.defaultCache(account)))
	}
	return []string{strings.Join(parts, " · "), short}
}

func cacheLabel(on bool) string {
	if on {
		return "1h"
	}
	return "5m"
}

// wizardSpans paints the open step: what is already chosen, dim, then the
// step's chips with the highlighted one boxed.
func (model Model) wizardSpans(row compose.Row, width int, bg, engineHex string) []span {
	palette := configuredPalette
	wizard := model.wizard
	dim := tone{fg: palette.Dim, bg: bg}
	lead := "New chat"
	if wizard.step > wizardEngine && len(wizard.trail) > 0 {
		parts := []string{pfmengine.MustLookup(wizard.engine).Short}
		if wizard.step == wizardCache && len(model.accountsOf(wizard.engine)) > 1 {
			parts = append(parts, accountChip(wizard.engine, wizard.account))
		}
		lead = strings.Join(parts, " · ")
	}
	choices := model.orderedChoices(row, wizard)
	if len(choices) == 0 {
		return []span{{text: lead + " ›", paint: dim}}
	}
	wizard.pos = min(wizard.pos, len(choices)-1) // a refresh may have shrunk the step
	build := func(withLead, brackets bool) []span {
		var spans []span
		if withLead {
			spans = append(spans, span{text: lead + " ›", paint: dim})
		}
		for index, choice := range choices {
			label, paint := choice.label, tone{fg: palette.Muted, bg: bg}
			switch {
			case index == wizard.pos:
				label, paint = "◖ "+choice.label+" ◗", tone{fg: engineHex, bg: bg, bold: true}
			case brackets:
				label = "[ " + choice.label + " ]"
			}
			if len(spans) > 0 {
				spans = append(spans, span{text: " ", paint: tone{bg: bg}})
			}
			spans = append(spans, span{text: label, paint: paint})
		}
		return spans
	}
	// As the row narrows: drop the brackets, then the lead, then every chip
	// but the highlighted one (the footer and dossier still name the rest).
	for _, tier := range [][2]bool{{true, true}, {true, false}, {false, true}, {false, false}} {
		if spans := build(tier[0], tier[1]); spansWidth(spans) <= width {
			return spans
		}
	}
	return []span{{
		text:  "◖ " + choices[wizard.pos].label + " ◗ " + strconv.Itoa(wizard.pos+1) + "/" + strconv.Itoa(len(choices)),
		paint: tone{fg: engineHex, bg: bg, bold: true},
	}}
}
