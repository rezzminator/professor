package claudelaunch

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type Parsed struct {
	Settings                                                      map[string]any
	SettingsFile                                                  string
	Resumed                                                       bool
	SettingsEnv                                                   map[string]string
	Hooks                                                         []Hook
	PromptFile, MCPConfig, SessionID, Resume, Name, Model, Effort string
	Fork, Autonomy                                                bool
	RemoteControl                                                 bool
	RemoteControlName                                             string
	Rest                                                          []string
	// Warnings names every part of the command line Parse could not read and
	// left out; the rest of the line is parsed as if it were absent.
	Warnings []string
}

// Parse decodes a Claude command line, including its leading binary word.
func Parse(argv []string) (Parsed, error) {
	var parsed Parsed
	if len(argv) == 0 {
		return parsed, fmt.Errorf("parse Claude argv: missing binary")
	}
	for index := 1; index < len(argv); index++ {
		flag, inlineValue, inline := splitValueFlag(argv[index])
		value := func() (string, error) {
			if inline {
				return inlineValue, nil
			}
			if flag == flagResume && (index+1 >= len(argv) || strings.HasPrefix(argv[index+1], "-")) {
				return "", nil
			}
			if index+1 >= len(argv) {
				return "", fmt.Errorf("%s requires a value", flag)
			}
			index++
			return argv[index], nil
		}
		switch flag {
		case flagRemoteControl:
			parsed.RemoteControl = true
			if inline {
				parsed.RemoteControlName = inlineValue
			} else if index+1 < len(argv) && !strings.HasPrefix(argv[index+1], "-") {
				index++
				parsed.RemoteControlName = argv[index]
			}
		case flagSessionID,
			flagResume,
			flagName,
			flagModel,
			flagEffort,
			flagPromptFile,
			flagSettings,
			flagMCPConfig:
			word, err := value()
			if err != nil {
				return Parsed{}, err
			}
			if flag != flagResume && (word == "" || strings.HasPrefix(word, "-")) {
				return Parsed{}, fmt.Errorf("%s requires a value", flag)
			}
			switch flag {
			case flagSessionID:
				parsed.SessionID = word
			case flagResume:
				parsed.Resume = word
				parsed.Resumed = true
			case flagName:
				parsed.Name = word
			case flagModel:
				parsed.Model = word
			case flagEffort:
				parsed.Effort = word
			case flagPromptFile:
				parsed.PromptFile = word
			case flagSettings:
				if !strings.HasPrefix(strings.TrimSpace(word), "{") {
					parsed.SettingsFile = word
					parsed.Rest = append(parsed.Rest, flag, word)
					continue
				}
				parsed.readInlineSettings(flag, word)
			case flagMCPConfig:
				// Render passes a file path; inline JSON is still Claude's
				// other accepted form and is validated as an object.
				if !strings.HasPrefix(strings.TrimSpace(word), "{") {
					parsed.MCPConfig = word
					continue
				}
				var object map[string]any
				if err := json.Unmarshal([]byte(word), &object); err != nil {
					parsed.Warnings = append(parsed.Warnings, fmt.Sprintf("--mcp-config: %v; kept as written", err))
				} else if object == nil {
					parsed.Warnings = append(parsed.Warnings, "--mcp-config: expected object; kept as written")
				}
				parsed.MCPConfig = word
			}
		case flagForkSession:
			parsed.Fork = true
		case flagAllowBypass:
			// The second flag activates bypass; both together are the autonomy pair.
		case flagBypass:
			parsed.Autonomy = true
		default:
			parsed.Rest = append(parsed.Rest, flag)
			if strings.HasPrefix(flag, "-") && !strings.Contains(flag, "=") && index+1 < len(argv) &&
				!strings.HasPrefix(argv[index+1], "-") {
				index++
				parsed.Rest = append(parsed.Rest, argv[index])
			}
		}
	}
	return parsed, nil
}

func splitValueFlag(word string) (string, string, bool) {
	name, value, inline := strings.Cut(word, "=")
	switch name {
	case flagSessionID,
		flagResume,
		flagName,
		flagModel,
		flagEffort,
		flagPromptFile,
		flagSettings,
		flagMCPConfig,
		flagRemoteControl:
		return name, value, inline
	default:
		return word, "", false
	}
}

// argsName reports whether the caller's own args already name the session —
// --name, --name=…, or Claude's short -n — so Render never emits a second one.
func argsName(args []string) bool {
	for _, arg := range args {
		if arg == flagName || arg == flagNameShort || strings.HasPrefix(arg, flagName+"=") {
			return true
		}
	}
	return false
}

