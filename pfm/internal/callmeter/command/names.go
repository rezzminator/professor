package command

import (
	"context"
	"fmt"
	"io"

	pfmcli "github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// chatIndexNames resolves a session id to its chat name through pfm's
// transcript index, opened read-only on first use: a report never migrates the
// index or opens the shared fleet store. An absent index names nothing; an
// index that cannot be opened returns its error on every call, so the table
// prints "?" and one note.
type chatIndexNames struct {
	ctx    context.Context
	path   string
	opened bool
	index  *store.TranscriptNames
	err    error
}

func (names *chatIndexNames) nameOf(sessionID string) (string, error) {
	if !names.opened {
		names.opened = true
		index, _, err := store.OpenTranscriptNames(names.ctx, names.path)
		if err != nil {
			names.err = fmt.Errorf("read chat names from %s: %w", names.path, err)
		}
		names.index = index
	}
	if names.err != nil || names.index == nil {
		return "", names.err
	}
	return names.index.DisplayName(names.ctx, sessionID)
}

func (names *chatIndexNames) close(stderr io.Writer, exitCode *int) {
	if names.index != nil {
		pfmcli.CloseResource(names.index, "callmeter: close chat index", stderr, exitCode)
	}
}
