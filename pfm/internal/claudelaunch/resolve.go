package claudelaunch

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type Resolved struct {
	Knob  Knob
	Value string
	Won   string
}

// Resolve reports the value and winning source of every registry row.
func Resolve(machine pfmconfig.Config, account int) []Resolved {
	prefs := machine.Claude
	accountIndex := -1
	overrides := accountOverrideKeys(machine.Path, account)
	if account != 0 {
		prefs = machine.EffectiveClaude(account)
		for index, candidate := range machine.Accounts {
			if candidate.ID == account {
				accountIndex = index
				break
			}
		}
	}
	rows := make([]Resolved, 0, len(Knobs))
	for _, knob := range Knobs {
		row := Resolved{Knob: knob, Won: defaultWord, Value: unsetWord}
		switch knob.Source {
		case SourceConstant:
			row.Won = "constant"
			if knob.Wire == WireUnset {
				row.Value = unsetWord
			} else {
				row.Value = fmt.Sprint(knob.Default)
			}
		case SourceAccount:
			if accountIndex >= 0 {
				selected := machine.Accounts[accountIndex]
				if knob.Name == knobNoFlicker {
					row.Value, row.Won = noFlickerValue(selected.ConfigDir), accountWord
				} else {
					row.Value = selected.ConfigDir
					row.Won = accountWord
				}
			}
		case SourceConfig, SourceLaunchThenConfig:
			row.Value = configValue(knob.Name, prefs, machine)
			key := "claude." + knob.Name
			if accountIndex >= 0 &&
				(overrides[knob.Name] || machine.Source(fmt.Sprintf("accounts[%d].claude.%s", accountIndex, knob.Name)) == pfmconfig.SourceFile) {
				row.Won = accountWord
			} else if machine.Source(
				key,
			) == pfmconfig.SourceFile {
				row.Won = "config"
			}
		case SourceMachineConfig:
			row.Value = configValue(knob.Name, prefs, machine)
			if machine.MCPServerSource(pfmconfig.MCPServerChat) != pfmconfig.SourceDefault ||
				machine.MCPServerSource(pfmconfig.MCPServerHarvester) != pfmconfig.SourceDefault {
				row.Won = "config"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// The config decoder records sources for most account fields, but not the
// simple string fields. Presence is needed when an override equals its parent.
func accountOverrideKeys(path string, account int) map[string]bool {
	if path == "" || account == 0 {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var document struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil
	}
	for _, entry := range document.Accounts {
		var id int
		if err := json.Unmarshal(entry["id"], &id); err != nil || id != account {
			continue
		}
		var overrides map[string]json.RawMessage
		if err := json.Unmarshal(entry[pfmengine.MustLookup(pfmengine.Claude).LongName], &overrides); err != nil {
			return nil
		}
		keys := make(map[string]bool, len(overrides))
		for key := range overrides {
			keys[key] = true
		}
		return keys
	}
	return nil
}

func configValue(name string, prefs pfmconfig.ClaudePrefs, machine pfmconfig.Config) string {
	switch name {
	case knobBinary:
		return prefs.Binary
	case knobCache1H:
		return strconv.FormatBool(prefs.Cache1H)
	case knobSystemPrompt:
		if prefs.SystemPrompt == "" {
			return productionMode
		}
		return prefs.SystemPrompt
	case knobNativeCursor:
		return strconv.FormatBool(prefs.NativeCursor)
	case knobMaxSubagentSpawnDepth:
		return strconv.Itoa(prefs.MaxSubagentSpawnDepth)
	case knobMaxConcurrentSubagents:
		if prefs.MaxConcurrentSubagents == 0 {
			return unsetWord
		}
		return strconv.Itoa(prefs.MaxConcurrentSubagents)
	case knobWebSearchesPerSession:
		return strconv.FormatInt(prefs.WebSearchesPerSession, 10)
	case knobAutoCompactWindow:
		return strconv.FormatInt(prefs.AutoCompactWindow, 10)
	case knobTmuxTruecolor:
		return strconv.FormatBool(prefs.TmuxTruecolor)
	case knobTheme:
		if prefs.Theme == "" {
			return unsetWord
		}
		return prefs.Theme
	case knobCleanupPeriodDays:
		return strconv.Itoa(prefs.CleanupPeriodDays)
	case knobPermissionMode:
		return prefs.PermissionMode
	case knobMCP:
		var enabled []string
		for _, name := range []string{pfmconfig.MCPServerChat, pfmconfig.MCPServerHarvester} {
			if machine.MCPServers[name].Enabled {
				enabled = append(enabled, name)
			}
		}
		if len(enabled) == 0 {
			return unsetWord
		}
		return strings.Join(enabled, ",")
	}
	return unsetWord
}
