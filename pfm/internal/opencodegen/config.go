package opencodegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	opencodeAllow = "allow"
	opencodeDeny  = "deny"
	opencodeType  = "type"
)

func compileConfig(root string, add func(generatedFile), problem, warn func(string, ...any)) {
	path := filepath.Join(root, ".opencode", "opencode.jsonc")
	owned := map[string]bool{"$schema": true, "permission": true, "mcp": true}
	extra := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil && !hasMarker(string(raw)):
		// Not ours: its keys are not read, and reconcile's claimProblem
		// refuses the output below exactly as it refuses an unmarked agent,
		// command, or skill link — a failed build, the file left as is.
	case err == nil:
		var object map[string]json.RawMessage
		if parseErr := json.Unmarshal(parseOpenCodeJSONC(raw), &object); parseErr != nil {
			warn("unparseable %s (%v) — regenerating from sources; adopter keys could not be preserved", path, parseErr)
		}
		for key, value := range object {
			if !owned[key] {
				extra[key] = value
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		problem("read %s: %v", path, err)
	}

	mcp, hasMCP := compileOpenCodeMCP(filepath.Join(root, ".mcp.json"), warn, problem)
	add(generatedFile{Path: path, Content: serializeOpenCodeConfig(extra, mcp, hasMCP)})
}

var permissionPolicy = map[string]any{
	"*":                  opencodeAllow,
	"external_directory": opencodeAllow,
	"doom_loop":          opencodeAllow,
	"edit": map[string]string{
		"**": opencodeAllow, "**/.claude/**": opencodeDeny, "AGENTS.md": opencodeDeny, "**/AGENTS.md": opencodeDeny,
		"CLAUDE.md": opencodeDeny, "**/CLAUDE.md": opencodeDeny, ".opencode/**": opencodeDeny,
	},
	"bash": bashPermission(),
}

// gitWriteVerbs are the Git verbs only gitter may run; each is denied bare
// and in its `git -C <dir>` form, as a whole word — `git merge-base` or a
// path like `src/reset.go` is not the verb.
var gitWriteVerbs = []string{"commit", "push", "tag", "merge", "rebase", "reset", "cherry-pick", "revert", "am"}

// permissionRule is one OpenCode pattern → action pair.
type permissionRule struct{ Pattern, Action string }

// orderedRules renders as a JSON object in slice order: OpenCode lets the
// last matching pattern win, so `"*": allow` comes first and every deny after.
type orderedRules []permissionRule

func (rules orderedRules) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, rule := range rules {
		if i > 0 {
			out.WriteByte(',')
		}
		pattern, err := json.Marshal(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("encode permission pattern %q: %w", rule.Pattern, err)
		}
		action, err := json.Marshal(rule.Action)
		if err != nil {
			return nil, fmt.Errorf("encode permission action %q: %w", rule.Action, err)
		}
		out.Write(pattern)
		out.WriteByte(':')
		out.Write(action)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func bashPermission() orderedRules {
	rules := orderedRules{{Pattern: "*", Action: opencodeAllow}}
	for _, prefix := range []string{"git ", "git -C * "} {
		for _, verb := range gitWriteVerbs {
			rules = append(rules,
				permissionRule{Pattern: prefix + verb, Action: opencodeDeny},
				permissionRule{Pattern: prefix + verb + " *", Action: opencodeDeny})
		}
	}
	return append(rules, permissionRule{Pattern: "gh release*", Action: opencodeDeny})
}

func compileOpenCodeMCP(path string, warn, problem func(string, ...any)) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		problem("read %s: %v", path, err)
		return nil, false
	}
	var source struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		problem("parse %s: %v", path, err)
		return nil, true
	}
	result := map[string]any{}
	for name, encoded := range source.MCPServers {
		var server struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		}
		if err := json.Unmarshal(encoded, &server); err != nil {
			problem("parse .mcp.json server %q: %v", name, err)
			continue
		}
		switch {
		case server.Command != "":
			command := append([]string{server.Command}, server.Args...)
			entry := map[string]any{opencodeType: "local", "command": command, "enabled": true}
			if len(server.Env) != 0 {
				entry["environment"] = server.Env
			}
			result[name] = entry
		case server.URL != "":
			entry := map[string]any{opencodeType: "remote", "url": server.URL, "enabled": true}
			if len(server.Headers) != 0 {
				entry["headers"] = server.Headers
			}
			result[name] = entry
		default:
			warn("mcp %s: neither command nor url — skipped, never silently dropped from the config", name)
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err == nil {
			for key := range fields {
				if key != "command" && key != "args" && key != "env" && key != "url" && key != opencodeType &&
					key != "headers" {
					warn("mcp %s: fields not mapped to opencode config: %s", name, key)
				}
			}
		}
	}
	return result, true
}

func serializeOpenCodeConfig(extra map[string]json.RawMessage, mcp map[string]any, hasMCP bool) string {
	var body bytes.Buffer
	body.WriteString(
		"// " + newMarker + " from pfm opencode build + .mcp.json; do not edit — edit the source, then re-run: pfm opencode build\n",
	)
	body.WriteString("// Sources: this compiler owns $schema, permission, and mcp. Other keys survive regeneration.\n")
	body.WriteString("{\n  \"$schema\": \"https://opencode.ai/config.json\"")
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		encoded, _ := json.Marshal(extra[key])
		body.WriteString(",\n  " + quoteOpenCodeJSON(key) + ": " + indentOpenCodeJSON(encoded, "  "))
	}
	permission, _ := json.Marshal(permissionPolicy)
	body.WriteString(",\n  \"permission\": " + indentOpenCodeJSON(permission, "  "))
	if hasMCP && len(mcp) != 0 {
		encoded, _ := json.Marshal(mcp)
		body.WriteString(",\n  \"mcp\": " + indentOpenCodeJSON(encoded, "  "))
	}
	body.WriteString("\n}\n")
	return body.String()
}

func indentOpenCodeJSON(raw []byte, prefix string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) == 1 {
		return lines[0]
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func quoteOpenCodeJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func quoteOpenCodeYAML(value string) string { return quoteOpenCodeJSON(value) }

func parseOpenCodeJSONC(raw []byte) []byte {
	var out bytes.Buffer
	inString, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			out.WriteByte(c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(raw) && raw[i+1] == '/' {
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteByte(c)
	}
	return out.Bytes()
}