// resumesInPlace reports whether the launch continues an existing session
// rather than forking it — Resume, or the caller's own --resume, --resume=…,
// -r, --continue or -c, with no --fork-session. Such a session carries its
// name in its transcript and Claude restores it, so Render gives it no
// --name: Claude applies a launch name only once its session registers, and
// one landing then reverts a /rename the user typed meanwhile.
func resumesInPlace(request Request) bool {
	if request.Fork || slices.Contains(request.Args, flagForkSession) {
		return false
	}
	if request.Resume != "" {
		return true
	}
	for _, arg := range request.Args {
		switch {
		case arg == flagResume, arg == flagResumeShort, arg == flagContinue, arg == flagContinueShort,
			strings.HasPrefix(arg, flagResume+"="):
			return true
		}
	}
	return false
}

// readInlineSettings reads an inline --settings object into parsed. A word that is
// not a JSON object stays in Rest for Claude to judge; an env or hooks part that
// cannot be read, or one entry of either, is left out and named in Warnings.
func (parsed *Parsed) readInlineSettings(flag, word string) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(word), &settings); err != nil {
		parsed.Warnings = append(parsed.Warnings, fmt.Sprintf("--settings: %v; left to Claude as written", err))
		parsed.Rest = append(parsed.Rest, flag, word)
		return
	}
	if settings == nil {
		parsed.Warnings = append(parsed.Warnings, "--settings: expected object; left to Claude as written")
		parsed.Rest = append(parsed.Rest, flag, word)
		return
	}
	parsed.Settings = settings
	if raw, ok := settings["env"]; ok {
		if values, ok := raw.(map[string]any); !ok {
			parsed.Warnings = append(parsed.Warnings, "--settings env: expected object; its variables left out")
		} else {
			parsed.SettingsEnv = make(map[string]string, len(values))
			for name, value := range values {
				stringValue, ok := value.(string)
				if !ok {
					parsed.Warnings = append(
						parsed.Warnings, fmt.Sprintf("--settings env.%s: expected string; left out", name))
					continue
				}
				parsed.SettingsEnv[name] = stringValue
			}
		}
	}
	var hookWarnings []string
	parsed.Hooks, hookWarnings = parseHooks(settings[knobHooks])
	for _, warning := range hookWarnings {
		parsed.Warnings = append(parsed.Warnings, "--settings hooks: "+warning)
	}
}

// parseHooks reads a settings hooks object. A group or command that is not the
// expected shape is left out and named in warnings; every other hook is kept.
func parseHooks(raw any) (result []Hook, warnings []string) {
	if raw == nil {
		return nil, nil
	}
	events, ok := raw.(map[string]any)
	if !ok {
		return nil, []string{"expected object; every hook left out"}
	}
	for event, groupsRaw := range events {
		groups, ok := groupsRaw.([]any)
		if !ok {
			warnings = append(warnings, event+": expected groups; its hooks left out")
			continue
		}
		for _, groupRaw := range groups {
			group, ok := groupRaw.(map[string]any)
			if !ok {
				warnings = append(warnings, event+": expected group object; left out")
				continue
			}
			matcher, _ := group["matcher"].(string)
			commands, ok := group[knobHooks].([]any)
			if !ok {
				warnings = append(warnings, event+": expected hooks array; group left out")
				continue
			}
			for _, commandRaw := range commands {
				command, ok := commandRaw.(map[string]any)
				if !ok {
					warnings = append(warnings, event+": expected command object; left out")
					continue
				}
				text, ok := command[commandWord].(string)
				if !ok {
					warnings = append(warnings, event+": expected command string; left out")
					continue
				}
				async, _ := command["async"].(bool)
				result = append(
					result,
					Hook{
						Event:   event,
						Matcher: matcher,
						Command: text,
						Name:    parsedHookName(event, text),
						Async:   async,
					},
				)
			}
		}
	}
	return result, warnings
}

// parsedHookName is a rendered command's registry name: its last word, where
// the registry spells a row apart from that word. A Herdr reporter row is
// "herdr-" plus its state word, and its three working rows share one command,
// so their event tells them apart.
func parsedHookName(event, command string) string {
	words := strings.Fields(command)
	if len(words) == 0 {
		return ""
	}
	name := words[len(words)-1]
	if slices.Contains(words, "herdr-state") {
		switch event {
		case HookEventUserPromptSubmit:
			return "herdr-" + name + "-prompt"
		case "PostToolUse":
			return "herdr-" + name + "-tool"
		case "PostToolUseFailure":
			return "herdr-" + name + "-tool-failure"
		default:
			return "herdr-" + name
		}
	}
	if name == "clear-name" {
		if event == HookEventUserPromptSubmit {
			return "clear-name-prompt"
		}
		return "clear-name-start"
	}
	if name == "usage-hook" {
		return "usage"
	}
	return name
}
