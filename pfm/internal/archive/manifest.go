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
const manifestHeader = "uuid\tengine\torig_path\tbytes\tarchived_path\tarchived_at"

// ManifestRow is one archived file's record.
type ManifestRow struct {
	ID         string
	Engine     string
	Original   string
	Bytes      int64
	Archived   string
	ArchivedAt string
}

// ManifestPath is where the record lives.
func ManifestPath(archiveDir string) string {
	return filepath.Join(archiveDir, "MANIFEST.tsv")
}

// AppendManifest records one completed move.
func AppendManifest(archiveDir string, move Move, stamp string) (returnErr error) {
	path := ManifestPath(archiveDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create the archive directory: %w", err)
	}
	_, err := os.Stat(path)
	fresh := errors.Is(err, fs.ErrNotExist)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
	}
	if _, err := fmt.Fprintf(
		file,
		"%s\t%s\t%s\t%d\t%s\t%s\n",
		move.ID,
		move.Engine,
		move.Source,
		move.Bytes,
		move.Target,
		stamp,
	); err != nil {
		return fmt.Errorf("write the manifest row: %w", err)
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
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "uuid\t") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		bytes, _ := strconv.ParseInt(fields[3], 10, 64)
		rows = append(rows, ManifestRow{
			ID:         fields[0],
			Engine:     fields[1],
			Original:   fields[2],
			Bytes:      bytes,
			Archived:   fields[4],
			ArchivedAt: fields[5],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read the manifest: %w", err)
	}
	return rows, nil
}

// Restore puts one archived chat back exactly where it was.
//
// It refuses rather than guesses: an id the manifest never recorded, an
// archived file that is no longer there, or an original path that something
// else now occupies all stop the restore with the reason said out loud.
func Restore(archiveDir, id string) (ManifestRow, error) {
	rows, err := ReadManifest(archiveDir)
	if err != nil {
		return ManifestRow{}, err
	}
	var match ManifestRow
	found := false
	for _, row := range rows {
		if strings.EqualFold(row.ID, id) {
			// The LAST row wins: a chat archived, restored and archived again
			// has several, and the newest is the one on disk.
			match = row
			found = true
		}
	}
	if !found {
		return ManifestRow{}, fmt.Errorf("%s is not in the archive manifest", id)
	}
	if _, err := os.Stat(match.Archived); err != nil {
		return ManifestRow{}, fmt.Errorf(
			"the archived file for %s is missing: %w",
			id,
			err,
		)
	}
	if _, err := os.Stat(match.Original); err == nil {
		return ManifestRow{}, fmt.Errorf(
			"%s already exists — restoring would overwrite it",
			match.Original,
		)
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
