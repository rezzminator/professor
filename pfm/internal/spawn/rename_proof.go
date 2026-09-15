package spawn

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"hostops/pfm/internal/codexmeta"
)

// renameProof reports whether Codex itself recorded a rename of a thread to
// name at or after since. An error means the record could not be read —
// "could not verify", never "not renamed".
type renameProof func(name string, since time.Time) (bool, error)

// codexHomes is where every Codex rename in this process looks for its proof.
// main sets it once, from the machine config it loaded; nil (never set) leaves
// the screen as the only witness.
var codexHomes atomic.Pointer[[]string]

// UseCodexHomes names the Codex homes whose ledgers prove a rename. It is the
// one place the machine config reaches the rename: every door that names a
// Codex chat — chat new, chat branch, the post-/clear re-apply, a dream seat —
// goes through RenameCodex, and RenameCodex reads the homes set here.
func UseCodexHomes(homes []string) {
	owned := append([]string(nil), homes...)
	codexHomes.Store(&owned)
}

// codexRenameProof is the proof for this process, or nil when no homes were set.
func codexRenameProof() renameProof {
	homes := codexHomes.Load()
	if homes == nil {
		return nil
	}
	return codexIndexProof(*homes)
}

// codexIndexProof proves a Codex rename from Codex's own ledger: the
// session_index.jsonl entry thread/name/set appends in every Codex home. It is
// the witness that survives a TUI change — Codex 0.154 renames a thread
// without printing a word about it, and a status line shows the name only when
// the user's own config asks it to.
//
// An entry counts only when it carries the name AND a rename time at or after
// since, so an older rename to the same name is never taken for this one.
func codexIndexProof(codexRoots []string) renameProof {
	roots := append([]string(nil), codexRoots...)
	return func(name string, since time.Time) (bool, error) {
		if len(roots) == 0 {
			return false, errors.New("no Codex home is configured to read the rename from")
		}
		for _, root := range roots {
			landed, err := sessionIndexHasRename(filepath.Join(root, codexmeta.SessionIndexFile), name, since)
			if err != nil || landed {
				return landed, err
			}
		}
		return false, nil
	}
}

func sessionIndexHasRename(path, name string, since time.Time) (renamed bool, returnErr error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A home Codex has never renamed a thread in: nothing recorded yet.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open Codex session index %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Codex session index %s: %w", path, err))
		}
	}()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		// A line that does not decode is skipped, not fatal: Codex appends
		// while this reads, so the last line can be torn mid-write.
		entry, err := codexmeta.DecodeSessionIndexLine(scanner.Bytes())
		if err != nil || entry.ThreadName != name {
			continue
		}
		if renamed, ok := entry.RenamedAt(); ok && !renamed.Before(since) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read Codex session index %s: %w", path, err)
	}
	return false, nil
}
