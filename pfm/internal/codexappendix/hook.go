package codexappendix

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const marker = "# Professor Codex appendix"

// PromptPath is the installer's staged, model-independent appendix.
func PromptPath(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "prompts", "codex-appendix.md")
}

// Run answers Codex's snake_case hook input with its camelCase output contract.
// Unknown history never masquerades as absence: it emits a visible warning and
// the current appendix, preserving ephemeral and remote launches.
func Run(input io.Reader, output io.Writer, home string) (returnErr error) {
	raw, err := io.ReadAll(io.LimitReader(input, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("appendix hook input exceeds 1 MiB")
	}
	var request struct {
		Event      string  `json:"hook_event_name"`
		Source     string  `json:"source"`
		Transcript *string `json:"transcript_path"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("decode appendix hook input: %w", err)
	}
	if request.Event != "SessionStart" {
		return fmt.Errorf("unexpected appendix event %q", request.Event)
	}
	switch request.Source {
	case "startup", "resume", "clear", "compact":
	default:
		return fmt.Errorf("unexpected appendix source %q", request.Source)
	}
	file, err := os.Open(PromptPath(home))
	if err != nil {
		return fmt.Errorf("read Professor appendix (run pfm install): %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Professor appendix: %w", err))
		}
	}()
	prompt, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil {
		return err
	}
	if len(prompt) > 16384 || strings.TrimSpace(string(prompt)) == "" || !utf8.Valid(prompt) {
		return fmt.Errorf("professor appendix must be nonempty UTF-8, at most 16 KiB")
	}
	body := marker + "\n\n" + strings.TrimSpace(string(prompt))
	present, older, historyErr := presentInHistory(request.Transcript, body)
	response := map[string]any{}
	if !present || historyErr != nil {
		response["hookSpecificOutput"] = map[string]any{"hookEventName": "SessionStart", "additionalContext": body}
	}
	if historyErr != nil {
		response["systemMessage"] = "Professor appendix injected; duplicate context could not be ruled out: " + historyErr.Error()
	} else if older {
		response["systemMessage"] = "Professor appendix updated; an earlier version remains in this conversation until compaction or a new session."
	}
	if err := json.NewEncoder(output).Encode(response); err != nil {
		return fmt.Errorf("write appendix hook response: %w", err)
	}
	return nil
}
