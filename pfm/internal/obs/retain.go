package obs

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// DefaultKeepDays is the time limit the fleet ships with: a rotated file
// whose newest record is older than thirty days is deleted (spec
// § Destinations). 0 disables the limit; the size limits stand regardless.
const DefaultKeepDays = 30

// prune is the time limit, run at open and after every rotation: each rotated
// generation (.1 … .keep-1) whose newest record is older than keepDays is
// deleted. A file's last write IS its newest record, so the age is its
// modification time on the rotator's clock — a rename keeps it, so a
// generation ages in place. The live file is never a candidate: it is only
// ever rotated. A generation that cannot be inspected or removed is an error,
// never a silent skip.
func (writer *rotator) prune() error {
	if writer.keepDays <= 0 {
		return nil
	}
	cutoff := writer.timing.Now().Add(-time.Duration(writer.keepDays) * 24 * time.Hour)
	var failures []error
	for generation := 1; generation < writer.keep; generation++ {
		aged := writer.path + "." + strconv.Itoa(generation)
		info, err := os.Stat(aged)
		if err != nil {
			if !os.IsNotExist(err) {
				failures = append(failures, fmt.Errorf("inspect activity log %s for retention: %w", aged, err))
			}
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(aged); err != nil && !os.IsNotExist(err) {
			failures = append(
				failures, fmt.Errorf("drop activity log %s past keepDays=%d: %w", aged, writer.keepDays, err),
			)
		}
	}
	return errors.Join(failures...)
}
