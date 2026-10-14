package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ClaudePluginMarketplaceGaps reads the store's plugins/known_marketplaces.json
// and names every target whose marketplace is registered from a source other
// than target.Source: `plugin marketplace add` answers "already added" for a
// same-named marketplace, so pfm install keeps installing from the wrong source
// until it is removed. A target with no source adds no marketplace and is not
// compared. A source compares by its form (claudeMarketplaceSourceMatches); a
// form whose record pfm cannot rebuild is not compared, never named with a
// repair that cannot close it. A missing file or entry names nothing — the
// enabled and installed checks cover a plugin never added; a file that cannot
// be read or parsed returns its error, never an answer. A string read from the
// file prints with its control characters escaped.
func ClaudePluginMarketplaceGaps(store string, targets []ClaudePluginTarget) ([]ClaudePluginGap, error) {
	path := filepath.Join(store, "plugins", "known_marketplaces.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var known map[string]struct {
		Source claudeMarketplaceRecord `json:"source"`
	}
	if err := json.Unmarshal(raw, &known); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var gaps []ClaudePluginGap
	for index := range targets {
		target := &targets[index]
		if target.Source == "" {
			continue
		}
		_, marketplace, _ := strings.Cut(target.ID, "@")
		entry, registered := known[marketplace]
		if !registered || claudeMarketplaceSourceMatches(target.Source, entry.Source) {
			continue
		}
		var got string
		switch entry.Source.Kind {
		case claudeMarketplaceGitHub:
			got = entry.Source.Repo
		case claudeMarketplaceDirectory:
			got = entry.Source.Path
		default:
			got = strings.TrimSpace(entry.Source.Kind + " " + entry.Source.URL)
		}
		gaps = append(gaps, ClaudePluginGap{
			fmt.Sprintf(
				"plugin %s marketplace %s in %s points at %s, want %s",
				target.ID,
				marketplace,
				path,
				escapeControl(got),
				target.Source,
			),
			"claude plugin marketplace remove " + marketplace + ", then " + ClaudePluginRepair,
		})
	}
	return gaps, nil
}

// claudeMarketplaceRecord is the source Claude Code records for one marketplace
// in known_marketplaces.json: a github repo, a git or url record's url, or a
// directory's path.
type claudeMarketplaceRecord struct {
	Kind string `json:"source"`
	Repo string `json:"repo"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

// The record kinds pfm compares a source with.
const (
	claudeMarketplaceGitHub    = "github"
	claudeMarketplaceDirectory = "directory"
)

// claudeGitHubShorthand is an owner/repo source, with an optional #ref.
var claudeGitHubShorthand = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+(#\S+)?$`)

// claudeMarketplaceSourceMatches reports whether record is what `plugin
// marketplace add source` registers, or a record pfm cannot compare with it. An
// owner/repo[#ref] source matches a github record of the repo, the ref
// stripped; an absolute path a directory record of the same clean path; a URL a
// record holding that url, and a github record (one Claude Code may make from a
// GitHub URL) is not compared. A relative or ~ path resolves where the add
// runs, so a directory record is not compared. Every other pairing is a
// different source.
func claudeMarketplaceSourceMatches(source string, record claudeMarketplaceRecord) bool {
	switch {
	case strings.Contains(source, "://") || strings.HasPrefix(source, "git@"):
		return record.Kind == claudeMarketplaceGitHub || (record.URL != "" && record.URL == source)
	case filepath.IsAbs(source):
		return record.Kind == claudeMarketplaceDirectory && filepath.Clean(record.Path) == filepath.Clean(source)
	case claudeGitHubShorthand.MatchString(source):
		repo, _, _ := strings.Cut(source, "#")
		return record.Kind == claudeMarketplaceGitHub && record.Repo == repo
	default:
		return record.Kind == claudeMarketplaceDirectory
	}
}
