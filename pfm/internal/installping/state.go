package installping

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// The files in pfm's state directory: the install id, made once, and one
// claim file per UTC day a ping went out on, install-ping-day-{YYYY-MM-DD}.
const (
	idFile        = "install-ping-id"
	dayFilePrefix = "install-ping-day-"
)

// installID is the install's id: read from dir, or made once as a random
// UUIDv4 when missing or not one, never derived from the machine or the user.
// A concurrent first run that wins the create is read back, not overwritten;
// a malformed file is removed and made again through the same create.
func installID(dir string) (string, error) {
	if err := checkDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, idFile)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if id := strings.TrimSpace(string(raw)); idPattern.MatchString(id) {
			return id, nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("remove the malformed install id: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("read the install id: %w", err)
	}
	id := uuid.NewV4().String()
	err = atomicfile.Create(path, []byte(id+"\n"), 0o600)
	if errors.Is(err, fs.ErrExist) {
		return installID(dir)
	}
	if err != nil {
		return "", fmt.Errorf("create the install id: %w", err)
	}
	return id, nil
}

// claimDay claims day for the one ping it may carry: claimed is false when an
// earlier ping, of this or another process, already holds it. The claim is an
// exclusive create, so two carriers racing for one day send it once.
func claimDay(dir, day string) (claimed bool, err error) {
	if err := checkDir(dir); err != nil {
		return false, err
	}
	err = atomicfile.Create(filepath.Join(dir, dayFilePrefix+day), []byte(day+"\n"), 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim the day %s: %w", day, err)
	}
	return true, nil
}

// pruneDays removes every day claim in dir but day's, so the claims never pile up.
func pruneDays(dir, day string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list the day claims: %w", err)
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, dayFilePrefix) || name == dayFilePrefix+day {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove the day claim %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// checkDir refuses a state directory that is not absolute, so a runtime with
// no state path never writes into the working directory.
func checkDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("state directory %q is not absolute", dir)
	}
	return nil
}
