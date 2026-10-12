package archive

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// One relocation is a transaction against writers that take no lock of ours.
//
//  1. Intent: a manifest row naming the source's device and inode, durable
//     before anything moves (AppendManifest).
//  2. Publication: the archive name is created exclusively as a hard link,
//     which keeps the inode, and made durable. Across filesystems there is no
//     such link and the move is refused: a copy would strand a writer's
//     descriptor on a deleted inode.
//  3. A fresh live reading and the plan's view of the source: a writer that
//     registered, or a transcript that changed, withdraws this transaction's
//     own publication and keeps the kill.
//  4. The source name is removed.
//  5. A second fresh live reading, the source path and the archived file: a
//     writer seen now gets the archived inode linked back at its path, so a
//     descriptor it holds keeps writing where the chat lives. A path a writer
//     recreated meanwhile is never overwritten: both copies stay and the move
//     names them both.
//
// A publication belongs to the newest intent whose recorded identity is the
// file at the archived name (recordedArchive), never to an intent that merely
// names it: the loser of a race for one name cannot claim the winner's file,
// and a retry or a restore recognises a publication whose source removal
// failed.

var (
	removeArchiveSource    = os.Remove
	linkArchiveFile        = os.Link
	syncArchivePublication = syncPublishedArchive
	beforeArchiveCopyWrite func()
)

// performMoves runs every planned relocation through the transaction. The
// live re-readings happen once per phase, not once per move.
func (runner *Runner) performMoves(moves []Move) []Move {
	stamp := runner.now().UTC().Format("20060102-150405")
	performed := append([]Move(nil), moves...)
	for index := range performed {
		if !performed[index].Replayed {
			runner.publish(&performed[index], stamp)
		}
	}
	runner.holdBackWriters(performed, false)
	for index := range performed {
		move := &performed[index]
		if !move.published || move.Failed != "" {
			continue
		}
		if err := removeArchiveSource(move.Source); err != nil {
			move.Failed = fmt.Sprintf(
				"published %s, but could not remove %s (a retry finishes the move, restore undoes it): %v",
				move.Target, move.Source, err,
			)
			continue
		}
		move.removed = true
	}
	runner.holdBackWriters(performed, true)
	for index := range performed {
		if performed[index].removed && performed[index].Failed == "" {
			performed[index].Applied = true
		}
	}
	return performed
}

// publish records the intent and creates the archive name, or resumes this
// chat's own earlier publication whose source removal never happened.
func (runner *Runner) publish(move *Move, stamp string) {
	current, err := os.Stat(move.Source)
	if err != nil {
		move.Failed = fmt.Sprintf("inspect %s before publishing: %v", move.Source, err)
		return
	}
	if !current.Mode().IsRegular() {
		move.Failed = fmt.Sprintf("%s is not a regular transcript", move.Source)
		return
	}
	if changedSince(move.planned, current) {
		move.Failed = fmt.Sprintf(
			"%s changed after the live snapshot, so a writer may have started; kept, kill retained",
			move.Source,
		)
		return
	}
	move.planned = current
	if err := os.MkdirAll(filepath.Dir(move.Target), 0o700); err != nil {
		move.Failed = err.Error()
		return
	}
	if _, err := os.Lstat(move.Target); err == nil {
		resumed, err := runner.resumePublication(move)
		switch {
		case err != nil:
			move.Failed = fmt.Sprintf("inspect the publication already at %s: %v", move.Target, err)
		case !resumed:
			move.Failed = "a file is already archived under that name"
		}
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		move.Failed = fmt.Sprintf("inspect archive target %s: %v", move.Target, err)
		return
	}
	if move.fileID, err = fileIdentity(current); err != nil {
		move.Failed = err.Error()
		return
	}
	if err := AppendManifest(runner.paths.ArchiveDir, *move, stamp); err != nil {
		move.Failed = "record relocation before move: " + err.Error()
		return
	}
	if err := linkArchiveFile(move.Source, move.Target); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			// Only a hard link keeps a running writer's descriptor attached to
			// the archived transcript; a copy would strand it on a deleted inode.
			move.Failed = fmt.Sprintf(
				"the archive %s is on another filesystem than %s, where only a copy could move it "+
					"and a writer's descriptor would be stranded; transcript kept, kill retained",
				move.Target, move.Source,
			)
			return
		}
		move.Failed = fmt.Sprintf("publish %s without overwriting %s: %v", move.Source, move.Target, err)
		return
	}
	if err := syncArchivePublication(move.Target); err != nil {
		move.Failed = fmt.Sprintf("make archive publication durable before removing %s: %v", move.Source, err)
		return
	}
	if move.archived, err = os.Stat(move.Target); err != nil {
		move.Failed = fmt.Sprintf("inspect the publication at %s: %v", move.Target, err)
		return
	}
	move.published = true
}

