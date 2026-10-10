package archive

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// manifestHeader is the manifest's first line. The file is a plain TSV on
// purpose: the one thing that must survive every future rewrite of this tool
// is the record of where a file came from.
const manifestHeader = "uuid\tengine\torig_path\tbytes\tarchived_path\tarchived_at\tstate\tfile_id"

// stateIntent marks a row written before its publication; a six-column row
// from before states recorded a relocation that had already completed.
const stateIntent = "intent"

// ManifestRow is one archived file's record.
type ManifestRow struct {
	ID         string
	Engine     string
	Original   string
	Bytes      int64
	Archived   string
	ArchivedAt string
	// State is stateIntent: written before the publication, FileID the
	// source's device:inode, which the hard-link publication keeps.
	State  string
	FileID string
}

// ManifestPath is where the record lives.
func ManifestPath(archiveDir string) string {
	return filepath.Join(archiveDir, "MANIFEST.tsv")
}

// AppendManifest durably records a relocation intent before the source moves.
func AppendManifest(archiveDir string, move Move, stamp string) error {
	return appendManifestRow(archiveDir, manifestRowFor(move, stamp))
}

func manifestRowFor(move Move, stamp string) ManifestRow {
	return ManifestRow{
		ID:         move.ID,
		Engine:     move.Engine,
		Original:   move.Source,
		Bytes:      move.Bytes,
		Archived:   move.Target,
		ArchivedAt: stamp,
		State:      stateIntent,
		FileID:     move.fileID,
	}
}

func appendManifestRow(archiveDir string, row ManifestRow) (returnErr error) {
	path := ManifestPath(archiveDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create the archive directory: %w", err)
	}
	_, err := os.Stat(path)
	fresh := errors.Is(err, fs.ErrNotExist)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open the manifest: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the manifest: %w", err))
		}
	}()
	if fresh {
		if _, err := fmt.Fprintln(file, manifestHeader); err != nil {
			return fmt.Errorf("write the manifest header: %w", err)
		}
	} else if err := terminateTornRow(file); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		file,
		"%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
		row.ID,
		row.Engine,
		row.Original,
		row.Bytes,
		row.Archived,
		row.ArchivedAt,
		row.State,
		row.FileID,
	); err != nil {
		return fmt.Errorf("write the manifest row: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync the manifest row: %w", err)
	}
	if err := syncManifestParents(path); err != nil {
		return err
	}
	return nil
}

// terminateTornRow ends a row an interrupted append left without its newline,
// so the next row starts on a line of its own instead of being glued onto it.
func terminateTornRow(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect the manifest: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return fmt.Errorf("read the manifest's last byte: %w", err)
	}
	if last[0] == '\n' {
		return nil
	}
	if _, err := file.WriteString("\n"); err != nil {
		return fmt.Errorf("terminate the manifest's torn row: %w", err)
	}
	return nil
}

// Directory sync is a failure boundary: a failed durability reading cannot
// authorize relocating the source. Tests inject failure, not a syscall verdict.
var syncManifestParents = syncManifestDirectoryParents

func syncManifestDirectoryParents(path string) error {
	// The intent is not durable until its newly created directory entries are too.
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		parent, err := os.Open(directory)
		if err != nil {
			return fmt.Errorf("open manifest directory %s: %w", directory, err)
		}
		syncErr := parent.Sync()
		closeErr := parent.Close()
		if err := errors.Join(syncErr, closeErr); err != nil {
			return fmt.Errorf("sync manifest directory %s: %w", directory, err)
		}
		if filepath.Dir(directory) == directory {
			break
		}
	}
	return nil
}

