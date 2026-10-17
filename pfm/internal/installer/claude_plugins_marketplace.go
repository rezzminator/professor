package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
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
				EscapeControl(got),
				target.Source,
			),
			claudeMarketplaceGapFix(target, marketplace),
		})
	}
	return gaps, nil
}

// claudeMarketplaceGapFix repairs a marketplace registered from another
// source: pfm install re-points a local target's own -dev marketplace; any
// other is removed by hand first.
func claudeMarketplaceGapFix(target *ClaudePluginTarget, marketplace string) string {
	if target.Local {
		return ClaudePluginRepair
	}
	return "claude plugin marketplace remove " + marketplace + ", then " + ClaudePluginRepair
}

// claudeMarketplaceRemoveFix is the repair when a re-point did not take: a
// remove, which deletes what the marketplace's plugins saved.
func claudeMarketplaceRemoveFix(marketplace string) string {
	return "claude plugin marketplace remove " + marketplace +
		" deletes the saved options and data of its plugins; run it only if they may go, then " + ClaudePluginRepair
}

// professorMarketplaceName is the marketplace the source clone's own
// .claude-plugin/marketplace.json registers: {name}@professor is a copy of a
// plugin of ours.
const professorMarketplaceName = "professor"

// marketplacePluginName is a plugin name the source clone's marketplace may
// list: one safe path element, as a bare claude.plugins entry is.
var marketplacePluginName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// MarketplacePluginNames reads the plugin names the source clone's
// marketplace, {sourceRepo}/.claude-plugin/marketplace.json, lists, in its
// order, and returns that path beside them. A file that cannot be read returns
// its error wrapped, so a missing file still matches fs.ErrNotExist; a file
// that cannot be parsed, lists no plugins or lists a name twice or one that is
// not a plugin name returns its error, never a partial list.
func MarketplacePluginNames(sourceRepo string) (string, []string, error) {
	path := filepath.Join(sourceRepo, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, nil, fmt.Errorf("read the marketplace plugin list %s: %w", path, err)
	}
	var manifest devMarketplaceManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return path, nil, fmt.Errorf("parse the marketplace plugin list %s: %w", path, err)
	}
	if len(manifest.Plugins) == 0 {
		return path, nil, fmt.Errorf("the marketplace plugin list %s lists no plugins", path)
	}
	names := make([]string, 0, len(manifest.Plugins))
	for index, entry := range manifest.Plugins {
		if !marketplacePluginName.MatchString(entry.Name) {
			return path, nil, fmt.Errorf("the marketplace plugin list %s: plugins[%d] name %q is not a plugin name",
				path, index, EscapeControl(entry.Name))
		}
		if slices.Contains(names, entry.Name) {
			return path, nil, fmt.Errorf("the marketplace plugin list %s lists %s twice", path, entry.Name)
		}
		names = append(names, entry.Name)
	}
	return path, names, nil
}

// WithMarketplacePlugins resolves an -alpha build's plugins against the source
// clone's marketplace, {SourceRepo}/.claude-plugin/marketplace.json: with
// claude.plugins unset (PluginsDefault) Plugins becomes every plugin it lists,
// in its order, and Unlisted every plugin it lists that claude.plugins names in
// no form, bare or name@marketplace. A release, and a build with no source
// clone, is returned as it is; so is an explicit list beside a clone with no
// marketplace file. A file that cannot be read, parsed or validated, a source
// clone that could not be resolved, and a default list with no file to read
// return the build unchanged with the error: install and doctor name it as a
// failure and still plan the list, never an empty one.
func (build ClaudePluginBuild) WithMarketplacePlugins() (ClaudePluginBuild, error) {
	if !obs.AlphaBuild(build.Version) {
		return build, nil
	}
	if build.SourceRepoErr != nil {
		return build, fmt.Errorf("marketplace plugin list unread: source clone unresolved: %w", build.SourceRepoErr)
	}
	if build.SourceRepo == "" {
		return build, nil
	}
	_, names, err := MarketplacePluginNames(build.SourceRepo)
	if errors.Is(err, fs.ErrNotExist) && !build.PluginsDefault {
		return build, nil
	}
	if err != nil {
		return build, err
	}
	if build.PluginsDefault {
		build.Plugins = make([]pfmconfig.ClaudePlugin, 0, len(names))
		for _, name := range names {
			build.Plugins = append(build.Plugins, pfmconfig.ClaudePlugin{ID: name})
		}
	}
	listed := map[string]bool{}
	for _, plugin := range build.Plugins {
		listed[plugin.Name()] = true
	}
	build.Unlisted = nil
	for _, name := range names {
		if !listed[name] {
			build.Unlisted = append(build.Unlisted, name)
		}
	}
	return build, nil
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
