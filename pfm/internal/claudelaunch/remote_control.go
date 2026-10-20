package claudelaunch

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/naming"
)

// RefreshRemoteControl reapplies the current preference to fresh or stored
// interactive argv. The flag's optional name must not consume a prompt: use the
// chat name when known, otherwise insert it immediately before another flag.
// Print-mode argv never carries it.
func RefreshRemoteControl(args []string, enabled bool, name string) []string {
	clean := make([]string, 0, len(args)+2)
	printMode := false
	existingName := ""
	for index := 0; index < len(args); index++ {
		word := args[index]
		if word == "--" {
			clean = append(clean, args[index:]...)
			break
		}
		key, value, inline := strings.Cut(word, "=")
		if key == flagRemoteControl {
			if inline {
				existingName = value
			} else if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
				existingName = args[index]
			}
			continue
		}
		if key == "-p" || key == "--print" {
			printMode = true
		}
		clean = append(clean, word)
		if inline {
			if name == "" && (key == flagName || key == flagNameShort) {
				name = value
			}
			continue
		}
		if remoteControlValueFlag(key) && index+1 < len(args) &&
			(key != flagResume && key != flagResumeShort || !strings.HasPrefix(args[index+1], "-")) {
			index++
			clean = append(clean, args[index])
			if name == "" && (key == flagName || key == flagNameShort) {
				name = args[index]
			}
		}
	}
	if !enabled || printMode {
		return clean
	}
	if name == "" {
		name = existingName
	}
	if name = naming.LaunchName(name); name != "" {
		if strings.HasPrefix(name, "-") {
			// An optional argument beginning with '-' needs the equals form;
			// otherwise Claude treats the chat name as another CLI option.
			return append([]string{flagRemoteControl + "=" + name}, clean...)
		}
		return append([]string{flagRemoteControl, name}, clean...)
	}
	// Every Render recipe has a settings flag. A legacy prompt-only recipe
	// instead gets a terminator, keeping every positional word out of the name.
	for index, word := range clean {
		if strings.HasPrefix(word, "-") {
			return append(append(append([]string(nil), clean[:index]...), flagRemoteControl), clean[index:]...)
		}
	}
	return append([]string{flagRemoteControl, "--"}, clean...)
}

func remoteControlValueFlag(flag string) bool {
	switch flag {
	case flagSettings, flagMCPConfig, flagModel, flagEffort, flagSessionID, flagResume,
		flagResumeShort, flagName, flagNameShort, flagPromptFile, flagSystemPrompt:
		return true
	default:
		return false
	}
}
