package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// claude.env is pfm's map of plugin options as environment variables. pfm
// writes it into the chosen settings.json (normally the primary seat), plus
// alpha plugin-log policy into every managed config dir. It owns only keys
// it wrote: the ledger records each physical file's keys and values, the
// chosen target, and generated log policy separately from user claude.env.

const (
	claudeEnvOwnershipName    = "claude-env-ownership.json"
	claudeEnvOwnershipVersion = 1
)

// claudeEnvOwnership is the ledger: Files maps a physical settings.json path
// to the keys pfm set there and the value each was set to.
type claudeEnvOwnership struct {
	Target       string                       `json:"target,omitempty"`
	PluginLogDir string                       `json:"pluginLogDir,omitempty"`
	Version      int                          `json:"version"`
	Files        map[string]map[string]string `json:"files"`
}

// claudeEnvPlan is what one settings file needs: the keys to set, the owned
// keys to remove, the owned keys changed by hand since pfm set them, and the
// keys pfm owns there once the plan is applied.
type claudeEnvPlan struct {
	set, remove, handChanged, preserved []string
	owned                               map[string]string
}

func (plan claudeEnvPlan) changes() bool { return len(plan.set)+len(plan.remove) > 0 }

func (plan claudeEnvPlan) message(display string) string {
	return "claude.env: " + plan.actions() + " in " + display
}

// actions names the plan's sets and removes, "no key changed" when it has none.
func (plan claudeEnvPlan) actions() string {
	parts := []string{}
	if len(plan.set) > 0 {
		parts = append(parts, "set "+strings.Join(plan.set, ", "))
	}
	if len(plan.remove) > 0 {
		parts = append(parts, "remove "+strings.Join(plan.remove, ", "))
	}
	if len(parts) == 0 {
		return "no key changed"
	}
	return strings.Join(parts, "; ")
}

func claudeEnvOwnershipPath(managedRoot string) string {
	return filepath.Join(managedRoot, claudeEnvOwnershipName)
}