// resumePublication adopts the publication at the archive name only when the
// manifest proves it is this chat's, from this source, and it still holds
// exactly the source's bytes.
func (runner *Runner) resumePublication(move *Move) (bool, error) {
	row, owned, _, err := recordedArchive(runner.paths.ArchiveDir, move.ID)
	if err != nil || !owned || row.Original != move.Source || row.Archived != move.Target || row.State == "" {
		return false, err
	}
	same, err := sameTranscript(move.Source, move.Target)
	if err != nil || !same {
		return false, err
	}
	archived, err := os.Stat(move.Target)
	if err != nil {
		return false, fmt.Errorf("inspect the publication at %s: %w", move.Target, err)
	}
	move.fileID, move.archived, move.published = row.FileID, archived, true
	return true, nil
}

// holdBackWriters checks every move at one phase — before or after its source
// was removed — against a fresh live reading and the files themselves.
func (runner *Runner) holdBackWriters(moves []Move, removed bool) {
	var live map[string]struct{}
	var readErr error
	read := false
	for index := range moves {
		move := &moves[index]
		if !move.published || move.Failed != "" || move.removed != removed {
			continue
		}
		if !read {
			live, readErr = runner.liveSessions()
			read = true
		}
		if reason := writerEvidence(move, live, readErr); reason != "" {
			withdraw(move, reason)
		}
	}
}

// writerEvidence names why a writer may own the transcript now, or "".
func writerEvidence(move *Move, live map[string]struct{}, readErr error) string {
	if readErr != nil {
		return fmt.Sprintf("the live re-reading could not run: %v", readErr)
	}
	if transcriptLive(live, move.ID, move.Source) {
		return "a writer for this transcript registered after the live snapshot"
	}
	if !move.removed {
		current, err := os.Stat(move.Source)
		if err != nil {
			return fmt.Sprintf("re-inspect %s: %v", move.Source, err)
		}
		if changedSince(move.planned, current) {
			return "the transcript changed after it was published"
		}
		return ""
	}
	if _, err := os.Lstat(move.Source); err == nil {
		return "the transcript path was recreated after its removal"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Sprintf("re-inspect %s: %v", move.Source, err)
	}
	current, err := os.Stat(move.Target)
	if err != nil {
		return fmt.Sprintf("re-inspect %s: %v", move.Target, err)
	}
	if changedSince(move.archived, current) {
		return "the archived transcript changed after its source was removed"
	}
	return ""
}

// withdraw undoes this transaction and says what is where. It removes only
// a name whose identity is this transaction's own publication.
func withdraw(move *Move, reason string) {
	if !move.removed {
		move.published = false
		if err := removeOwnPublication(move); err != nil {
			move.Failed = fmt.Sprintf("%s; %v; transcript kept at %s, kill retained", reason, err, move.Source)
			return
		}
		move.Failed = fmt.Sprintf("%s; archive withdrawn, transcript kept at %s, kill retained", reason, move.Source)
		return
	}
	err := os.Link(move.Target, move.Source)
	if errors.Is(err, fs.ErrExist) {
		move.Failed = fmt.Sprintf(
			"%s; both copies are kept: the archived history at %s and the recreated file at %s; kill retained",
			reason, move.Target, move.Source,
		)
		return
	}
	if err != nil {
		move.Failed = fmt.Sprintf(
			"%s; could not return the transcript to %s: %v; it stays archived at %s, restore puts it back; kill retained",
			reason,
			move.Source,
			err,
			move.Target,
		)
		return
	}
	move.removed = false
	if err := removeOwnPublication(move); err != nil {
		move.Failed = fmt.Sprintf("%s; transcript returned to %s; %v; kill retained", reason, move.Source, err)
		return
	}
	move.published = false
	move.Failed = fmt.Sprintf("%s; transcript returned to %s, kill retained", reason, move.Source)
}