// ReadManifest returns every recorded move, oldest first.
func ReadManifest(archiveDir string) (rows []ManifestRow, returnErr error) {
	file, err := os.Open(ManifestPath(archiveDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open the manifest: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the manifest: %w", err))
		}
	}()
	rows = make([]ManifestRow, 0)
	// Six-column rows are legacy completed moves only before the manifest
	// records states; after that, a row short of its state and identity is one
	// an interrupted append tore, and it owns nothing.
	stateful := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if strings.HasPrefix(line, "uuid\t") {
			stateful = stateful || len(fields) >= 8
			continue
		}
		if len(fields) < 6 || (stateful && len(fields) < 8) {
			continue
		}
		bytes, _ := strconv.ParseInt(fields[3], 10, 64)
		row := ManifestRow{
			ID:         fields[0],
			Engine:     fields[1],
			Original:   fields[2],
			Bytes:      bytes,
			Archived:   fields[4],
			ArchivedAt: fields[5],
		}
		if len(fields) >= 8 {
			row.State, row.FileID = fields[6], fields[7]
			stateful = true
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the manifest: %w", err)
	}
	return rows, nil
}

// Restore puts one archived chat back exactly where it was.
//
// It refuses rather than guesses: an id the manifest never recorded, an
// archived file that is no longer there or is not the recorded publication,
// or an original path that something else now occupies all stop the restore
// with the reason said out loud. An original that still holds the very
// transcript published — a relocation whose source removal failed — is
// restored by removing only the archive name.
func Restore(archiveDir, id string) (ManifestRow, error) {
	match, owned, found, err := recordedArchive(archiveDir, id)
	if err != nil {
		return ManifestRow{}, err
	}
	if !found {
		return ManifestRow{}, fmt.Errorf("%s is not in the archive manifest", id)
	}
	if !owned {
		if _, err := os.Lstat(match.Archived); err == nil {
			return ManifestRow{}, fmt.Errorf(
				"the file at %s is not the publication recorded for %s; refusing to restore it",
				match.Archived,
				id,
			)
		}
		return ManifestRow{}, fmt.Errorf("the archived file for %s is missing at %s", id, match.Archived)
	}
	if _, err := os.Lstat(match.Original); err == nil {
		same, err := sameTranscript(match.Original, match.Archived)
		if err != nil {
			return ManifestRow{}, fmt.Errorf("compare %s with its archive: %w", match.Original, err)
		}
		if !same {
			return ManifestRow{}, fmt.Errorf(
				"%s already exists — restoring would overwrite it",
				match.Original,
			)
		}
		if err := os.Remove(match.Archived); err != nil {
			return ManifestRow{}, fmt.Errorf("remove the unfinished archive copy %s: %w", match.Archived, err)
		}
		return match, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ManifestRow{}, fmt.Errorf("inspect %s before restoring: %w", match.Original, err)
	}
	if err := os.MkdirAll(filepath.Dir(match.Original), 0o700); err != nil {
		return ManifestRow{}, fmt.Errorf(
			"recreate %s: %w",
			filepath.Dir(match.Original),
			err,
		)
	}
	if err := moveFile(match.Archived, match.Original); err != nil {
		return ManifestRow{}, fmt.Errorf("restore %s: %w", id, err)
	}
	return match, nil
}

// recordedArchive selects, for both restore and cleanup replay, the newest row
// that owns the file at its archived name. owned is false when no row does;
// the newest row is then returned for the caller's message.
func recordedArchive(archiveDir, id string) (row ManifestRow, owned, found bool, err error) {
	rows, err := ReadManifest(archiveDir)
	if err != nil {
		return ManifestRow{}, false, false, err
	}
	var newest ManifestRow
	for index := len(rows) - 1; index >= 0; index-- {
		candidate := rows[index]
		if !strings.EqualFold(candidate.ID, id) {
			continue
		}
		if !found {
			newest, found = candidate, true
		}
		owns, err := ownsPublication(candidate)
		if err != nil {
			return ManifestRow{}, false, false, err
		}
		if owns {
			return candidate, true, true, nil
		}
	}
	return newest, false, found, nil
}

// ownsPublication proves a row's claim on the file at its archived name by
// identity: an intent that merely names a target another transaction
// published owns nothing.
func ownsPublication(row ManifestRow) (bool, error) {
	info, err := os.Lstat(row.Archived)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect recorded archive %s: %w", row.Archived, err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	if row.State == "" {
		return true, nil
	}
	if row.FileID == "" {
		return false, nil
	}
	id, err := fileIdentity(info)
	if err != nil {
		return false, err
	}
	return id == row.FileID, nil
}
