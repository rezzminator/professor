package claudelaunch

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// WriteResolved writes the `pfm config claude` view of account (0: the
// machine): a line per registry row (Resolve), then the extra claude.settings
// section (ResolveExtraSettings), which says "none" when it is empty.
func WriteResolved(w io.Writer, machine pfmconfig.Config, account int) {
	line := func(row Resolved) {
		fmt.Fprintf(
			w,
			"%s wire=%s target=%s value=%s source=%s\n",
			row.Knob.Name, row.Knob.Wire, row.Knob.Target, row.Value, row.Won,
		)
	}
	for _, row := range Resolve(machine, account) {
		line(row)
	}
	extras := ResolveExtraSettings(machine, account)
	if len(extras) == 0 {
		fmt.Fprintln(w, "extra claude.settings: none")
		return
	}
	fmt.Fprintf(w, "extra claude.settings: %d (no knob above; every launch carries them)\n", len(extras))
	for _, row := range extras {
		line(row)
	}
}

// ResolveExtraSettings is every claude.settings value of account (0: the
// machine) a launch carries that no registry row targets, sorted: an env
// variable as env.{NAME}, any other key by its name. A null is left out, as
// the launch drops it (dropNullSettings).
func ResolveExtraSettings(machine pfmconfig.Config, account int) []Resolved {
	claude := machine.Claude.Settings
	if account != 0 {
		claude = machine.EffectiveClaude(account).Settings
	}
	named := map[string]bool{}
	for _, knob := range Knobs {
		for _, target := range strings.Split(knob.Target, "|") {
			named[target] = true
		}
	}
	var rows []Resolved
	add := func(target string, value any) {
		if named[target] || value == nil {
			return
		}
		rows = append(rows, Resolved{
			Knob:  Knob{Name: target, Wire: WireSettings, Target: target, Source: SourceConfig},
			Value: settingText(value),
			Won:   configWord,
		})
	}
	for key, value := range claude {
		env, isEnv := value.(map[string]any)
		if key != settingsEnvKey || !isEnv {
			add(key, value)
			continue
		}
		for name, variable := range env {
			add(settingsEnvKey+"."+name, variable)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Knob.Target < rows[j].Knob.Target })
	return rows
}

// layeredDefault is the value claude, the layered claude.settings, sets for
// knob when it is a shipped default (defaultKnobs), a null included, and
// whether it sets one; any other knob is never layered here.
func layeredDefault(knob Knob, claude map[string]any) (any, bool) {
	for _, shipped := range defaultKnobs() {
		if shipped.Target != knob.Target {
			continue
		}
		if name, isEnv := strings.CutPrefix(knob.Target, settingsEnvKey+"."); isEnv {
			env, _ := claude[settingsEnvKey].(map[string]any)
			value, set := env[name]
			return value, set
		}
		value, set := claude[knob.Target]
		return value, set
	}
	return nil, false
}

// settingText is a settings value as `pfm config claude` shows it: a string
// as it is, a null as unset, any other value in its JSON text.
func settingText(value any) string {
	switch typed := value.(type) {
	case nil:
		return unsetWord
	case string:
		return typed
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value) // a value json.Unmarshal produced always marshals
	}
	return string(encoded)
}
