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

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
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
	path, known, err := claudeplugin.ReadKnownMarketplaces(store)
	if err != nil {
		return nil, err
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
		case claudeplugin.MarketplaceGitHub:
			got = entry.Source.Repo
		case claudeplugin.MarketplaceDirectory:
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
// source: pfm install re-points the professor marketplace of a plugin of
// ours; any other is removed by hand first.
func claudeMarketplaceGapFix(target *ClaudePluginTarget, marketplace string) string {
	if target.Own {
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

// professorMarketplaceRepository is the GitHub repository whose
// .claude-plugin/marketplace.json is the professor marketplace a release
// installs every plugin of ours from.
const professorMarketplaceRepository = claudePluginOwner + "/" + pfmconfig.ProfessorMarketplace

// marketplacePluginName is a plugin name the source clone's marketplace may
// list: one safe path element, as a bare claude.plugins entry is.
var marketplacePluginName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// MarketplacePluginNames reads the plugin names the source clone's
// marketplace, {sourceRepo}/.claude-plugin/marketplace.json, lists, in its
// order, and returns that path beside them. A file that cannot be read returns
// its error wrapped, so a missing file still matches fs.ErrNotExist; a file
// that cannot be parsed or lists no plugins returns its error and no list. An
// entry listed twice or whose name is not a plugin name is skipped and named
// in the error returned beside every valid name.
func MarketplacePluginNames(sourceRepo string) (string, []string, error) {
	path := filepath.Join(sourceRepo, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, nil, fmt.Errorf("read the marketplace plugin list %s: %w", path, err)
	}
	var manifest claudeplugin.Marketplace
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return path, nil, fmt.Errorf("parse the marketplace plugin list %s: %w", path, err)
	}
	if len(manifest.Plugins) == 0 {
		return path, nil, fmt.Errorf("the marketplace plugin list %s lists no plugins", path)
	}
	names := make([]string, 0, len(manifest.Plugins))
	var skipped []error
	for index, entry := range manifest.Plugins {
		switch {
		case !marketplacePluginName.MatchString(entry.Name):
			skipped = append(skipped, fmt.Errorf(
				"the marketplace plugin list %s: plugins[%d] name %q is not a plugin name",
				path, index, EscapeControl(entry.Name)))
		case slices.Contains(names, entry.Name):
			skipped = append(skipped, fmt.Errorf("the marketplace plugin list %s lists %s twice", path, entry.Name))
		default:
			names = append(names, entry.Name)
		}
	}
	if len(names) == 0 {
		return path, nil, errors.Join(skipped...)
	}
	return path, names, errors.Join(skipped...)
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
// failure and still plan the list, never an empty one. Entries the file lists
// badly are named in the error beside the build derived from the valid ones.
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
	if err != nil && len(names) == 0 {
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
	return build, err
}

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
func claudeMarketplaceSourceMatches(source string, record claudeplugin.MarketplaceSource) bool {
	switch {
	case strings.Contains(source, "://") || strings.HasPrefix(source, "git@"):
		return record.Kind == claudeplugin.MarketplaceGitHub || (record.URL != "" && record.URL == source)
	case filepath.IsAbs(source):
		return record.Kind == claudeplugin.MarketplaceDirectory && filepath.Clean(record.Path) == filepath.Clean(source)
	case claudeGitHubShorthand.MatchString(source):
		repo, _, _ := strings.Cut(source, "#")
		return record.Kind == claudeplugin.MarketplaceGitHub && record.Repo == repo
	default:
		return record.Kind == claudeplugin.MarketplaceDirectory
	}
}
