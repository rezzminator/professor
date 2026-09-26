package claudelaunch

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Parsed struct {
	Settings                                                      map[string]any
	SettingsEnv                                                   map[string]string
	Hooks                                                         []Hook
	PromptFile, MCPConfig, SessionID, Resume, Name, Model, Effort string
	Fork, Autonomy                                                bool
	Rest                                                          []string
}

// Parse decodes a Claude command line, including its leading binary word.
func Parse(argv []string) (Parsed, error) {
	var parsed Parsed
	if len(argv) == 0 {
		return parsed, fmt.Errorf("parse Claude argv: missing binary")
	}
	for index := 1; index < len(argv); index++ {
		flag := argv[index]
		value := func() (string, error) {
			if index+1 >= len(argv) {
				return "", fmt.Errorf("%s requires a value", flag)
			}
			index++
			return argv[index], nil
		}
		switch flag {
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
			switch flag {
			case flagSessionID:
				parsed.SessionID = word
			case flagResume:
				parsed.Resume = word
			case flagName:
				parsed.Name = word
			case flagModel:
				parsed.Model = word
			case flagEffort:
				parsed.Effort = word
			case flagPromptFile:
				parsed.PromptFile = word
			case flagSettings:
				if err := json.Unmarshal([]byte(word), &parsed.Settings); err != nil {
					return Parsed{}, fmt.Errorf("--settings: %w", err)
				}
				if parsed.Settings == nil {
					return Parsed{}, fmt.Errorf("--settings: expected object")
				}
				if raw, ok := parsed.Settings["env"]; ok {
					values, ok := raw.(map[string]any)
					if !ok {
						return Parsed{}, fmt.Errorf("--settings env: expected object")
					}
					parsed.SettingsEnv = make(map[string]string, len(values))
					for name, value := range values {
						stringValue, ok := value.(string)
						if !ok {
							return Parsed{}, fmt.Errorf("--settings env.%s: expected string", name)
						}
						parsed.SettingsEnv[name] = stringValue
					}
				}
				parsed.Hooks, err = parseHooks(parsed.Settings[knobHooks])
				if err != nil {
					return Parsed{}, fmt.Errorf("--settings hooks: %w", err)
				}
			case flagMCPConfig:
				var object map[string]any
				if err := json.Unmarshal([]byte(word), &object); err != nil {
					return Parsed{}, fmt.Errorf("--mcp-config: %w", err)
				}
				if object == nil {
					return Parsed{}, fmt.Errorf("--mcp-config: expected object")
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

func parseHooks(raw any) ([]Hook, error) {
	if raw == nil {
		return nil, nil
	}
	events, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object")
	}
	var result []Hook
	for event, groupsRaw := range events {
		groups, ok := groupsRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: expected groups", event)
		}
		for _, groupRaw := range groups {
			group, ok := groupRaw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: expected group object", event)
			}
			matcher, _ := group["matcher"].(string)
			commands, ok := group[knobHooks].([]any)
			if !ok {
				return nil, fmt.Errorf("%s: expected hooks array", event)
			}
			for _, commandRaw := range commands {
				command, ok := commandRaw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s: expected command object", event)
				}
				text, ok := command[commandWord].(string)
				if !ok {
					return nil, fmt.Errorf("%s: expected command string", event)
				}
				async, _ := command["async"].(bool)
				words := strings.Fields(text)
				name := ""
				if len(words) != 0 {
					name = words[len(words)-1]
				}
				if name == "usage-hook" {
					name = "usage"
				}
				result = append(result, Hook{Event: event, Matcher: matcher, Command: text, Name: name, Async: async})
			}
		}
	}
	return result, nil
}
