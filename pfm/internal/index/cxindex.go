package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func reloadCxNamesFromRoots(
	ctx context.Context,
	database *store.Store,
	codexHomes []string,
	counters *Counters,
) error {
	type source struct {
		path string
		size int64
	}
	sources := make([]source, 0, len(codexHomes))
	signature := sha256.New()
	for _, codexHome := range codexHomes {
		path := filepath.Join(codexHome, codexmeta.SessionIndexFile)
		size, mtimeNS := int64(-1), int64(-1)
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
			mtimeNS = info.ModTime().UnixNano()
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat Codex session index %q: %w", path, err)
		}
		fmt.Fprintf(signature, "%s\x00%d\x00%d\x00", filepath.Clean(codexHome), size, mtimeNS)
		sources = append(sources, source{path: path, size: size})
	}
	signatureText := hex.EncodeToString(signature.Sum(nil))
	if previous, found, err := database.Meta(ctx, "cx_index_roots_signature"); err != nil {
		return err
	} else if found && previous == signatureText {
		return nil
	}

	names := make(map[string]store.CxName)
	for _, source := range sources {
		if source.size < 0 {
			continue
		}
		_, bytesRead, err := readCompleteLines(source.path, 0, func(line []byte) {
			if entry, err := codexmeta.DecodeSessionIndexLine(line); err == nil {
				names[entry.ID] = store.CxName{
					ID: entry.ID, ThreadName: entry.ThreadName,
					Source:    store.CxNameSourceSessionIndex,
					RenamedAt: cxRenameTime(entry),
				}
			}
		})
		if err != nil {
			return fmt.Errorf("parse Codex session index %q: %w", source.path, err)
		}
		counters.BytesRead += bytesRead
	}

	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := database.WithImmediateTx(ctx, func(tx *store.ImmediateTx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM cx_names"); err != nil {
			return fmt.Errorf("clear Codex names: %w", err)
		}
		for _, id := range ids {
			if err := tx.UpsertCxName(ctx, names[id]); err != nil {
				return err
			}
		}
		return tx.SetMeta(ctx, "cx_index_roots_signature", signatureText)
	}); err != nil {
		return fmt.Errorf("replace Codex names from roster: %w", err)
	}
	counters.CxNamesReloaded = true
	counters.RowsTouched += len(ids) + 1
	return nil
}

// cxRenameTime reads a session_index.jsonl entry's updated_at. An empty
// or unparsable value means no rename time is known for this entry, not that
// the rename happened at the Unix epoch — reconcileCodexNames treats the two
// cases identically (RenamedAt of 0), so returning 0 here is the correct
// "unknown" sentinel, not a wrong guess.
func cxRenameTime(entry codexmeta.SessionIndexEntry) int64 {
	renamed, ok := entry.RenamedAt()
	if !ok {
		return 0
	}
	return renamed.UnixNano()
}

func reloadCxNames(
	ctx context.Context,
	database *store.Store,
	codexHome string,
	counters *Counters,
) error {
	path := filepath.Join(codexHome, codexmeta.SessionIndexFile)
	size, mtimeNS := int64(-1), int64(-1)
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
		mtimeNS = info.ModTime().UnixNano()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat Codex session index: %w", err)
	}

	oldSize, sizeFound, err := database.Meta(ctx, "cx_index_size")
	if err != nil {
		return err
	}
	oldMTime, mtimeFound, err := database.Meta(ctx, "cx_index_mtime_ns")
	if err != nil {
		return err
	}
	sizeText := strconv.FormatInt(size, 10)
	mtimeText := strconv.FormatInt(mtimeNS, 10)
	if sizeFound && mtimeFound && oldSize == sizeText && oldMTime == mtimeText {
		return nil
	}

	names := make(map[string]store.CxName)
	if size >= 0 {
		_, bytesRead, err := readCompleteLines(path, 0, func(line []byte) {
			if entry, err := codexmeta.DecodeSessionIndexLine(line); err == nil {
				// The file is append-only, so the LAST entry for an id — the
				// one this overwrite leaves standing — is the freshest rename
				// intent, whether or not it carries a timestamp.
				names[entry.ID] = store.CxName{
					ID:         entry.ID,
					ThreadName: entry.ThreadName,
					Source:     store.CxNameSourceSessionIndex,
					RenamedAt:  cxRenameTime(entry),
				}
			}
		})
		if err != nil {
			return fmt.Errorf("parse Codex session index: %w", err)
		}
		counters.BytesRead += bytesRead
	}

	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := database.WithImmediateTx(ctx, func(tx *store.ImmediateTx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM cx_names"); err != nil {
			return fmt.Errorf("clear Codex names: %w", err)
		}
		for _, id := range ids {
			if err := tx.UpsertCxName(ctx, names[id]); err != nil {
				return err
			}
		}
		if err := tx.SetMeta(ctx, "cx_index_size", sizeText); err != nil {
			return err
		}
		return tx.SetMeta(ctx, "cx_index_mtime_ns", mtimeText)
	}); err != nil {
		return fmt.Errorf("replace Codex names: %w", err)
	}

	counters.CxNamesReloaded = true
	counters.RowsTouched += len(ids) + 2
	return nil
}
