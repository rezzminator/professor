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
	"reflect"
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

// claudePluginCopyGap names what is wrong with a local target's copy — absent,
// not a plain directory, or not the checkout's content — or "".
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
	change, err := pluginCopyChange(target.Checkout, mirror)
	if err != nil || change == "" {
		return "", err
	}
	return mirror + " differs from " + target.Checkout, nil
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

// copyIDs are the ids of a plugin of ours: its GitHub, professor and -dev
// copies, each one the target has.
func (target ClaudePluginTarget) copyIDs() []string {
	var ids []string
	for _, id := range []string{target.GitHubID, target.ProfessorID, target.DevID} {
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// OtherIDs are the ids of ours this build does not ensure: every copy but ID.
func (target ClaudePluginTarget) OtherIDs() []string {
	var others []string
	for _, id := range target.copyIDs() {
		if id != target.ID {
			others = append(others, id)
		}
	}
	return others
}

// planOtherCopyRetirement plans the settings.json edit that leaves only the
// build's copy loading: every other id (OtherIDs) set false in enabledPlugins
// when it is enabled (never uninstalled), and the pluginConfigs entry of the
// first other id that has one copied to the build's id when that id has none,
// never overwriting. An install-only target's own id is set false too:
// claude.plugins does not list it. It returns "" when there is nothing to do.
// The edit writes through a settings.json symlink and keeps every number
// exactly.
func planOtherCopyRetirement(path string, target *ClaudePluginTarget) (string, func() error, error) {
	raw, document, err := readSettingsForEdit(path)
	if err != nil || document == nil {
		return "", nil, err
	}
	disabled, carried := retireOtherCopies(document, target)
	if len(disabled) == 0 && carried == "" {
		return "", nil, nil
	}
	message := "copy the pluginConfigs of " + carried + " to " + target.ID + " in " + path
	if len(disabled) != 0 {
		reason := target.ID + " replaces it"
		if target.InstallOnly {
			reason = pfmconfig.KeyClaudePlugins + " does not list " + target.Name
		}
		message = "disable " + strings.Join(disabled, ", ") + " in " + path + ": " + reason
		switch {
		case carried == "":
		case len(disabled) == 1 && disabled[0] == carried:
			message += "; copy its pluginConfigs to " + target.ID
		default:
			message += "; copy the pluginConfigs of " + carried + " to " + target.ID
		}
	}
	return message, writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
		retireOtherCopies(fresh, target)
		return nil
	}), nil
}

// retireOtherCopies applies planOtherCopyRetirement's edit to document and
// names what it changed: the ids it set false and the id whose pluginConfigs
// entry it carried to the build's id.
func retireOtherCopies(document map[string]any, target *ClaudePluginTarget) (disabled []string, carried string) {
	others := target.OtherIDs()
	enabled, _ := document["enabledPlugins"].(map[string]any)
	if target.InstallOnly && enabled[target.ID] == true {
		disabled = append(disabled, target.ID)
	}
	for _, other := range others {
		if enabled[other] == true {
			disabled = append(disabled, other)
		}
	}
	for _, id := range disabled {
		enabled[id] = false
	}
	configs, _ := document["pluginConfigs"].(map[string]any)
	if _, kept := configs[target.ID]; !kept {
		for _, other := range others {
			if _, ok := configs[other]; ok {
				carried = other
				configs[target.ID] = configs[other]
				break
			}
		}
	}
	return disabled, carried
}

// readSettingsForEdit reads path as an edit's base: its bytes, which the edit
// compares again before its rename, and its document; nil for both when the
// file is absent.
func readSettingsForEdit(path string) ([]byte, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	var document map[string]any
	if err := unmarshalKeepingNumbers(raw, &document); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return raw, document, nil
}

// settingsEditRetries is how many times an edit re-applies itself to a
// settings.json another writer changed between its read and its rename.
const settingsEditRetries = 3

// errSettingsChanged is the precondition failure of a settings.json whose
// bytes are no longer the ones an edit was planned on.
var errSettingsChanged = errors.New("changed since pfm read it")

