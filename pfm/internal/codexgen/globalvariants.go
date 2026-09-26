package codexgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GlobalAgentVariantsFile is the optional declaration beside the global agent
// sources: variant name -> {"from": source agent, <frontmatter key>: value,
// "replace": {body text: new text}}.
// Absent means "this clone declares no variants" and is never an error.
const GlobalAgentVariantsFile = "variants.json"

// globalAgentVariantFrom and globalAgentVariantReplace are the reserved keys
// of a variant declaration; every other key is a frontmatter override.
const (
	globalAgentVariantFrom    = "from"
	globalAgentVariantReplace = "replace"
)

var (
	globalAgentVariantName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	globalAgentVariantKey  = regexp.MustCompile(`^([A-Za-z-]+):`)
	// A value made only of these needs no YAML quoting; anything else is
	// single-quoted, the one YAML style with a single escape rule.
	globalAgentPlainScalar = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// GlobalAgentVariant is one Claude agent rendered from another: the source
// file with `name:` and the declared frontmatter keys overridden, the body
// byte-identical but for the declared "replace" swaps. Path is where the
// rendered file belongs in the pfm-owned generated directory; nothing is
// written by loading it.
type GlobalAgentVariant struct {
	Name    string
	From    string
	Path    string
	Content []byte
}

// LoadGlobalAgentVariants renders every variant agentsDir/variants.json
// declares, sorted by name. A missing file is (nil, nil). Every malformed
// declaration is an error naming the variant: an unparseable file, a name
// that is not kebab-case, a missing or unknown "from", a name colliding with
// an original agent, a non-string value, or an override key the source
// frontmatter does not carry (an override never ADDS a key, so a typo cannot
// ship as a silently ignored field), or a "replace" that is not an object of
// strings or whose text does not occur exactly once in the source body.
func LoadGlobalAgentVariants(agentsDir, outputDir string) ([]GlobalAgentVariant, error) {
	declPath := filepath.Join(agentsDir, GlobalAgentVariantsFile)
	raw, err := os.ReadFile(declPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", declPath, err)
	}
	var decl map[string]map[string]any
	if err := json.Unmarshal(raw, &decl); err != nil {
		return nil, fmt.Errorf("parse %s: %w", declPath, err)
	}
	names := make([]string, 0, len(decl))
	for name := range decl {
		names = append(names, name)
	}
	sort.Strings(names)

	variants := make([]GlobalAgentVariant, 0, len(names))
	for _, name := range names {
		if !globalAgentVariantName.MatchString(name) {
			return nil, fmt.Errorf("%s: variant %q: name is not kebab-case", declPath, name)
		}
		overrides := make(map[string]string, len(decl[name]))
		var swaps map[string]string
		for key, value := range decl[name] {
			if key == globalAgentVariantReplace {
				parsed, err := globalAgentVariantSwaps(value)
				if err != nil {
					return nil, fmt.Errorf("%s: variant %q: %w", declPath, name, err)
				}
				swaps = parsed
				continue
			}
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s: variant %q: %q must be a string, got %T", declPath, name, key, value)
			}
			if strings.ContainsAny(text, "\r\n") {
				return nil, fmt.Errorf("%s: variant %q: %q must be a single line", declPath, name, key)
			}
			overrides[key] = text
		}
		from := overrides[globalAgentVariantFrom]
		delete(overrides, globalAgentVariantFrom)
		if !globalAgentVariantName.MatchString(from) {
			return nil, fmt.Errorf("%s: variant %q: \"from\" must name a source agent, got %q", declPath, name, from)
		}
		if _, exists := decl[from]; exists {
			return nil, fmt.Errorf("%s: variant %q: \"from\" names %q, itself a variant", declPath, name, from)
		}
		if _, err := os.Lstat(filepath.Join(agentsDir, name+".md")); err == nil {
			return nil, fmt.Errorf("%s: variant %q collides with the original agent %s.md", declPath, name, name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspect %s: %w", filepath.Join(agentsDir, name+".md"), err)
		}
		if _, reserved := overrides["name"]; reserved {
			return nil, fmt.Errorf(
				"%s: variant %q: \"name\" is the variant's own key, never an override",
				declPath,
				name,
			)
		}
		sourcePath := filepath.Join(agentsDir, from+".md")
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("%s: variant %q: read source agent: %w", declPath, name, err)
		}
		content, err := renderGlobalAgentVariant(string(source), name, from, overrides, swaps)
		if err != nil {
			return nil, fmt.Errorf("%s: variant %q from %s: %w", declPath, name, sourcePath, err)
		}
		variants = append(variants, GlobalAgentVariant{
			Name:    name,
			From:    from,
			Path:    filepath.Join(outputDir, name+".md"),
			Content: []byte(content),
		})
	}
	return variants, nil
}