func removeOwnPublication(move *Move) error {
	info, err := os.Lstat(move.Target)
	if err != nil {
		return fmt.Errorf("could not inspect the publication at %s: %w", move.Target, err)
	}
	id, err := fileIdentity(info)
	if err != nil {
		return err
	}
	if id != move.fileID {
		return fmt.Errorf("%s no longer holds this transaction's publication, left untouched", move.Target)
	}
	if err := os.Remove(move.Target); err != nil {
		return fmt.Errorf("could not withdraw the publication at %s: %w", move.Target, err)
	}
	return nil
}

// transcriptLive reports whether the chat, or any session whose id is part of
// the transcript's path (a sidechain's owning session), is running.
func transcriptLive(live map[string]struct{}, id, source string) bool {
	if _, running := live[strings.ToLower(id)]; running {
		return true
	}
	for _, session := range uuidPattern.FindAllString(source, -1) {
		if _, running := live[strings.ToLower(session)]; running {
			return true
		}
	}
	return false
}

func (runner *Runner) liveSessions() (map[string]struct{}, error) {
	return LiveSessions(
		runner.proc,
		firstEngineRoot(runner.paths.Roots[pfmengine.Codex]),
		runner.paths.SIDDir,
		runner.codexBinary,
	)
}

// changedSince reports a transcript that is no longer the file the plan saw:
// another inode, another size or another modification time.
func changedSince(planned, current fs.FileInfo) bool {
	if planned == nil {
		return false
	}
	return !os.SameFile(planned, current) ||
		planned.Size() != current.Size() ||
		!planned.ModTime().Equal(current.ModTime())
}

// fileIdentity is the device and inode a manifest row records.
func fileIdentity(info fs.FileInfo) (string, error) {
	id, err := gather.FileIDOfInfo(info)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", id.Device, id.Inode), nil
}

// sameTranscript reports whether two names hold one transcript: one inode, or
// byte-identical content.
func sameTranscript(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", left, err)
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", right, err)
	}
	if os.SameFile(leftInfo, rightInfo) {
		return true, nil
	}
	if leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	leftContent, err := os.ReadFile(left)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", left, err)
	}
	rightContent, err := os.ReadFile(right)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", right, err)
	}
	return bytes.Equal(leftContent, rightContent), nil
}

func syncPublishedArchive(target string) error {
	file, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("open archive publication %s: %w", target, err)
	}
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync archive publication %s: %w", target, err)
	}
	return syncManifestDirectoryParents(target)
}

// moveFile is Restore's move: it publishes exclusively, so it never
// overwrites, falls back to a complete exclusive copy across filesystems (an
// archived file has no running writer), and removes the source only once the
// publication is durable.
func moveFile(source, target string) error {
	if err := linkArchiveFile(source, target); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("publish %s without overwriting %s: %w", source, target, err)
		}
		content, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("read %s: %w", source, err)
		}
		if beforeArchiveCopyWrite != nil {
			beforeArchiveCopyWrite()
		}
		if err := atomicfile.Create(target, content, 0o600); err != nil {
			return fmt.Errorf("publish complete archive copy %s: %w", target, err)
		}
	}
	if err := syncArchivePublication(target); err != nil {
		return fmt.Errorf("make archive publication durable before removing %s: %w", source, err)
	}
	if err := removeArchiveSource(source); err != nil {
		return fmt.Errorf("remove %s after publishing %s: %w", source, target, err)
	}
	return nil
}
