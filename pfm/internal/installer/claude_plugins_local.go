package installer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// An -alpha build installs each bare plugin name from a local marketplace pfm
// owns, ~/.local/share/{plugin}-dev: its .claude-plugin/marketplace.json names
// the one plugin at ./plugins/{plugin}, a copy of the checkout's
// plugins/{plugin} (under claude.pluginCheckoutRoot, else the source clone's
// plugins/) refreshed on every install. A copy, never a symlink: Claude
// Code refuses a plugin file that resolves outside its marketplace dir.

// claudePluginOwner is the GitHub owner every bare plugin name's marketplace names.
const claudePluginOwner = "rezzminator"

// devMarketplaceName is the local marketplace a plugin installs from on -alpha.
func devMarketplaceName(plugin string) string { return plugin + "-dev" }

// pluginCopySkipped names the checkout entries the copy leaves out.
func pluginCopySkipped(name string) bool { return name == "node_modules" || name == ".DS_Store" }

// localClaudePluginTarget turns an -alpha build's target local when its
// checkout exists under the checkout root, and otherwise says why not. The
// root is build.CheckoutRoot when claude.pluginCheckoutRoot is set, else the
// source clone's plugins/; a fallback names which root it tried. A source
// clone that cannot be resolved, or a checkout that cannot be read, leaves the
// target Undecided rather than falling back.
func localClaudePluginTarget(target ClaudePluginTarget, build ClaudePluginBuild) ClaudePluginTarget {
	root, rootName := build.CheckoutRoot, pfmconfig.KeyClaudePluginCheckoutRoot
	if root == "" {
		unset := pfmconfig.KeyClaudePluginCheckoutRoot + " unset"
		switch {
		case build.SourceRepoErr != nil:
			target.Undecided = fmt.Sprintf("source clone unresolved (%s): %v", unset, build.SourceRepoErr)
			return target
		case build.SourceRepo == "":
			target.Fallback = unset + " and no source clone"
			return target
		}
		root, rootName = filepath.Join(build.SourceRepo, "plugins"), "the source clone's plugins/; "+unset
	}
	checkout := filepath.Join(root, target.Name, "plugins", target.Name)
	manifest := filepath.Join(checkout, ".claude-plugin", "plugin.json")
	info, err := os.Stat(manifest)
	switch {
	case err == nil && info.Mode().IsRegular():
	case err == nil || errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		target.Fallback = "no checkout at " + manifest + " (" + rootName + ")"
		return target
	default:
		target.Undecided = fmt.Sprintf("checkout unreadable (%s): %v", rootName, err)
		return target
	}
	marketplace := filepath.Join(build.Home, ".local", "share", devMarketplaceName(target.Name))
	target.ID, target.Source, target.Local = target.DevID, marketplace, true
	target.Marketplace, target.Checkout = marketplace, checkout
	return target
}

// Mirror is the plugin's copy inside its local marketplace.
func (target ClaudePluginTarget) Mirror() string {
	return filepath.Join(target.Marketplace, "plugins", target.Name)
}

// pluginCopyState is what stands at a local target's copy path; install and
// doctor both read it from inspectPluginCopy.
type pluginCopyState int

const (
	pluginCopyDir pluginCopyState = iota
	pluginCopyMissing
	pluginCopySymlink
	pluginCopyNotDir
)

func inspectPluginCopy(mirror string) (pluginCopyState, error) {
	info, err := os.Lstat(mirror)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return pluginCopyMissing, nil
	case err != nil:
		return 0, fmt.Errorf("inspect %s: %w", mirror, err)
	case info.Mode()&fs.ModeSymlink != 0:
		return pluginCopySymlink, nil
	case !info.IsDir():
		return pluginCopyNotDir, nil
	}
	return pluginCopyDir, nil
}

// claudePluginCopyGap names what is wrong with a local target's copy, or "".
func claudePluginCopyGap(target *ClaudePluginTarget) (string, error) {
	mirror := target.Mirror()
	state, err := inspectPluginCopy(mirror)
	switch {
	case err != nil:
		return "", err
	case state == pluginCopyMissing:
		return mirror + " missing", nil
	case state == pluginCopySymlink:
		return mirror + " is a symlink: Claude refuses plugin files outside its marketplace", nil
	case state == pluginCopyNotDir:
		return mirror + " is not a directory", nil
	}
	return "", nil
}