// globalAgentVariantSwaps reads a "replace" value: an object whose keys are
// non-empty body text and whose values are the text that takes their place.
func globalAgentVariantSwaps(value any) (map[string]string, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"%q must be an object of body text to new text, got %T",
			globalAgentVariantReplace,
			value,
		)
	}
	swaps := make(map[string]string, len(object))
	for text, with := range object {
		if text == "" {
			return nil, fmt.Errorf("%q carries an empty body text", globalAgentVariantReplace)
		}
		replacement, ok := with.(string)
		if !ok {
			return nil, fmt.Errorf("%q entry %q must be a string, got %T", globalAgentVariantReplace, text, with)
		}
		swaps[text] = replacement
	}
	return swaps, nil
}

// renderGlobalAgentVariant rewrites only the frontmatter lines it is told to:
// `name:` and each override key, a multi-line value's continuation lines
// going with its key. Every other frontmatter line — comments, unknown keys —
// is carried through byte-identical, and so is the body but for the swaps,
// each applied in key order to text that must occur in the body exactly once.
func renderGlobalAgentVariant(source, name, from string, overrides, swaps map[string]string) (string, error) {
	lines := strings.Split(source, "\n")
	if len(lines) == 0 || lines[0] != frontmatterFence {
		return "", fmt.Errorf("no frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == frontmatterFence {
			end = i
			break
		}
	}
	if end < 0 {
		return "", fmt.Errorf("frontmatter starts with --- but has no closing fence")
	}
	replacements := map[string]string{"name": name}
	for key, value := range overrides {
		replacements[key] = value
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, frontmatterFence,
		"# Generated by pfm from templates/global/agents/"+from+".md + "+GlobalAgentVariantsFile+" — edit the source")
	seen := make(map[string]bool, len(replacements))
	for i := 1; i < end; i++ {
		match := globalAgentVariantKey.FindStringSubmatch(lines[i])
		if match == nil {
			out = append(out, lines[i])
			continue
		}
		value, replace := replacements[match[1]]
		if !replace {
			out = append(out, lines[i])
			continue
		}
		if seen[match[1]] {
			return "", fmt.Errorf("frontmatter carries %q twice", match[1])
		}
		seen[match[1]] = true
		out = append(out, match[1]+": "+globalAgentYAMLScalar(value))
		for i+1 < end && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t")) {
			i++
		}
	}
	for key := range replacements {
		if !seen[key] {
			return "", fmt.Errorf("source frontmatter has no %q to override", key)
		}
	}
	body := strings.Join(lines[end+1:], "\n")
	texts := make([]string, 0, len(swaps))
	for text := range swaps {
		texts = append(texts, text)
	}
	sort.Strings(texts)
	for _, text := range texts {
		if count := strings.Count(body, text); count != 1 {
			return "", fmt.Errorf(
				"%q text %q occurs %d times in the body, want exactly 1",
				globalAgentVariantReplace,
				text,
				count,
			)
		}
		body = strings.Replace(body, text, swaps[text], 1)
	}
	out = append(out, frontmatterFence, body)
	return strings.Join(out, "\n"), nil
}

func globalAgentYAMLScalar(value string) string {
	if globalAgentPlainScalar.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
