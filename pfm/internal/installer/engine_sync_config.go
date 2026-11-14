package installer

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

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/enginesync"
)

// engineSyncManagedBy is the machine.managedBy value that marks the global
// engine-sync file as pfm's: an install writes only an absent file or one
// carrying it.
const engineSyncManagedBy = "professor"

// engineSyncFileMode is the global file's mode: it names the host's Codex homes.
const engineSyncFileMode fs.FileMode = 0o600

// engineSyncTargets is the "targets" object of the global file: the machine
// roster this install ran global over, the one `engine-sync hook claude start`
// reads to run global again.
type engineSyncTargets struct {
	CodexHomes       []string `json:"codexHomes"`
	ClaudeConfigDirs []string `json:"claudeConfigDirs"`
	SourceRepo       string   `json:"sourceRepo"`
	// Capabilities are the host's capability names: engine-sync reads them on
	// every run, including the `hook claude start` refresh pfm does not flag.
	Capabilities []string `json:"capabilities"`
}

type engineSyncFileState int

const (
	engineSyncFileAbsent engineSyncFileState = iota
	engineSyncFileOwned
	engineSyncFileUnowned
)

// engineSyncFilePlan is what an install does to the global file.
type engineSyncFilePlan struct {
	path   string
	source string
	// future is the file an install writes; nil when the clone ships no
	// template.
	future []byte
	state  engineSyncFileState
	// write is true when the apply creates or rewrites the file.
	write bool
}

func engineSyncPolicySource(sourceRepo string) string {
	return filepath.Join(sourceRepo, "templates", "global", "engine-sync.json")
}

// engineSyncFileBytes is the template with the roster spliced in as its last
// key, so the written file keeps the template's layout.
func engineSyncFileBytes(template []byte, targets engineSyncTargets) ([]byte, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(template, &keys); err != nil {
		return nil, fmt.Errorf("parse engine-sync machine policy: %w", err)
	}
	if _, found := keys["targets"]; found {
		return nil, errors.New("engine-sync machine policy carries a \"targets\" key: pfm writes it")
	}
	encoded, err := json.MarshalIndent(targets, "  ", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode engine-sync targets: %w", err)
	}
	body := bytes.TrimRight(template, " \t\r\n")
	if !bytes.HasSuffix(body, []byte("}")) {
		return nil, errors.New("engine-sync machine policy does not end in a closing brace")
	}
	body = bytes.TrimRight(body[:len(body)-1], " \t\r\n")
	separator := ",\n  "
	if len(keys) == 0 {
		separator = "\n  "
	}
	out := append([]byte{}, body...)
	out = append(out, separator+"\"targets\": "...)
	out = append(out, encoded...)
	return append(out, "\n}\n"...), nil
}

// engineSyncFileOwnedBy reports the state of the global file: absent, pfm's
// (a regular file that parses and names machine.managedBy "professor") or
// someone else's (any other file, a link, a directory, unparsable text).
func engineSyncFileOwnedBy(path string) (engineSyncFileState, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return engineSyncFileAbsent, nil
	}
	if err != nil {
		return engineSyncFileUnowned, fmt.Errorf("inspect engine-sync global file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return engineSyncFileUnowned, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return engineSyncFileUnowned, fmt.Errorf("read engine-sync global file %s: %w", path, err)
	}
	var parsed struct {
		Machine struct {
			ManagedBy string `json:"managedBy"`
		} `json:"machine"`
	}
	if json.Unmarshal(raw, &parsed) != nil || parsed.Machine.ManagedBy != engineSyncManagedBy {
		return engineSyncFileUnowned, nil
	}
	return engineSyncFileOwned, nil
}

// engineSyncRoster is the targets this install records.
func (installer *engine) engineSyncRoster(sourceRepo string) engineSyncTargets {
	return engineSyncTargets{
		CodexHomes:       append([]string{}, installer.codexHomes()...),
		ClaudeConfigDirs: []string{installer.options.ConfigDir},
		SourceRepo:       sourceRepo,
		Capabilities:     append([]string{}, enginesync.HostCapabilities(installer.options.GptAgents)...),
	}
}

