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
	"slices"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// An -alpha build installs each bare plugin name as {plugin}@professor from a
// local marketplace pfm owns, ~/.local/share/professor-dev: its
// .claude-plugin/marketplace.json, named professor, lists each plugin at
// ./plugins/{plugin}, a copy of the checkout's plugins/{plugin} (under
// claude.pluginCheckoutRoot, else the source clone's plugins/) refreshed on
// every install. A copy, never a symlink: Claude Code refuses a plugin file
// that resolves outside its marketplace dir.

// claudePluginOwner is the GitHub owner of the professor marketplace.
const claudePluginOwner = "rezzminator"

// professorDevMarketplace is the local professor marketplace an -alpha build
// installs every plugin with a checkout from.
func professorDevMarketplace(home string) string {
	return filepath.Join(home, ".local", "share", pfmconfig.ProfessorMarketplace+"-dev")
}

// pluginCopySkipped names the entries, by path under the plugin root, the copy
// leaves out: dependencies, Finder's .DS_Store, and the typings and tsconfig.json
// Claude Code generates into a plugin folder it loads, so a chat loading the copy
// never reads as drift.
func pluginCopySkipped(rel string) bool {
	switch rel {
	case filepath.Join(".claude-plugin", "types"), "tsconfig.json":
		return true
	}
	name := filepath.Base(rel)
	return name == "node_modules" || name == ".DS_Store"
}

// localClaudePluginTarget turns an -alpha build's target local when its
// checkout exists under the checkout root, and otherwise says why not. The
// root is build.CheckoutRoot when claude.pluginCheckoutRoot is set, else the
// source clone's plugins/; a fallback names which root it tried. A source
// clone that cannot be resolved, or a checkout that cannot be read, leaves the
// target Undecided rather than falling back. Only a local target has a
// source: one professor marketplace has one source, and an add from GitHub
// would re-point it off every local copy.
func localClaudePluginTarget(target ClaudePluginTarget, build ClaudePluginBuild) ClaudePluginTarget {
	target.Source = ""
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
	marketplace := professorDevMarketplace(build.Home)
	target.Source, target.Local = marketplace, true
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

// ensureClaudePluginCopy adds the plugin to the local marketplace's manifest
// when it does not list it, and refreshes the copy when it differs from the
// checkout.
func (installer *engine) ensureClaudePluginCopy(target *ClaudePluginTarget) error {
	manifestPath := filepath.Join(target.Marketplace, ".claude-plugin", "marketplace.json")
	manifest, current, err := devMarketplaceCurrent(manifestPath, target.Name)
	if err != nil {
		return err
	}
	if !current {
		message, backup := "add "+target.Name+" to "+manifestPath, ""
		if manifest.Name != pfmconfig.ProfessorMarketplace {
			// A manifest that does not parse or names another marketplace is
			// rewritten whole: its bytes are kept beside it first.
			if _, err := os.Lstat(manifestPath); err == nil {
				backup = availableBackup(manifestPath, installer.stamp)
				message += "; the manifest it replaces kept as " + backup
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("inspect %s: %w", manifestPath, err)
			}
		}
		// Every other plugin's entry stays: each target adds its own.
		manifest.Name = pfmconfig.ProfessorMarketplace
		manifest.Owner = &claudeplugin.MarketplaceOwner{
			Name: claudePluginOwner,
			URL:  "https://github.com/" + claudePluginOwner,
		}
		manifest.Plugins = append(slices.DeleteFunc(manifest.Plugins, func(entry claudeplugin.MarketplaceEntry) bool {
			return entry.Name == target.Name
		}), claudeplugin.MarketplaceEntry{Name: target.Name, Source: "./plugins/" + target.Name})
		if err := installer.writeDevMarketplaceManifest(message, manifestPath, backup, manifest); err != nil {
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

// writeDevMarketplaceManifest is the one writer of the local professor
// marketplace's manifest at path: manifest, indented, as one change line
// message, the current file copied to backup first when backup is set.
func (installer *engine) writeDevMarketplaceManifest(
	message, path, backup string,
	manifest claudeplugin.Marketplace,
) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return installer.change(message, func() error {
		if backup != "" {
			if err := copyBackup(path, backup); err != nil {
				return fmt.Errorf("back up %s: %w", path, err)
			}
		}
		return atomicfile.Write(path, append(raw, '\n'), 0o644)
	})
}

// devMarketplaceCurrent reads the local professor marketplace's manifest at
// path and reports whether it lists plugin: its name professor and an entry
// for the plugin at ./plugins/{plugin}. A current manifest is left as it is,
// other keys (a description) included; a manifest of another name or one that
// does not parse is not current and comes back empty, rewritten whole; one
// missing the plugin comes back with the entries it has.
func devMarketplaceCurrent(path, plugin string) (claudeplugin.Marketplace, bool, error) {
	var manifest claudeplugin.Marketplace
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return manifest, false, nil
	}
	if err != nil {
		return manifest, false, fmt.Errorf("read %s: %w", path, err)
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Name != pfmconfig.ProfessorMarketplace {
		return claudeplugin.Marketplace{}, false, nil
	}
	for _, entry := range manifest.Plugins {
		if entry.Name == plugin && entry.Source == "./plugins/"+plugin {
			return manifest, true, nil
		}
	}
	return manifest, false, nil
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
// following symlinks to what they name and leaving out each entry whose path
// under root skip names. A dangling link, a symlink loop or a special file is an error.
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
			if skip != nil && skip(filepath.Join(rel, entry.Name())) {
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

// planInstallOnlyDisable plans the settings.json edit that sets an enabled
// install-only target's id false: claude.plugins does not list it. It returns
// "" for a listed target, one not enabled, or an absent file. The edit writes
// through a settings.json symlink and keeps every number exactly.
func planInstallOnlyDisable(path string, target *ClaudePluginTarget) (string, func() error, error) {
	if !target.InstallOnly {
		return "", nil, nil
	}
	raw, document, err := readSettingsForEdit(path)
	if err != nil || document == nil || !disableInstallOnly(document, target) {
		return "", nil, err
	}
	message := "disable " + target.ID + " in " + path + ": " + pfmconfig.KeyClaudePlugins + " does not list " + target.Name
	return message, writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
		disableInstallOnly(fresh, target)
		return nil
	}), nil
}

// disableInstallOnly sets target.ID false in document's enabledPlugins when
// it is enabled, reporting whether it was.
func disableInstallOnly(document map[string]any, target *ClaudePluginTarget) bool {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	if enabled[target.ID] != true {
		return false
	}
	enabled[target.ID] = false
	return true
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

// planRetiredPluginDisable plans the settings.json edit that retires each of
// retired, the retired lists' plugin ids of target's name (retired.Lists
// ClaudePlugins): each enabled one set false in enabledPlugins,
// and the plugin's pluginConfigs settled on target.ID alone (retireConfigs).
// It returns "" when none is enabled, no entry needs settling or the file is
// absent. The edit writes through a settings.json symlink and keeps every
// number exactly.
func planRetiredPluginDisable(path string, target *ClaudePluginTarget, retired []string) (string, func() error, error) {
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

// retiredConfigIDs are the ids of retired holding a pluginConfigs entry,
// which target's retirement leaves to target.ID alone, in carry order. Claude
// Code reads one options entry per plugin name and the plugin warns on every
// further one ("consolidate duplicate entries"), so none of them may remain
// beside target.ID's.
func retiredConfigIDs(document map[string]any, target *ClaudePluginTarget, retired []string) []string {
	configs, _ := document["pluginConfigs"].(map[string]any)
	var ids []string
	for _, id := range retired {
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
