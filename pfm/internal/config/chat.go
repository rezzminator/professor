package config

import (
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// DefaultChatBackend chooses from the requester seat or current pane environment.
const DefaultChatBackend = "auto"

// ChatConfig selects the default birth backend.
type ChatConfig struct{ Backend string }

type rawChat struct {
	Backend *string `json:"backend,omitempty"`
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