// engineSyncFilePlan decides what an install does to the global file.
func (installer *engine) engineSyncFilePlan(sourceRepo string) (engineSyncFilePlan, error) {
	plan := engineSyncFilePlan{
		path:   enginesync.GlobalConfigPath(installer.options.Home),
		source: engineSyncPolicySource(sourceRepo),
	}
	state, err := engineSyncFileOwnedBy(plan.path)
	if err != nil {
		return plan, err
	}
	plan.state = state
	template, err := os.ReadFile(plan.source)
	if errors.Is(err, fs.ErrNotExist) {
		return plan, nil
	}
	if err != nil {
		return plan, fmt.Errorf("read engine-sync machine policy %s: %w", plan.source, err)
	}
	plan.future, err = engineSyncFileBytes(template, installer.engineSyncRoster(sourceRepo))
	if err != nil {
		return plan, fmt.Errorf("build engine-sync global file from %s: %w", plan.source, err)
	}
	plan.write = state == engineSyncFileAbsent ||
		(state == engineSyncFileOwned && !sameFile(plan.path, plan.future, engineSyncFileMode))
	return plan, nil
}

// wireEngineSyncConfig writes the global file (the template plus this
// install's roster) when it is absent or pfm's.
func (installer *engine) wireEngineSyncConfig() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return fmt.Errorf("resolve the engine-sync policy source repository: %w", err)
	}
	plan, err := installer.engineSyncFilePlan(sourceRepo)
	if err != nil {
		return err
	}
	switch {
	case plan.future == nil:
		installer.skip("engine-sync machine policy source missing: " + plan.source)
		return nil
	case plan.state == engineSyncFileUnowned:
		installer.skip(plan.path + " is not managed by professor — left unchanged " +
			"(set machine.managedBy to \"" + engineSyncManagedBy + "\" to let pfm write it)")
	case plan.write:
		if err := installer.change("write "+plan.path, func() error {
			return atomicfile.Write(plan.path, plan.future, engineSyncFileMode)
		}); err != nil {
			return err
		}
	default:
		installer.ok(plan.path)
	}
	return nil
}

// retireEngineSyncFile removes the global file on uninstall once
// engine-sync global has used it: only when it is pfm's, and not when
// engine-sync was absent, since the file holds the targets a second uninstall
// needs.
func (installer *engine) retireEngineSyncFile() error {
	if installer.engineSyncAbsent {
		return nil
	}
	path := enginesync.GlobalConfigPath(installer.options.Home)
	state, err := engineSyncFileOwnedBy(path)
	if err != nil {
		return err
	}
	if state != engineSyncFileOwned {
		return nil
	}
	return installer.retire(path, "engine-sync machine configuration")
}

// engineSyncPreviewSkip is the reason a dry run does not preview `engine-sync
// global`, "" when it does. engine-sync reads {home}/.claude/engine-sync.json,
// so a preview of an install that would write that file has no real home to
// run against (a dry run writes nothing, and a scratch home of links is refused
// by engine-sync's own symlink-parent rule): the preview is skipped by name and
// the apply, which writes the file first, runs the real call. A rule link the
// pass retires skips it the same way. A dry run that changes neither previews
// against the real home.
func (installer *engine) engineSyncPreviewSkip(sourceRepo string) (string, error) {
	if installer.apply {
		return "", nil
	}
	var changes []string
	if installer.options.Mode != ModeUninstall {
		plan, err := installer.engineSyncFilePlan(sourceRepo)
		if err != nil {
			return "", err
		}
		if plan.write {
			changes = append(changes, "write "+plan.path)
		}
	}
	// A rule link this pass retires (a renamed rule's dead link) is still on
	// disk in a dry run, where engine-sync's rule walk refuses it.
	rules := filepath.Join(installer.options.ConfigDir, globalRulesRegistry) + string(filepath.Separator)
	var retiredRules []string
	for path := range installer.removedPaths {
		if strings.HasPrefix(path, rules) {
			retiredRules = append(retiredRules, path)
		}
	}
	sort.Strings(retiredRules)
	for _, path := range retiredRules {
		changes = append(changes, "retire "+path)
	}
	if len(changes) == 0 {
		return "", nil
	}
	return "engine-sync global preview skipped: this install would " + strings.Join(changes, " and ") +
		", which engine-sync reads before it plans; the apply runs it after", nil
}

// relativeUnder is path relative to root, slash-separated, when it lies under it.
func relativeUnder(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