// readClaudeEnvOwnership reads the ledger; an absent one is empty.
func readClaudeEnvOwnership(managedRoot string) (claudeEnvOwnership, error) {
	path := claudeEnvOwnershipPath(managedRoot)
	ledger := claudeEnvOwnership{Version: claudeEnvOwnershipVersion, Files: map[string]map[string]string{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ledger, nil
	}
	if err != nil {
		return ledger, fmt.Errorf("read the ownership ledger %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return ledger, fmt.Errorf("read the ownership ledger %s: %w", path, err)
	}
	if ledger.Version != claudeEnvOwnershipVersion {
		return ledger, fmt.Errorf(
			"read the ownership ledger %s: version %d, want %d", path, ledger.Version, claudeEnvOwnershipVersion,
		)
	}
	if ledger.Files == nil {
		ledger.Files = map[string]map[string]string{}
	}
	return ledger, nil
}

// readClaudeEnvSettings reads a settings file, its bytes and its env object; an
// absent file is nil bytes and a nil document, an empty or whitespace-only one
// an empty document.
func readClaudeEnvSettings(path string) ([]byte, map[string]any, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, map[string]any{}, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	document := map[string]any{}
	if strings.TrimSpace(string(raw)) != "" {
		if err := unmarshalKeepingNumbers(raw, &document); err != nil {
			return nil, nil, nil, err
		}
		if document == nil {
			document = map[string]any{}
		}
	}
	env, err := claudeEnvOf(document)
	if err != nil {
		return nil, nil, nil, err
	}
	return raw, document, env, nil
}

// claudeEnvOf is document's env object, empty when it has none.
func claudeEnvOf(document map[string]any) (map[string]any, error) {
	value, has := document["env"]
	if !has {
		return map[string]any{}, nil
	}
	env, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("env is not an object")
	}
	return env, nil
}

// planClaudeEnv compares one file's env with want and the keys pfm owns
// there: a wanted key whose value differs is set (an unowned one is taken
// over: claude.env wins); an unowned key already holding the wanted value
// stays the user's, pfm never having written it; an owned key no longer
// wanted is removed while it holds the value pfm set, and forgotten when
// changed by hand. PluginLogDirEnv only fills an absent or untouched owned
// value: an operator value is never overwritten or adopted, even during replan.
func planClaudeEnv(env map[string]any, owned, want map[string]string) claudeEnvPlan {
	plan := claudeEnvPlan{owned: map[string]string{}}
	for _, key := range slices.Sorted(maps.Keys(want)) {
		if key == PluginLogDirEnv {
			current, present := env[key]
			previous, wasOwned := owned[key]
			if present && (!wasOwned || current != previous) {
				if current != want[key] {
					plan.preserved = append(plan.preserved, key)
				}
				if wasOwned {
					plan.handChanged = append(plan.handChanged, key)
				}
				continue
			}
		}
		if value, ok := env[key].(string); !ok || value != want[key] {
			plan.set = append(plan.set, key)
			plan.owned[key] = want[key]
		} else if _, was := owned[key]; was {
			plan.owned[key] = want[key]
		}
	}
	for _, key := range slices.Sorted(maps.Keys(owned)) {
		if _, wanted := want[key]; wanted {
			continue
		}
		current, has := env[key]
		if !has {
			continue
		}
		if value, ok := current.(string); ok && value == owned[key] {
			plan.remove = append(plan.remove, key)
		} else {
			plan.handChanged = append(plan.handChanged, key)
		}
	}
	return plan
}

// claudeEnvTarget is the settings file claude.env lands in: the primary
// seat's, else the config dir's.
func (installer *engine) claudeEnvTarget() string {
	dir := installer.options.PrimaryConfigDir
	if dir == "" {
		dir = installer.options.ConfigDir
	}
	return filepath.Join(dir, "settings.json")
}

// ensureClaudeEnv writes claude.env into the target settings file's env and
// removes from every file the ledger names the keys pfm set there and no
// longer wants; uninstall wants none. An unreadable ledger or settings file
// fails the step, nothing written to it.
func (installer *engine) ensureClaudeEnv(uninstall bool) (result error) {
	if installer.apply {
		// The parent directory is a stable lock inode even when uninstall
		// retires the managed root. No lock file keeps that root non-empty.
		path := filepath.Dir(installer.managedRoot)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return installer.fail(fmt.Errorf("create claude.env lock directory: %w", err))
		}
		lock, err := os.Open(path)
		if err != nil {
			return installer.fail(fmt.Errorf("open claude.env lock %s: %w", path, err))
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return installer.fail(
				fmt.Errorf(
					"lock claude.env ownership %s (another installer may be running; retry): %w",
					path,
					errors.Join(err, lock.Close()),
				),
			)
		}
		defer func() {
			if err := errors.Join(syscall.Flock(int(lock.Fd()), syscall.LOCK_UN), lock.Close()); err != nil {
				result = errors.Join(result, installer.fail(fmt.Errorf("release claude.env lock %s: %w", path, err)))
			}
		}()
	}
	want := maps.Clone(installer.options.ClaudeEnv)
	if uninstall {
		want = nil
	}
	ledger, err := readClaudeEnvOwnership(installer.managedRoot)
	if err != nil {
		return installer.fail(fmt.Errorf("claude.env: %w", err))
	}
	if !uninstall && len(want) > 0 {
		installer.warnUnknownClaudeEnvKeys(want)
	}
	target := installer.claudeEnvTarget()
	targetPhysical := physicalSettingsPath(target)
	wants := map[string]map[string]string{}
	var pluginLogDir string
	if !uninstall {
		wants[targetPhysical] = want
		if obs.AlphaBuild(installer.options.Version) {
			logDir := want[PluginLogDirEnv]
			if logDir == "" {
				logDir = filepath.Join(installer.options.Home, ".local", "state", "pfm", "plugin-logs")
			}
			pluginLogDir = logDir
			if !filepath.IsAbs(logDir) {
				return installer.fail(fmt.Errorf("%s must be absolute: %q", PluginLogDirEnv, logDir))
			}
			if info, err := os.Stat(logDir); errors.Is(err, fs.ErrNotExist) {
				if err := installer.change(
					"ensure plugin log directory "+logDir,
					func() error { return os.MkdirAll(logDir, 0o700) },
				); err != nil {
					return err
				}
			} else if err != nil {
				return installer.fail(fmt.Errorf("inspect plugin log directory %s: %w", logDir, err))
			} else if !info.IsDir() {
				return installer.fail(fmt.Errorf("plugin log directory %s is not a directory", logDir))
			}
			if want == nil {
				want = map[string]string{}
				wants[targetPhysical] = want
			}
			want[PluginLogDirEnv] = logDir
			dirs := append(
				[]string{ClaudeStore(installer.options.Home), installer.options.ConfigDir},
				installer.options.RosterConfigDirs...)
			for _, account := range installer.options.ClaudeAccounts {
				dirs = append(dirs, account.ConfigDir)
			}
			for _, dir := range dirs {
				if dir == "" {
					continue
				}
				path := physicalSettingsPath(filepath.Join(dir, "settings.json"))
				if wants[path] == nil {
					wants[path] = map[string]string{}
				}
				wants[path][PluginLogDirEnv] = logDir
			}
		}
	}
	if len(want) == 0 && len(ledger.Files) == 0 && ledger.Target == "" && ledger.PluginLogDir == "" {
		if !uninstall {
			installer.ok("claude.env: empty, and pfm set no key earlier")
		}
		return nil
	}
	paths := slices.Sorted(maps.Keys(ledger.Files))
	for _, path := range slices.Sorted(maps.Keys(wants)) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	next := claudeEnvOwnership{
		Version:      claudeEnvOwnershipVersion,
		Files:        maps.Clone(ledger.Files),
		PluginLogDir: pluginLogDir,
	}
	if !uninstall && len(installer.options.ClaudeEnv) > 0 {
		next.Target = ledger.Target
	}
	var failures []error
	for _, path := range paths {
		display, fileWant := path, wants[path]
		if path == targetPhysical && !uninstall {
			display = target
		}
		owned, err := installer.ensureClaudeEnvFile(path, display, ledger.Files[path], fileWant)
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf("claude.env: %w", err)))
			continue
		}
		if path == targetPhysical && !uninstall && len(installer.options.ClaudeEnv) > 0 {
			next.Target = targetPhysical
		}
		if len(owned) == 0 {
			delete(next.Files, path)
		} else {
			next.Files[path] = owned
		}
	}
	if installer.apply {
		if err := writeClaudeEnvOwnership(installer.managedRoot, next); err != nil {
			failures = append(failures, installer.fail(fmt.Errorf("claude.env: %w", err)))
		}
	}
	return errors.Join(failures...)
}

