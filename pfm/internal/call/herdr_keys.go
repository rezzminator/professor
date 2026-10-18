package call

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// ValidateKeyCapabilities checks the whole named-key sequence before its first mutation.
// The tmux grammar remains the CLI's contract; Herdr has a smaller capability set.
func ValidateKeyCapabilities(target Target, keys []string) error {
	if target.Backend == backendHerdr {
		for _, key := range keys {
			if _, err := herdrKey(key); err != nil {
				return err
			}
		}
	}
	return nil
}

func herdrKey(key string) (string, error) {
	original := key
	var modifiers []string
	// Internal callers also use the producer's native logical modifier spelling.
	for {
		matched := false
		for _, modifier := range []string{"ctrl", "alt", "shift"} {
			prefix := modifier + "+"
			if strings.HasPrefix(key, prefix) {
				modifiers = append(modifiers, modifier)
				key = strings.TrimPrefix(key, prefix)
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}
	for len(key) >= 2 && key[1] == '-' {
		modifier := ""
		switch key[0] {
		case 'C':
			modifier = "ctrl"
		case 'M':
			modifier = "alt"
		case 'S':
			modifier = "shift"
		}
		if modifier == "" {
			break
		}
		modifiers = append(modifiers, modifier)
		key = key[2:]
	}
	switch key {
	case "BTab":
		modifiers = append(modifiers, "shift")
		key = "tab"
	case "BSpace":
		key = "backspace"
	case "Enter", "Escape", "Space", "Tab", "Up", "Down", "Left", "Right":
	case "+":
		key = "plus"
	case "-":
		key = "minus"
	default:
		function := false
		for i := 1; i <= 12; i++ {
			function = function || key == fmt.Sprintf("F%d", i)
		}
		runes := []rune(key)
		if !function && (len(runes) != 1 || !unicode.IsGraphic(runes[0]) || unicode.IsSpace(runes[0])) {
			return "", fmt.Errorf("the Herdr backend does not support named key %q; nothing was sent", original)
		}
	}
	return strings.Join(append(modifiers, key), "+"), nil
}

// SendKey sends one named key to an already resolved seat. The target's address,
// never the calling process's environment, selects the backend.
func (engine *Engine) SendKey(ctx context.Context, target Target, key string) error {
	if target.SocketPath == "" || target.Pane == "" || key == "" {
		return fmt.Errorf("send key: socket, pane and key are required")
	}
	switch target.Backend {
	case "", backendTmux:
		return engine.tmux.SendKey(ctx, target.SocketPath, target.Pane, key)
	case backendHerdr:
		var err error
		key, err = herdrKey(key)
		if err != nil {
			return err
		}
		target, err = ProveHerdrTarget(ctx, target)
		if err != nil {
			return err
		}
		result, err := herdr.SeatRequest(ctx, target.SocketPath, "pane.send_keys", struct {
			Pane string   `json:"pane_id"`
			Keys []string `json:"keys"`
		}{target.Pane, []string{key}})
		if err != nil {
			return fmt.Errorf("herdr send key %q to %q: %w", key, target.Pane, err)
		}
		var ack struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(result, &ack); err != nil {
			return fmt.Errorf("decode herdr send key to %q: %w", target.Pane, err)
		}
		if ack.Type != "ok" {
			return fmt.Errorf("herdr send key to %q: unexpected result type %q", target.Pane, ack.Type)
		}
		return nil
	default:
		return fmt.Errorf("send key: unsupported seat backend %q", target.Backend)
	}
}
