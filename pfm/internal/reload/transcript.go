package reload

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Transcript is a Claude transcript opened for ONE guarded append: exclusive
// flock, the whole file read under the lock, a byte-for-byte backup written
// O_EXCL before the record lands, fsync after. This is the discipline
// cmd/pfm's appendResumeInjection established for a dormant-session inject
// (chat_inject_resume.go) and the custom-title record `reload --new` leaves
// on the session it abandons (newname.go) — one writer, shared, never a
// second copy of the lock/backup dance to drift.
type Transcript struct {
	path string
	file *os.File
	raw  []byte
}

// OpenTranscript opens path read-write, takes the exclusive lock and reads
// the whole file, so the caller composes its record against the tail the
// lock guarantees is current. Close releases the lock.
func OpenTranscript(path string) (transcript *Transcript, returnErr error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open transcript %q: %w", path, err)
	}
	defer func() {
		if returnErr != nil {
			if err := file.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %q: %w", path, err))
			}
		}
	}()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock transcript %q: %w", path, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind transcript %q: %w", path, err)
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read transcript %q: %w", path, err)
	}
	return &Transcript{path: path, file: file, raw: raw}, nil
}

// Raw is the transcript's whole content as read under the lock.
func (transcript *Transcript) Raw() []byte { return transcript.raw }

// Append writes the pre-append content to backup (created O_EXCL — an
// existing file is never overwritten), then appends record as one JSONL line
// — newline-separated from a tail that lacks one — and syncs.
func (transcript *Transcript) Append(record []byte, backup string) error {
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return fmt.Errorf("create transcript backup directory %q: %w", filepath.Dir(backup), err)
	}
	backupFile, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create transcript backup %q: %w", backup, err)
	}
	if _, err := backupFile.Write(transcript.raw); err != nil {
		return errors.Join(fmt.Errorf("write transcript backup %q: %w", backup, err), backupFile.Close())
	}
	if err := backupFile.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync transcript backup %q: %w", backup, err), backupFile.Close())
	}
	if err := backupFile.Close(); err != nil {
		return fmt.Errorf("close transcript backup %q: %w", backup, err)
	}
	if _, err := transcript.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek transcript %q for append: %w", transcript.path, err)
	}
	if len(transcript.raw) > 0 && transcript.raw[len(transcript.raw)-1] != '\n' {
		if _, err := transcript.file.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("separate transcript append %q: %w", transcript.path, err)
		}
	}
	if _, err := transcript.file.Write(append(record, '\n')); err != nil {
		return fmt.Errorf("append transcript %q: %w", transcript.path, err)
	}
	if err := transcript.file.Sync(); err != nil {
		return fmt.Errorf("sync transcript %q: %w", transcript.path, err)
	}
	return nil
}

// Close releases the lock and the file; both failures are reported.
func (transcript *Transcript) Close() error {
	var errs error
	if err := syscall.Flock(int(transcript.file.Fd()), syscall.LOCK_UN); err != nil {
		errs = errors.Join(errs, fmt.Errorf("unlock transcript %q: %w", transcript.path, err))
	}
	if err := transcript.file.Close(); err != nil {
		errs = errors.Join(errs, fmt.Errorf("close transcript %q: %w", transcript.path, err))
	}
	return errs
}