// ensureClaudeEnvFile plans and applies one file and returns the keys pfm
// owns there afterwards; on a dry run it returns the keys it would own.
func (installer *engine) ensureClaudeEnvFile(path, display string, owned, want map[string]string) (
	map[string]string, error,
) {
	plan, edit, err := planClaudeEnvEdit(path, owned, want)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", display, err)
	}
	if edit != nil {
		planned := *plan
		if err := installer.change(planned.message(display), edit); err != nil {
			return nil, fmt.Errorf("write %s: %w", display, err)
		}
		// A chat's write the edit found before its rename re-planned it: the
		// change line above names the first plan, so the one written is named too.
		if !slices.Equal(plan.set, planned.set) || !slices.Equal(plan.remove, planned.remove) {
			installer.say("  warn    claude.env: %s changed while pfm wrote it; written instead: %s",
				display, plan.actions())
		}
	}
	for _, key := range plan.handChanged {
		installer.say(
			"  warn    claude.env %s in %s was changed by hand since pfm set it: left in place, no longer pfm's",
			key, display,
		)
	}
	for _, key := range plan.preserved {
		installer.skip(
			fmt.Sprintf("claude.env %s in %s: kept hand-set value; configured value not applied", key, display),
		)
	}
	if edit == nil && len(want) > 0 && len(plan.preserved) == 0 {
		installer.ok(fmt.Sprintf("claude.env: %d keys in %s", len(want), display))
	}
	return plan.owned, nil
}

