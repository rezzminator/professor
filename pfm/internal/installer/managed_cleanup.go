package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	ManagedCleanupOK         = "ok"
	ManagedCleanupUnreadable = "unreadable"
)

// ManagedCleanupStatus is the shared install and doctor inspection result.
type ManagedCleanupStatus struct {
	Path      string
	State     string
	Value     int
	KeyAbsent bool
	Err       error
}

// InspectManagedCleanup reads the managed drop-in without changing it.
func InspectManagedCleanup(dir string, require bool, want int) ManagedCleanupStatus {
	status := ManagedCleanupStatus{Path: filepath.Join(dir, "pfm.json")}
	if !require {
		status.State = ManagedCleanupOff
		return status
	}
	if !filepath.IsAbs(dir) {
		status.State = ManagedCleanupRelative
		return status
	}
	info, err := os.Lstat(status.Path)
	if errors.Is(err, fs.ErrNotExist) {
		status.State = ManagedCleanupMissing
		return status
	}
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("not a regular file")
	}
	if err == nil {
		var raw []byte
		raw, err = os.ReadFile(status.Path)
		if err == nil {
			var document map[string]json.RawMessage
			err = json.Unmarshal(raw, &document)
			if err == nil {
				value, present := document["cleanupPeriodDays"]
				if !present {
					status.State = ManagedCleanupWrong
					status.KeyAbsent = true
					return status
				}
				if decodeErr := json.Unmarshal(value, &status.Value); decodeErr != nil {
					err = fmt.Errorf("cleanupPeriodDays: %w", decodeErr)
				}
			}
		}
	}
	if err != nil {
		status.State = ManagedCleanupUnreadable
		status.Err = err
		return status
	}
	status.State = ManagedCleanupOK
	if status.Value != want {
		status.State = ManagedCleanupWrong
	}
	return status
}

// ManagedCleanupFix returns the shell line that writes the managed retention setting.
func ManagedCleanupFix(dir, path string, days int) string {
	return fmt.Sprintf(
		"sudo mkdir -p %s && printf '%%s\\n' '{\"cleanupPeriodDays\":%d}' | sudo tee %s >/dev/null",
		shellCommandLine(dir),
		days,
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
	case ManagedCleanupOK:
		installer.ok(status.Path)
		return nil
	case ManagedCleanupUnreadable:
		return fmt.Errorf("managed-cleanup %s: %w", status.Path, status.Err)
	}
	if !installer.apply {
		return installer.change("write "+status.Path, nil)
	}
	content := []byte(fmt.Sprintf("{\"cleanupPeriodDays\":%d}\n", options.CleanupPeriodDays))
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
				ManagedCleanupFix(options.ManagedSettingsDir, status.Path, options.CleanupPeriodDays),
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
