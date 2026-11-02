package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// CodexModel resolves the model a Codex launch passes to `codex --model`. A bare roster tier, a
// name with no hyphen or dot such as astra, names the registered agent persona of that name,
// {home}/.claude/agents/{name}.md, whose frontmatter model is the full Codex id with its
// context-window tag dropped: astra → gpt-6-astra. A full id, an empty value, a name no persona
// registers and a persona whose model is not a gpt- id pass through unchanged. A persona file that
// exists but cannot be read is an error, never a silent pass-through. The resolved model must then
// be a slug of Codex's own catalogue (codexCatalogue) or the call is refused naming the slugs
// Codex offers; a catalogue that cannot be read lets the model through. codexHome is the Codex
// root holding models_cache.json, paths.CodexHome.
func CodexModel(model, home, codexHome string) (string, error) {
	resolved, err := resolveCodexModel(model, home)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return resolved, err
	}
	return resolved, checkCodexCatalogue(resolved, codexHome)
}

func resolveCodexModel(model, home string) (string, error) {
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

var catalogueWarned sync.Once

// CatalogueNotice is where a catalogue that could not be read is reported, once; a test swaps it.
var CatalogueNotice io.Writer = os.Stderr

// checkCodexCatalogue refuses a model the catalogue does not list. A missing file is "could not
// look", not "absent": Codex writes it on its first run, so a fresh install has none and pfm cannot
// tell a bad model from an unfetched list; like an unreadable or unparsable file or an empty list,
// it lets the model through and says why on stderr once.
func checkCodexCatalogue(model, codexHome string) error {
	path := filepath.Join(codexHome, "models_cache.json")
	raw, err := os.ReadFile(path)
	var catalogue struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &catalogue)
	}
	if err == nil && len(catalogue.Models) == 0 {
		err = errors.New("no models listed")
	}
	if err != nil {
		catalogueWarned.Do(func() {
			fmt.Fprintf(
				CatalogueNotice,
				"pfm: codex model %q not checked, Codex catalogue %s unusable: %v\n",
				model,
				path,
				err,
			)
		})
		return nil
	}
	slugs := make([]string, 0, len(catalogue.Models))
	for _, entry := range catalogue.Models {
		if entry.Slug == strings.TrimSpace(model) {
			return nil
		}
		slugs = append(slugs, entry.Slug)
	}
	return fmt.Errorf("codex model %q is not offered by Codex (catalogue %s); offered: %s",
		strings.TrimSpace(model), path, strings.Join(slugs, ", "))
}
