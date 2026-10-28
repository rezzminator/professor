package config

import (
	"encoding/json"
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// DefaultChatBackend chooses from the requester seat or current pane environment.
const DefaultChatBackend = "auto"

// DefaultHerdrTabStatus is the installed-version colored tab marker policy.
const DefaultHerdrTabStatus = "emoji"

// ChatConfig selects the default birth backend.
type ChatConfig struct {
	Backend        string
	HerdrTabStatus string
}

type rawChat struct {
	Backend        *string `json:"backend,omitempty"`
	HerdrTabStatus *string `json:"herdrTabStatus,omitempty"`
	// RetiredHerdrTabPriority accepts old config files (the example shipped the
	// key) under the strict decoder; it is never read or written.
	RetiredHerdrTabPriority json.RawMessage `json:"herdrTabPriority,omitempty"`
}

// ChatSummary renders the resolved chat settings and their configuration sources.
func (config Config) ChatSummary() string {
	return fmt.Sprintf(
		"config chat.backend=%s (%s)\nconfig chat.herdrTabStatus=%s (%s)\n",
		config.Chat.Backend,
		config.Source("chat.backend"),
		config.Chat.HerdrTabStatus,
		config.Source("chat.herdrTabStatus"),
	)
}

// marshalChat preserves explicit selections without making an untouched default
// file incompatible with older strict readers that have no chat section.
func marshalChat(config Config) map[string]any {
	value := make(map[string]any)
	if config.Chat.Backend != DefaultChatBackend || config.Source("chat.backend") == SourceFile {
		value["backend"] = config.Chat.Backend
	}
	if config.Chat.HerdrTabStatus != DefaultHerdrTabStatus || config.Source("chat.herdrTabStatus") == SourceFile {
		value["herdrTabStatus"] = config.Chat.HerdrTabStatus
	}
	return value
}

// applyChatAndAsk decodes the chat birth and one-shot engine preferences.
func applyChatAndAsk(result *Config, raw rawConfig) error {
	if raw.Chat != nil && raw.Chat.Backend != nil {
		switch *raw.Chat.Backend {
		case DefaultChatBackend, "tmux", "herdr":
			result.Chat.Backend = *raw.Chat.Backend
			result.Sources["chat.backend"] = SourceFile
		default:
			return fmt.Errorf("config %s: chat.backend must be auto, tmux or herdr", result.Path)
		}
	}
	if raw.Chat != nil && raw.Chat.HerdrTabStatus != nil {
		switch *raw.Chat.HerdrTabStatus {
		case DefaultHerdrTabStatus, "text", "off":
			result.Chat.HerdrTabStatus = *raw.Chat.HerdrTabStatus
			result.Sources["chat.herdrTabStatus"] = SourceFile
		default:
			return fmt.Errorf("config %s: chat.herdrTabStatus must be emoji, text or off", result.Path)
		}
	}
	if raw.Ask != nil {
		if raw.Ask.Engine != nil {
			id, err := pfmengine.Parse(*raw.Ask.Engine)
			if err != nil {
				return fmt.Errorf("config %s: %w", result.Path, err)
			}
			result.Ask.Engine = id
			result.Sources["ask.engine"] = SourceFile
		}
		for id, rawPrefs := range raw.Ask.Prefs {
			prefs := result.Ask.Prefs[id]
			applyEnginePrefs(&prefs, rawPrefs)
			result.Ask.Prefs[id] = prefs
			name := pfmengine.MustLookup(id).LongName
			if rawPrefs.Model != nil {
				result.Sources["ask."+name+".model"] = SourceFile
			}
			if rawPrefs.Effort != nil {
				result.Sources["ask."+name+".effort"] = SourceFile
			}
		}
	}

	return nil
}
