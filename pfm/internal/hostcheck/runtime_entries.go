package hostcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// runtimeEntries names the physical top-level entries runtime evidence claims:
// each installed plugin's dir and every literal path a configured command hook
// (or the shell script it runs) references. Runtime evidence only changes
// diagnostics. It never adds an entry to the installer's shared or account
// lists, so no third-party data is relocated.
func runtimeEntries(rows *[]Row, env Env) map[string]bool {
	known := map[string]bool{}
	for name := range installedPlugins(rows, env) {
		for _, dir := range claudeDirs(env) {
			known[paths.PhysicalPath(filepath.Join(dir, name))] = true
		}
	}
	for _, dir := range claudeDirs(env) {
		settings := filepath.Join(dir, "settings.json")
		raw, err := os.ReadFile(settings)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var doc struct {
			Hooks map[string][]struct {
				Hooks []struct{ Type, Command string }
			}
		}
		if err == nil {
			err = json.Unmarshal(raw, &doc)
		}
		if err != nil {
			if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
				appendProbeError(rows, classUnclassified, settings, err)
			}
			continue
		}
		for _, groups := range doc.Hooks {
			for _, group := range groups {
				for _, hook := range group.Hooks {
					if hook.Type != "command" {
						continue
					}
					refs, err := literalRuntimePaths(hook.Command, env.Home)
					if err != nil {
						appendProbeError(rows, classUnclassified, settings, err)
						continue
					}
					for _, ref := range refs {
						markRuntimePath(known, env, ref)
						script, err := readRuntimeScript(ref)
						if err != nil {
							appendProbeError(rows, classUnclassified, ref, err)
							continue
						}
						if script == "" {
							continue
						}
						nested, err := literalRuntimePaths(script, env.Home)
						if err != nil {
							appendProbeError(rows, classUnclassified, ref, err)
							continue
						}
						for _, path := range nested {
							markRuntimePath(known, env, path)
						}
					}
				}
			}
		}
	}
	return known
}

func markRuntimePath(known map[string]bool, env Env, ref string) {
	if _, err := os.Stat(ref); err != nil {
		return
	}
	for _, dir := range claudeDirs(env) {
		root, candidate := filepath.Clean(dir), filepath.Clean(ref)
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			root, candidate = paths.PhysicalPath(dir), paths.PhysicalPath(ref)
			rel, err = filepath.Rel(root, candidate)
		}
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		name, _, _ := strings.Cut(rel, string(filepath.Separator))
		known[paths.PhysicalPath(filepath.Join(root, name))] = true
	}
}

func readRuntimeScript(path string) (string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect referenced hook file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read referenced hook file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect referenced hook file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	head := make([]byte, 2)
	n, err := file.Read(head)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read hook header: %w", err)
	}
	if n != 2 || string(head) != "#!" {
		return "", nil
	}
	if info.Size() > 65536 {
		return "", errors.New("referenced hook exceeds 64 KiB inspection bound")
	}
	tail, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return "", fmt.Errorf("read referenced hook: %w", err)
	}
	text := "#!" + string(tail)
	first, _, _ := strings.Cut(text, "\n")
	if !strings.HasSuffix(first, "/sh") && !strings.HasSuffix(first, "/bash") &&
		!strings.HasSuffix(first, "env bash") &&
		!strings.HasSuffix(first, "env sh") {
		return "", nil
	}
	return text, nil
}

// No shell runs. Only absolute literal words and simple HOME expansions are
// evidence; command substitutions, arbitrary variables and globs are not.
func literalRuntimePaths(text, home string) ([]string, error) {
	if len(text) > 65536 {
		return nil, errors.New("hook command exceeds 64 KiB inspection bound")
	}
	tree, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		return nil, fmt.Errorf("parse hook runtime references: %w", err)
	}
	var references []string
	syntax.Walk(tree, func(node syntax.Node) bool {
		if word, ok := node.(*syntax.Word); ok {
			if value, known := runtimeWord(
				word.Parts,
				home,
			); known && filepath.IsAbs(value) &&
				!strings.ContainsAny(value, "*?[") {
				references = append(references, value)
			}
			return false
		}
		return true
	})
	return references, nil
}

func runtimeWord(parts []syntax.WordPart, home string) (string, bool) {
	var value strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			value.WriteString(p.Value)
		case *syntax.SglQuoted:
			value.WriteString(p.Value)
		case *syntax.DblQuoted:
			text, ok := runtimeWord(p.Parts, home)
			if !ok {
				return "", false
			}
			value.WriteString(text)
		case *syntax.ParamExp:
			if p.Param == nil || p.Param.Value != "HOME" || p.Excl || p.Length || p.Width || p.Index != nil ||
				p.Slice != nil ||
				p.Repl != nil ||
				p.Exp != nil ||
				p.Names != 0 {
				return "", false
			}
			value.WriteString(home)
		default:
			return "", false
		}
	}
	return value.String(), true
}
