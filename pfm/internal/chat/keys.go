package chat

import (
	"regexp"
	"sort"
	"strings"
)

var namedKeys = map[string]bool{
	"Enter": true, "Escape": true, "Space": true, "Tab": true, "BTab": true,
	"BSpace": true, "Delete": true, "DC": true, "Insert": true, "IC": true,
	"Home": true, "End": true, "Up": true, "Down": true, "Left": true,
	"Right": true, "PageUp": true, "PPage": true, "PageDown": true,
	"NPage": true, "PgUp": true, "PgDn": true,
}

var (
	functionKey = regexp.MustCompile(`^F([1-9]|1[012])$`)
	modifier    = regexp.MustCompile(`^([CMS]-)+`)
)

// KeyValid reports whether key satisfies the tmux key-name contract the chat
// keys verb and its MCP tool validate against. Unknown names must be rejected
// by callers before SendKey is invoked.
func KeyValid(key string) bool {
	if key == "" {
		return false
	}
	base := modifier.ReplaceAllString(key, "")
	if base == "" {
		return false
	}
	if base != key && len([]rune(base)) == 1 {
		return true
	}
	return namedKeys[base] || functionKey.MatchString(base)
}

// KeyNames returns the stable refusal-message suffix used by the CLI.
func KeyNames() string {
	names := make([]string, 0, len(namedKeys))
	for name := range namedKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " ") + " F1-F12, each optionally prefixed C- M- S-"
}