// ensureClaudePluginCopy writes the local marketplace's manifest when it does
// not name the plugin, and refreshes the copy when it differs from the checkout.
func (installer *engine) ensureClaudePluginCopy(target *ClaudePluginTarget) error {
	manifestPath := filepath.Join(target.Marketplace, ".claude-plugin", "marketplace.json")
	current, err := devMarketplaceCurrent(manifestPath, target.Name)
	if err != nil {
		return err
	}
	if !current {
		manifest, err := json.MarshalIndent(devMarketplaceManifest{
			Name:    devMarketplaceName(target.Name),
			Owner:   &devMarketplaceOwner{Name: claudePluginOwner, URL: "https://github.com/" + claudePluginOwner},
			Plugins: []devMarketplaceEntry{{Name: target.Name, Source: "./plugins/" + target.Name}},
		}, "", "  ")
		if err != nil {
			return err
		}
		if err := installer.change("write "+manifestPath, func() error {
			return atomicfile.Write(manifestPath, append(manifest, '\n'), 0o644)
		}); err != nil {
			return err
		}
	}
	mirror := target.Mirror()
	message, err := pluginCopyChange(target.Checkout, mirror)
	if err != nil {
		return err
	}
	if message == "" {
		installer.ok("claude plugin " + target.Name + " copy " + mirror + " matches " + target.Checkout)
		return nil
	}
	return installer.change(message, func() error { return mirrorPluginTree(target.Checkout, mirror) })
}

// devMarketplaceCurrent reports whether the manifest at path is the local
// marketplace of plugin: its name and an entry for the plugin at
// ./plugins/{plugin}. A current manifest is left as it is, other keys (a
// description) included; an absent, unparsable or other manifest is not
// current and is rewritten whole.
func devMarketplaceCurrent(path, plugin string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var manifest devMarketplaceManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Name != devMarketplaceName(plugin) {
		return false, nil
	}
	for _, entry := range manifest.Plugins {
		if entry.Name == plugin && entry.Source == "./plugins/"+plugin {
			return true, nil
		}
	}
	return false, nil
}

// devMarketplaceManifest is a -dev marketplace's .claude-plugin/marketplace.json.
type devMarketplaceManifest struct {
	Name    string                `json:"name"`
	Owner   *devMarketplaceOwner  `json:"owner,omitempty"`
	Plugins []devMarketplaceEntry `json:"plugins"`
}

type devMarketplaceOwner struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// devMarketplaceEntry's Source is any: Claude also accepts an object source.
type devMarketplaceEntry struct {
	Name   string `json:"name"`
	Source any    `json:"source"`
}

// pluginCopyChange is the change that brings mirror to checkout's content, or
// "" when it already matches.
func pluginCopyChange(checkout, mirror string) (string, error) {
	state, err := inspectPluginCopy(mirror)
	switch {
	case err != nil:
		return "", err
	case state == pluginCopyMissing:
		return "copy " + checkout + " to " + mirror, nil
	case state == pluginCopySymlink:
		return "replace symlink " + mirror + " with a copy of " + checkout, nil
	case state == pluginCopyNotDir:
		return "replace " + mirror + " with a copy of " + checkout, nil
	}
	want, err := pluginTreeDigest(checkout, pluginCopySkipped)
	if err != nil {
		return "", err
	}
	// The copy skips what the checkout skips: a .DS_Store Finder leaves in it
	// is no difference.
	got, err := pluginTreeDigest(mirror, pluginCopySkipped)
	if err != nil {
		return "", err
	}
	if maps.Equal(want, got) {
		return "", nil
	}
	return "refresh " + mirror + " from " + checkout, nil
}

