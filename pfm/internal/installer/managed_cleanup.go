package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// Managed cleanup states are shared by install and doctor.
const (
	ManagedCleanupOff        = "off"
	ManagedCleanupRelative   = "relative"
	ManagedCleanupMissing    = "missing"
	ManagedCleanupWrong      = "wrong"
	ManagedCleanupNoPrepend  = "no-prepend"
	ManagedCleanupOK         = "ok"
	ManagedCleanupUnreadable = "unreadable"
)

// Where prependPlugins stands across the managed sources. Claude Code 2.1.299+
// seats its built-in security plugin ahead of the person's plugins, and skips
// their hooks, on a machine with managed settings unless some managed source
// sets prependPlugins to a list of plugin ids; an empty list counts as set, and
// any other value (null included) reads as unset.
const (
	// PrependSet: pfm's drop-in carries the key as a list.
	PrependSet = "set"
	// PrependOther: another managed source sets it, an organization's own order.
	PrependOther = "other"
	// PrependInvalid: pfm's drop-in carries a value that is not a list, which
	// Claude Code ignores; pfm never rewrites an existing value.
	PrependInvalid = "invalid"
	// PrependAbsent: no managed source sets it.
	PrependAbsent = "absent"
	// PrependUnknown: a managed source could not be read, so absence is unproven.
	PrependUnknown = "unknown"
)

const (
	prependPluginsKey   = "prependPlugins"
	cleanupPeriodKey    = "cleanupPeriodDays"
	managedSettingsFile = "managed-settings.json"
	managedDropInName   = "pfm.json"
)

// ManagedCleanupStatus is the shared install and doctor inspection result.
type ManagedCleanupStatus struct {
	Path      string
	State     string
	Value     int
	KeyAbsent bool
	Err       error
	// Prepend is the PrependSet|PrependOther|PrependInvalid|PrependAbsent|
	// PrependUnknown state; empty when the inspection is off or the drop-in
	// itself is unreadable.
	Prepend string
	// PrependSource is the managed file that sets prependPlugins (PrependOther),
	// holds a value that is not a list (PrependInvalid) or could not be read
	// (PrependUnknown, with PrependErr).
	PrependSource string
	PrependErr    error
	// Want is the exact drop-in document install writes, set when State is
	// missing, wrong or no-prepend: the drop-in's own keys kept, cleanupPeriodDays
	// set, and prependPlugins:[] added only when Prepend is PrependAbsent.
	Want []byte
}

// InspectManagedCleanup reads the managed drop-in without changing it.
func InspectManagedCleanup(dir string, require bool, want int) ManagedCleanupStatus {
	status := ManagedCleanupStatus{Path: filepath.Join(dir, managedDropInName)}
	if !require {
		status.State = ManagedCleanupOff
		return status
	}
	if !filepath.IsAbs(dir) {
		status.State = ManagedCleanupRelative
		return status
	}
	document, exists, err := readManagedSettings(status.Path, os.Lstat)
	if err != nil {
		status.State = ManagedCleanupUnreadable
		status.Err = err
		return status
	}
	status.State = ManagedCleanupMissing
	if exists {
		status.State = ManagedCleanupOK
		value, present := document[cleanupPeriodKey]
		if !present {
			status.State = ManagedCleanupWrong
			status.KeyAbsent = true
		} else if decodeErr := json.Unmarshal(value, &status.Value); decodeErr != nil {
			status.State = ManagedCleanupUnreadable
			status.Err = fmt.Errorf("%s: %w", cleanupPeriodKey, decodeErr)
			return status
		} else if status.Value != want {
			status.State = ManagedCleanupWrong
		}
	}
	status.Prepend, status.PrependSource, status.PrependErr = inspectPrependPlugins(dir, document)
	if status.State == ManagedCleanupOK && status.Prepend == PrependAbsent {
		status.State = ManagedCleanupNoPrepend
	}
	switch status.State {
	case ManagedCleanupMissing, ManagedCleanupWrong, ManagedCleanupNoPrepend:
		status.Want, status.Err = managedDropInContent(document, want, status.Prepend == PrependAbsent)
		if status.Err != nil {
			status.State = ManagedCleanupUnreadable
		}
	}
	return status
}

