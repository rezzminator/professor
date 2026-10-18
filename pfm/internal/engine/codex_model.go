package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CodexModel resolves the model a Codex launch passes to `codex --model`. A bare roster tier, a
// name with no hyphen or dot such as astra, names the registered agent persona of that name,
// {home}/.claude/agents/{name}.md, whose frontmatter model is the full Codex id with its
// context-window tag dropped: astra → gpt-6-astra. A full id, an empty value, a name no persona
// registers and a persona whose model is not a gpt- id pass through unchanged. A persona file that
// exists but cannot be read is an error, never a silent pass-through.
func CodexModel(model, home string) (string, error) {
	name := strings.TrimSpace(model)
	if name == "" || strings.ContainsAny(name, "-./\\[") {
		return model, nil
	}
	if home == "" {
		return "", fmt.Errorf("codex model %q: no home directory to resolve its agent persona from", name)
	}
	path := filepath.Join(home, ".claude", "agents", name+".md")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return model, nil
	}
	if err != nil {
		return "", fmt.Errorf("codex model %q: read agent persona %s: %w", name, path, err)
	}
	persona := frontmatterModel(string(raw))
	if !strings.HasPrefix(persona, "gpt-") {
		return model, nil
	}
	return persona, nil
}

// frontmatterModel reads the top-level model key of the "---" block an agent file opens with,
// its "[1m]" window tag cut; "" when the file has no such block or key.
func frontmatterModel(text string) string {
	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		value, found := strings.CutPrefix(line, "model:")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if cut := strings.IndexByte(value, '['); cut >= 0 {
			value = value[:cut]
		}
		return strings.TrimSpace(value)
	}
	return ""
}