// planClaudeEnvEdit reads path and plans claude.env's edit of its env: the
// plan, and the edit applying it, nil when nothing changes. A running chat
// may write the file between this read and the edit's rename; the edit then
// plans again on what it finds, so plan is the one written once edit returns.
func planClaudeEnvEdit(path string, owned, want map[string]string) (*claudeEnvPlan, func() error, error) {
	raw, document, env, err := readClaudeEnvSettings(path)
	if err != nil {
		return nil, nil, err
	}
	plan := planClaudeEnv(env, owned, want)
	if !plan.changes() {
		return &plan, nil, nil
	}
	if document == nil {
		document = map[string]any{}
	}
	applyClaudeEnvPlan(document, env, plan, want)
	return &plan, writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
		env, err := claudeEnvOf(fresh)
		if err != nil {
			return err
		}
		plan = planClaudeEnv(env, owned, want)
		applyClaudeEnvPlan(fresh, env, plan, want)
		return nil
	}), nil
}

// applyClaudeEnvPlan sets plan's keys in env, removes its owned ones, and
// puts env back into document, dropping an emptied env.
func applyClaudeEnvPlan(document, env map[string]any, plan claudeEnvPlan, want map[string]string) {
	for _, key := range plan.set {
		env[key] = want[key]
	}
	for _, key := range plan.remove {
		delete(env, key)
	}
	if len(env) == 0 {
		delete(document, "env")
	} else {
		document["env"] = env
	}
}

// writeClaudeEnvOwnership writes the ledger 0600, or removes it when it
// records neither a key, an active claude.env target nor plugin-log policy.
func writeClaudeEnvOwnership(managedRoot string, ledger claudeEnvOwnership) error {
	path := claudeEnvOwnershipPath(managedRoot)
	if len(ledger.Files) == 0 && ledger.Target == "" && ledger.PluginLogDir == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove the ownership ledger %s: %w", path, err)
		}
		return nil
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the ownership ledger %s: %w", path, err)
	}
	if err := os.MkdirAll(managedRoot, 0o700); err != nil {
		return fmt.Errorf("write the ownership ledger %s: %w", path, err)
	}
	if err := atomicfile.Write(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the ownership ledger %s: %w", path, err)
	}
	return nil
}

// warnUnknownClaudeEnvKeys warns for each key no plugin pfm installs names
// by its prefix; the key is still set.
func (installer *engine) warnUnknownClaudeEnvKeys(env map[string]string) {
	if len(ClaudeEnvUnknownKeys(env, nil)) == 0 {
		return
	}
	build, err := installer.claudePluginBuild()
	if err != nil {
		installer.say("  warn    claude.env: the plugin list is unread, so no key's prefix was checked: %v", err)
		return
	}
	for _, key := range ClaudeEnvUnknownKeys(env, ClaudePluginTargets(build)) {
		installer.say("  warn    claude.env %s: no plugin pfm installs is named by its prefix", key)
	}
}

// ClaudeEnvUnknownKeys lists, sorted, each key of env that no target's name
// names: the name upper-snaked is the key, or the key's prefix before "_".
// PluginLogDirEnv is shared policy, known independently of a plugin target.
func ClaudeEnvUnknownKeys(env map[string]string, targets []ClaudePluginTarget) []string {
	var unknown []string
	for _, key := range slices.Sorted(maps.Keys(env)) {
		known := key == PluginLogDirEnv
		for name := range pfmconfig.ExtensionRegistry() {
			if strings.HasPrefix(key, pfmconfig.ExtensionEnvPrefix(name)) {
				known = true
			}
		}
		for index := range targets {
			prefix := strings.ToUpper(strings.ReplaceAll(targets[index].Name, "-", "_"))
			if key == prefix || strings.HasPrefix(key, prefix+"_") {
				known = true
				break
			}
		}
		if !known {
			unknown = append(unknown, key)
		}
	}
	return unknown
}

// ClaudeEnvGap is one settings drift and its effective repair. Missing is
// semantic metadata so the alpha plugin_logs check can own missing-log readiness.
type ClaudeEnvGap struct {
	Key, Problem, Fix string
	Missing           bool
}