// readManagedSettings reads one managed settings file as a JSON object. A file
// that is absent is (nil, false, nil); one that cannot be read, is not a
// regular file or is not a JSON object is an error, never absence. JSON null
// reads as an empty document.
func readManagedSettings(
	path string,
	stat func(string) (fs.FileInfo, error),
) (document map[string]json.RawMessage, exists bool, err error) {
	info, statErr := stat(path)
	if errors.Is(statErr, fs.ErrNotExist) {
		return nil, false, nil
	}
	if statErr == nil && !info.Mode().IsRegular() {
		statErr = fmt.Errorf("not a regular file")
	}
	if statErr != nil {
		return nil, true, statErr
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, true, readErr
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, true, err
	}
	return document, true, nil
}

// inspectPrependPlugins decides where prependPlugins stands: set as a list in
// pfm's own drop-in, else in another managed source (every other file Claude
// Code lists beside it — a regular file or symlink named *.json, not hidden —
// and the platform's managed-settings.json in the directory above), else
// invalid when pfm's drop-in holds a value that is not a list, else absent. A
// source that cannot be read makes the answer PrependUnknown unless another
// source already sets the key.
func inspectPrependPlugins(dir string, own map[string]json.RawMessage) (state, source string, err error) {
	ownValue, ownPresent := own[prependPluginsKey]
	if ownPresent && prependPluginsIsList(ownValue) {
		return PrependSet, "", nil
	}
	var candidates []string
	entries, listErr := os.ReadDir(dir)
	if listErr != nil && !errors.Is(listErr, fs.ErrNotExist) {
		source, err = dir, listErr
	}
	for _, entry := range entries {
		name := entry.Name()
		listed := entry.Type().IsRegular() || entry.Type()&fs.ModeSymlink != 0
		if listed && name != managedDropInName && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	candidates = append(candidates, filepath.Join(filepath.Dir(dir), managedSettingsFile))
	for _, candidate := range candidates {
		document, _, readErr := readManagedSettings(candidate, os.Stat)
		if readErr != nil {
			if err == nil {
				source, err = candidate, readErr
			}
			continue
		}
		if value, present := document[prependPluginsKey]; present && prependPluginsIsList(value) {
			return PrependOther, candidate, nil
		}
	}
	if err != nil {
		return PrependUnknown, source, err
	}
	if ownPresent {
		return PrependInvalid, filepath.Join(dir, managedDropInName), nil
	}
	return PrependAbsent, "", nil
}

// prependPluginsIsList reports whether a prependPlugins value is one Claude
// Code honours: its settings schema takes a list of plugin id strings and
// reads any other value, null included, as unset.
func prependPluginsIsList(raw json.RawMessage) bool {
	var ids []string
	return json.Unmarshal(raw, &ids) == nil && ids != nil
}

// managedDropInContent is pfm's drop-in with cleanupPeriodDays set and, when
// addPrepend, prependPlugins:[]; every other key keeps its value. Keys come out
// sorted, with no trailing newline.
func managedDropInContent(document map[string]json.RawMessage, days int, addPrepend bool) ([]byte, error) {
	merged := make(map[string]json.RawMessage, len(document)+2)
	for key, value := range document {
		merged[key] = value
	}
	merged[cleanupPeriodKey] = json.RawMessage(strconv.Itoa(days))
	if addPrepend {
		merged[prependPluginsKey] = json.RawMessage("[]")
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(merged); err != nil {
		return nil, fmt.Errorf("encode managed drop-in: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// ManagedCleanupFix returns the shell line that writes the managed drop-in
// content (status.Want).
func ManagedCleanupFix(dir, path string, content []byte) string {
	return fmt.Sprintf(
		"sudo mkdir -p %s && printf '%%s\\n' %s | sudo tee %s >/dev/null",
		shellCommandLine(dir),
		shellCommandLine(string(content)),
		shellCommandLine(path),
	)
}

func (installer *engine) installManagedCleanup(ctx context.Context) error {
	options := installer.options
	status := InspectManagedCleanup(
		options.ManagedSettingsDir,
		options.RequireManagedCleanup,
		options.CleanupPeriodDays,
	)
	switch status.State {
	case ManagedCleanupOff, ManagedCleanupRelative:
		return nil
	case ManagedCleanupUnreadable:
		return fmt.Errorf("managed-cleanup %s: %w", status.Path, status.Err)
	}
	switch status.Prepend {
	case PrependOther:
		installer.skip(
			fmt.Sprintf("managed-cleanup %s is set by %s; left alone", prependPluginsKey, status.PrependSource),
		)
	case PrependUnknown:
		installer.say(
			"  warn    managed-cleanup %s not added: %s: %v",
			prependPluginsKey,
			status.PrependSource,
			status.PrependErr,
		)
		installer.record("warn", "managed-cleanup "+prependPluginsKey+" not added", status.PrependErr)
	case PrependInvalid:
		installer.say(
			"  warn    managed-cleanup %s in %s is not a list of plugin ids, which Claude Code ignores; left alone",
			prependPluginsKey,
			status.PrependSource,
		)
		installer.record("warn", "managed-cleanup "+prependPluginsKey+" in "+status.PrependSource+" is not a list", nil)
	}
	if status.State == ManagedCleanupOK {
		installer.ok(status.Path)
		return nil
	}
	if !installer.apply {
		return installer.change("write "+status.Path, nil)
	}
	content := append(append([]byte(nil), status.Want...), '\n')
	writeManaged := options.writeManaged
	if writeManaged == nil {
		writeManaged = func(path string, content []byte) error { return atomicfile.Write(path, content, 0o644) }
	}
	directErr := writeManaged(status.Path, content)
	if directErr == nil {
		return installer.change("write "+status.Path, nil)
	}
	if !errors.Is(directErr, fs.ErrPermission) {
		return fmt.Errorf("managed-cleanup %s: write: %w", status.Path, directErr)
	}
	temporary, err := os.MkdirTemp("", "pfm-managed-cleanup-")
	if err != nil {
		return fmt.Errorf("create managed-cleanup temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	source := filepath.Join(temporary, "pfm.json")
	if err := atomicfile.Write(source, content, 0o600); err != nil {
		return fmt.Errorf("stage managed-cleanup: %w", err)
	}
	for _, args := range managedInstallArgs(source, status.Path) {
		sudoArgs := append([]string{"-n"}, args...)
		installer.say("sudo %s", shellCommandLine(sudoArgs...))
		if sudoErr := options.Runner.Run(ctx, "sudo", sudoArgs...); sudoErr != nil {
			installer.say(
				"  warn    managed-cleanup %s not written: %v; %s: %v; run: %s",
				status.Path,
				directErr,
				shellCommandLine(append([]string{"sudo"}, sudoArgs...)...),
				sudoErr,
				ManagedCleanupFix(options.ManagedSettingsDir, status.Path, status.Want),
			)
			installer.record("warn", "managed-cleanup "+status.Path+" not written", errors.Join(directErr, sudoErr))
			return nil
		}
	}
	return installer.change("write "+status.Path, nil)
}

// installProgram is install(1), which managedInstallArgs runs under sudo.
const installProgram = "install"

// shellCommandLine quotes words as a POSIX shell line a person can paste.
func shellCommandLine(words ...string) string {
	quoted := make([]string, len(words))
	for index, word := range words {
		if word != "" && strings.IndexFunc(word, func(r rune) bool { return !shellSafeRune(r) }) < 0 {
			quoted[index] = word
		} else {
			quoted[index] = action.Quote(word)
		}
	}
	return strings.Join(quoted, " ")
}

func shellSafeRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)
}