// writeSettingsDocument is the edit that writes document, planned on read,
// back through a settings.json symlink, keeping the file's mode; read nil
// plans on an absent file, which the edit creates 0600. A running chat may
// write the file meanwhile, create or remove it: just before the rename the
// file is read again, and when its bytes differ apply is re-run on the fresh
// document (empty for a file now absent or blank), so the other write is
// kept, and an apply error ends the edit; after settingsEditRetries such
// retries the edit fails naming the file and its repair.
func writeSettingsDocument(
	path string,
	read []byte,
	document map[string]any,
	apply func(map[string]any) error,
) func() error {
	return func() error { return commitSettingsDocument(path, read, document, apply, os.ReadFile) }
}

// commitSettingsDocument is writeSettingsDocument's write; reread reads the
// file again before each rename.
func commitSettingsDocument(
	path string,
	read []byte,
	document map[string]any,
	apply func(map[string]any) error,
	reread func(string) ([]byte, error),
) error {
	resolved, mode, err := settingsEditTarget(path)
	if err != nil {
		return err
	}
	content, err := encodeSettingsDocument(document)
	if err != nil {
		return err
	}
	return writeWhileUnchanged(path, resolved, mode, read, content, reread, func(current []byte) ([]byte, bool, error) {
		var document map[string]any
		if len(bytes.TrimSpace(current)) > 0 {
			if err := unmarshalKeepingNumbers(current, &document); err != nil {
				return nil, false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
		if document == nil {
			document = map[string]any{}
		}
		if err := apply(document); err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		content, err := encodeSettingsDocument(document)
		return content, true, err
	})
}

// encodeSettingsDocument is document as a settings.json body: two-space
// indent, HTML characters kept, a trailing newline.
func encodeSettingsDocument(document map[string]any) ([]byte, error) {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	return content.Bytes(), nil
}

// settingsKeptChangingError is writeWhileUnchanged giving up on a file another
// writer changed on every one of its edits; its text names install's repair.
type settingsKeptChangingError struct {
	path  string
	edits int
}

func (err *settingsKeptChangingError) Error() string {
	return fmt.Sprintf("%s changed under pfm's edit %d times in a row — rerun pfm install", err.path, err.edits)
}

// writeWhileUnchanged publishes content over resolved, path's physical file,
// only while reread finds it still holding read (nil: absent). Bytes another
// writer wrote, created or removed since go to replan (nil for a file now
// absent), whose content is published in turn, or nothing when it returns
// false; after settingsEditRetries such retries it fails naming path and its
// repair, the file left as the other writer left it.
func writeWhileUnchanged(
	path, resolved string,
	mode fs.FileMode,
	read, content []byte,
	reread func(string) ([]byte, error),
	replan func(current []byte) ([]byte, bool, error),
) error {
	for retry := 0; ; retry++ {
		var current []byte
		err := atomicfile.WriteIf(resolved, content, mode, func() error {
			now, err := reread(resolved)
			if errors.Is(err, fs.ErrNotExist) {
				now, err = nil, nil
			}
			if err != nil {
				return fmt.Errorf("read %s again: %w", path, err)
			}
			if !bytes.Equal(now, read) {
				current = now
				return errSettingsChanged
			}
			return nil
		})
		if !errors.Is(err, errSettingsChanged) {
			return err
		}
		if retry == settingsEditRetries {
			return &settingsKeptChangingError{path: path, edits: retry + 1}
		}
		read = current
		next, write, err := replan(current)
		if err != nil || !write {
			return err
		}
		content = next
	}
}

// settingsEditTarget is the file an edit of path publishes, path's symlink
// resolved, and the mode it keeps; a file absent now, never there or removed
// since the edit's read, is created at path itself 0600, the re-read before
// the rename re-applying an edit planned on bytes that are gone.
func settingsEditTarget(path string) (string, fs.FileMode, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		info, linkErr := os.Lstat(path)
		if linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", 0, fmt.Errorf(
				"settings file %s is a dangling symlink — repair its target and rerun pfm install",
				path,
			)
		}
		if linkErr != nil && !errors.Is(linkErr, fs.ErrNotExist) {
			return "", 0, fmt.Errorf("inspect settings file %s: %w", path, linkErr)
		}
		return path, 0o600, nil
	}
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", 0, err
	}
	return resolved, info.Mode().Perm(), nil
}