// ClaudeEnvGaps compares claude.env with the settings file it lands in
// (settingsPath) and with every other file the ledger under managedRoot
// names; each gap is one line naming the key and the file. An unreadable
// ledger or settings file is the error, never an empty list.
func ClaudeEnvGaps(managedRoot, settingsPath string, env map[string]string) ([]ClaudeEnvGap, error) {
	ledger, err := readClaudeEnvOwnership(managedRoot)
	if err != nil {
		return nil, err
	}
	if ledger.Target != "" {
		settingsPath = ledger.Target
	}
	if len(env) == 0 && len(ledger.Files) == 0 && ledger.PluginLogDir == "" {
		return nil, nil
	}
	// Current explicit source policy supersedes the installed default in
	// every managed file, not just the primary claude.env target.
	pluginLogDir := env[PluginLogDirEnv]
	if pluginLogDir == "" {
		pluginLogDir = ledger.PluginLogDir
	}
	targetPhysical := physicalSettingsPath(settingsPath)
	var gaps []ClaudeEnvGap
	paths := ownedClaudeEnvSettingsPaths(ledger, settingsPath)
	for _, path := range paths {
		display, want := path, map[string]string(nil)
		if path == targetPhysical {
			display, want = settingsPath, env
		}
		if pluginLogDir != "" {
			want = maps.Clone(want)
			if want == nil {
				want = map[string]string{}
			}
			want[PluginLogDirEnv] = pluginLogDir
		}
		_, _, fileEnv, err := readClaudeEnvSettings(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", display, err)
		}
		plan := planClaudeEnv(fileEnv, ledger.Files[path], want)
		for _, key := range plan.set {
			value, has := fileEnv[key]
			gap := ClaudeEnvGap{Key: key, Missing: !has, Fix: "run pfm install --yes"}
			if has {
				gap.Problem = fmt.Sprintf("%s in %s env is %s; claude.env sets %q", key, display,
					claudeEnvValueText(value), want[key])
			} else {
				gap.Problem = fmt.Sprintf("%s missing from %s env; claude.env sets %q", key, display, want[key])
			}
			gaps = append(gaps, gap)
		}
		// The default is not a mandate on operator settings. An explicit
		// conflicting config still needs a repair, but install cannot replace it.
		if explicit := env[PluginLogDirEnv]; explicit != "" {
			if value, has := fileEnv[PluginLogDirEnv]; has && value != explicit &&
				!slices.Contains(plan.set, PluginLogDirEnv) {
				gaps = append(gaps, ClaudeEnvGap{
					Key: PluginLogDirEnv,
					Problem: fmt.Sprintf("%s in %s env is %s; claude.env sets %q", PluginLogDirEnv, display,
						claudeEnvValueText(value), explicit),
					Fix: "set env." + PluginLogDirEnv + " in " + display + " to the claude.env value; pfm preserves hand-set values",
				})
			}
		}
		for _, key := range plan.remove {
			gaps = append(gaps, ClaudeEnvGap{
				Key: key, Problem: fmt.Sprintf("%s left in %s env after its removal from claude.env", key, display),
				Fix: "run pfm install --yes",
			})
		}
	}
	return gaps, nil
}

func claudeEnvValueText(value any) string {
	if text, ok := value.(string); ok {
		return fmt.Sprintf("%q", text)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}

// ClaudeEnvSettingsPath returns the last install's actual claude.env target;
// pre-target ledgers and uninstalled homes retain the caller's fallback.
func ClaudeEnvSettingsPath(managedRoot, fallback string) (string, error) {
	ledger, err := readClaudeEnvOwnership(managedRoot)
	if err != nil {
		return "", err
	}
	if ledger.Target != "" {
		return ledger.Target, nil
	}
	return fallback, nil
}

// ClaudeEnvSettingsPaths returns exactly the physical settings files checked by
// ClaudeEnvGaps, including a custom target and historical ledger-only files.
func ClaudeEnvSettingsPaths(managedRoot, fallback string) ([]string, error) {
	ledger, err := readClaudeEnvOwnership(managedRoot)
	if err != nil {
		return nil, err
	}
	return ownedClaudeEnvSettingsPaths(ledger, fallback), nil
}

func ownedClaudeEnvSettingsPaths(ledger claudeEnvOwnership, fallback string) []string {
	if ledger.Target != "" {
		fallback = ledger.Target
	}
	target := physicalSettingsPath(fallback)
	paths := slices.Sorted(maps.Keys(ledger.Files))
	if !slices.Contains(paths, target) {
		paths = append(paths, target)
	}
	return paths
}
