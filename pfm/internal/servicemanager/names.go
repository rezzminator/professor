package servicemanager

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// UnitNames is the namespace of one installation's supervised services.
// Its zero value preserves the shipped pfm names.
type UnitNames struct{ prefix string }

var (
	unitPrefixPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	canonicalSystemdUnit = regexp.MustCompile(`^pfm-(?:mcp|name-sync|schedule|theme|reminder)\.(?:service|timer|path)$`)
)

var unitReferenceForms = []struct {
	pattern       *regexp.Regexp
	before, after string
}{
	{regexp.MustCompile(`(?m)^Unit=pfm-[a-z0-9-]+\.(?:service|timer|path)$`), "Unit=", ""},
	{regexp.MustCompile(`<string>com\.professor\.pfm\.[a-z0-9.-]+</string>`), "<string>", "</string>"},
	{
		regexp.MustCompile(`(?m)^Environment="XPC_SERVICE_NAME=com\.professor\.pfm\.[a-z0-9.-]+"$`),
		`Environment="XPC_SERVICE_NAME=`,
		`"`,
	},
}

// UnitNamesFrom resolves the one prefix used for unit files, their references,
// service-manager commands and readers. An explicit empty value is an error.
func UnitNamesFrom(env paths.Env) (UnitNames, error) {
	prefix, present := env.Lookup(paths.EnvUnitPrefix)
	if !present {
		return UnitNames{}, nil
	}
	if !unitPrefixPattern.MatchString(prefix) {
		return UnitNames{}, fmt.Errorf(
			"%s must be 1..63 lowercase letters, digits or hyphens, starting with a letter (got %q)",
			paths.EnvUnitPrefix,
			prefix,
		)
	}
	return UnitNames{prefix: prefix}, nil
}

// CurrentUnitNames resolves this process's installation namespace.
func CurrentUnitNames() (UnitNames, error) { return UnitNamesFrom(paths.OSEnv{}) }

// Prefix is the configured prefix; an empty string means the production default.
func (names UnitNames) Prefix() string { return names.prefix }

// Matches reports whether a supervised filename belongs to this namespace.
func (names UnitNames) Matches(name string) bool {
	prefix := names.prefix
	if prefix == "" {
		prefix = "pfm"
	}
	return strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, "com.professor."+prefix+".")
}

// Name maps a canonical service name or filename into this namespace. Mapping
// an already qualified name again keeps it unchanged.
func (names UnitNames) Name(name string) string {
	if names.prefix == "" || names.prefix == "pfm" {
		return name
	}
	base := filepath.Base(name)
	resolved := base
	switch {
	case strings.HasPrefix(base, names.prefix+"-") && !canonicalSystemdUnit.MatchString(base),
		strings.HasPrefix(base, "com.professor."+names.prefix+"."):
		return name
	case strings.HasPrefix(base, "pfm-") && (strings.HasSuffix(base, ".service") || strings.HasSuffix(base, ".timer") || strings.HasSuffix(base, ".path")):
		resolved = names.prefix + strings.TrimPrefix(base, "pfm")
	case strings.HasPrefix(base, "com.professor.pfm."):
		resolved = "com.professor." + names.prefix + strings.TrimPrefix(base, "com.professor.pfm")
	}
	if resolved == base {
		return name
	}
	if directory := filepath.Dir(name); directory != "." {
		return filepath.Join(directory, resolved)
	}
	return resolved
}

// Render qualifies unit and label references in a trusted service template.
func (names UnitNames) Render(content string) string {
	// Rewrite name-bearing fields without changing caller paths or template bytes.
	for _, form := range unitReferenceForms {
		content = form.pattern.ReplaceAllStringFunc(content, func(match string) string {
			name := strings.TrimSuffix(strings.TrimPrefix(match, form.before), form.after)
			return form.before + names.Name(name) + form.after
		})
	}
	return content
}