// retiredClaudePlugins are plugin ids once ours that a successor replaced,
// keyed by the successor's bare name. Doctor flags each enabled one
// (ClaudePluginGaps); install disables it, never uninstalls it, once the
// successor is ensured.
var retiredClaudePlugins = map[string][]string{
	"auto-effort-control": {
		"agent-effort@agent-effort",
		"agent-effort@agent-effort-dev",
		"agent-effort@professor",
		"agent-effort@rezzminator",
		"auto-effort@auto-effort",
		"auto-effort@auto-effort-dev",
		"auto-effort@professor",
		"auto-effort@rezzminator",
		"effort-live-control@effort-live-control",
		"effort-live-control@effort-live-control-dev",
		"effort-live-control@professor",
		"effort-live-control@rezzminator",
	},
	"forward-compaction": {
		"sub-agent-compact@professor",
		"sub-agent-compact@sub-agent-compact",
		"sub-agent-compact@sub-agent-compact-dev",
	},
}

// planRetiredPluginDisable plans the settings.json edit that retires every
// retired id of target's name: each enabled one set false in enabledPlugins,
// and the plugin's pluginConfigs settled on target.ID alone (retireConfigs).
// It returns "" when none is enabled, no entry needs settling or the file is
// absent. The edit writes through a settings.json symlink and keeps every
// number exactly.
func planRetiredPluginDisable(path string, target *ClaudePluginTarget) (string, func() error, error) {
	retired := retiredClaudePlugins[target.Name]
	if len(retired) == 0 {
		return "", nil, nil
	}
	raw, document, err := readSettingsForEdit(path)
	if err != nil || document == nil {
		return "", nil, err
	}
	disabled := disableRetiredPlugins(document, retired)
	carried, dropped := retireConfigs(document, target, retired)
	if len(disabled) == 0 && len(dropped) == 0 {
		return "", nil, nil
	}
	message := "settle the pluginConfigs of " + target.Name + " on " + target.ID + " in " + path + ": one entry per plugin"
	if len(disabled) != 0 {
		message = "disable " + strings.Join(disabled, ", ") + " in " + path + ": retired, " + target.ID + " replaces it"
	}
	if carried != "" {
		message += "; copy the pluginConfigs of " + carried + " to " + target.ID
	}
	if len(dropped) != 0 {
		message += "; drop the pluginConfigs of " + strings.Join(dropped, ", ")
	}
	return message, writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
		disableRetiredPlugins(fresh, retired)
		retireConfigs(fresh, target, retired)
		return nil
	}), nil
}

// disableRetiredPlugins sets every enabled id of retired false in document's
// enabledPlugins and names them.
func disableRetiredPlugins(document map[string]any, retired []string) []string {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	var disabled []string
	for _, id := range retired {
		if enabled[id] == true {
			enabled[id] = false
			disabled = append(disabled, id)
		}
	}
	return disabled
}

// retiredConfigIDs are the ids holding a pluginConfigs entry that target's
// retirement leaves to target.ID alone, in carry order: its other copies, then
// the retired ids. Claude Code reads one options entry per plugin name and the
// plugin warns on every further one ("consolidate duplicate entries"), so none
// of them may remain beside target.ID's.
func retiredConfigIDs(document map[string]any, target *ClaudePluginTarget, retired []string) []string {
	configs, _ := document["pluginConfigs"].(map[string]any)
	var ids []string
	for _, id := range append(target.OtherIDs(), retired...) {
		if _, ok := configs[id]; ok && id != target.ID {
			ids = append(ids, id)
		}
	}
	return ids
}

// retireConfigs settles document's pluginConfigs of target's plugin on
// target.ID: the first retiredConfigIDs entry is carried to target.ID when it
// has none, never overwriting, and then every one of them is deleted. A user's
// only copy is never lost: an entry goes only once target.ID holds one, and
// each dropped entry that differs from it is named with "(differs)". It names
// the id carried and the ids dropped.
func retireConfigs(
	document map[string]any,
	target *ClaudePluginTarget,
	retired []string,
) (carried string, dropped []string) {
	ids := retiredConfigIDs(document, target, retired)
	if len(ids) == 0 {
		return "", nil
	}
	configs, _ := document["pluginConfigs"].(map[string]any)
	if _, kept := configs[target.ID]; !kept {
		carried = ids[0]
		configs[target.ID] = configs[carried]
	}
	for _, id := range ids {
		name := id
		if !reflect.DeepEqual(configs[id], configs[target.ID]) {
			name += " (differs)"
		}
		delete(configs, id)
		dropped = append(dropped, name)
	}
	return carried, dropped
}