// walkPluginTree visits root and everything under it, root's own rel "",
// following symlinks to what they name and leaving out each entry skip
// names. A dangling link, a symlink loop or a special file is an error.
func walkPluginTree(root string, skip func(string) bool, visit func(rel, path string, info fs.FileInfo) error) error {
	active := map[string]bool{}
	var walk func(path, rel string) error
	walk = func(path, rel string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s: unsupported file mode %s", path, info.Mode())
			}
			return visit(rel, path, info)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if active[resolved] {
			return fmt.Errorf("%s: symlink loop", path)
		}
		active[resolved] = true
		defer delete(active, resolved)
		if err := visit(rel, path, info); err != nil {
			return err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if skip != nil && skip(entry.Name()) {
				continue
			}
			if err := walk(filepath.Join(path, entry.Name()), filepath.Join(rel, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root, "")
}

// pluginTreeDigest maps every entry under root to its permission bits and,
// for a file, its content hash.
func pluginTreeDigest(root string, skip func(string) bool) (map[string]string, error) {
	digest := map[string]string{}
	err := walkPluginTree(root, skip, func(rel, path string, info fs.FileInfo) error {
		if info.IsDir() {
			digest[rel] = "dir " + info.Mode().Perm().String()
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		digest[rel] = info.Mode().Perm().String() + " " + hex.EncodeToString(sum[:])
		return nil
	})
	return digest, err
}

// mirrorPluginTree replaces target (a dir, a file or a symlink) with a copy of
// source: the copy is built beside target, then renamed over it.
func mirrorPluginTree(source, target string) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".copy-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	type dirMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []dirMode
	err = walkPluginTree(source, pluginCopySkipped, func(rel, path string, info fs.FileInfo) error {
		destination := filepath.Join(staging, rel)
		if info.IsDir() {
			dirs = append(dirs, dirMode{destination, info.Mode().Perm()})
			if rel == "" {
				return nil
			}
			return os.Mkdir(destination, 0o700)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(destination, content, 0o600); err != nil {
			return err
		}
		return os.Chmod(destination, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	// Permissions last, deepest first, so a read-only source dir never stops
	// the copy filling it.
	for index := len(dirs) - 1; index >= 0; index-- {
		if err := os.Chmod(dirs[index].path, dirs[index].mode); err != nil {
			return err
		}
	}
	aside := staging + ".old"
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, aside); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		if _, statErr := os.Lstat(aside); statErr == nil {
			if restoreErr := os.Rename(aside, target); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("restore the old copy from %s: %w", aside, restoreErr))
			}
		}
		return err
	}
	return os.RemoveAll(aside)
}

// otherID is the id of ours this build does not ensure: the GitHub id
// when it ensures the -dev copy, the -dev id otherwise.
func (target ClaudePluginTarget) otherID() string {
	if target.ID == target.DevID {
		return target.GitHubID
	}
	return target.DevID
}

// planOtherCopyRetirement plans the settings.json edit that leaves only the
// build's copy loading: the other id (otherID) set false in enabledPlugins
// when it is enabled (never uninstalled) and its pluginConfigs entry copied to
// the build's id when that id has none, never overwriting. It returns "" when
// there is nothing to do. The edit writes through a settings.json symlink and
// keeps every number exactly.
func planOtherCopyRetirement(path string, target *ClaudePluginTarget) (string, func() error, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", path, err)
	}
	other := target.otherID()
	enabled, _ := document["enabledPlugins"].(map[string]any)
	disable := enabled[other] == true
	configs, _ := document["pluginConfigs"].(map[string]any)
	options, carried := configs[other]
	_, kept := configs[target.ID]
	carry := carried && !kept
	if !disable && !carry {
		return "", nil, nil
	}
	message := "copy the pluginConfigs of " + other + " to " + target.ID + " in " + path
	if disable {
		enabled[other] = false
		message = "disable " + other + " in " + path + ": " + target.ID + " replaces it"
		if carry {
			message += "; copy its pluginConfigs to " + target.ID
		}
	}
	if carry {
		configs[target.ID] = options
	}
	return message, writeSettingsDocument(path, document), nil
}

// writeSettingsDocument is the edit that writes document back through a
// settings.json symlink, keeping the file's mode.
func writeSettingsDocument(path string, document map[string]any) func() error {
	return func() error {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		var content bytes.Buffer
		encoder := json.NewEncoder(&content)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			return err
		}
		return atomicfile.Write(resolved, content.Bytes(), info.Mode().Perm())
	}
}

// retiredClaudePlugins are plugin ids once ours that a successor replaced,
// keyed by the successor's bare name. Doctor flags each enabled one
// (ClaudePluginGaps); install disables it, never uninstalls it, once the
// successor is ensured.
var retiredClaudePlugins = map[string][]string{
	"effort-live-control": {"agent-effort@agent-effort", "agent-effort@agent-effort-dev"},
}

// planRetiredPluginDisable plans the settings.json edit that sets every
// retired id of target's name false in enabledPlugins when it is enabled. It
// returns "" when none is enabled or the file is absent.
func planRetiredPluginDisable(path string, target *ClaudePluginTarget) (string, func() error, error) {
	retired := retiredClaudePlugins[target.Name]
	if len(retired) == 0 {
		return "", nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", path, err)
	}
	enabled, _ := document["enabledPlugins"].(map[string]any)
	var disabled []string
	for _, id := range retired {
		if enabled[id] == true {
			enabled[id] = false
			disabled = append(disabled, id)
		}
	}
	if len(disabled) == 0 {
		return "", nil, nil
	}
	message := "disable " + strings.Join(disabled, ", ") + " in " + path + ": retired, " + target.ID + " replaces it"
	return message, writeSettingsDocument(path, document), nil
}
