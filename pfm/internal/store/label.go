package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/naming"
)

// Label is the chat's pfm label: the one naming-precedence result every
// surface that names a Claude chat reads.
func (transcript Transcript) Label() string {
	return naming.DisplayName(transcript.CustomTitle, transcript.AITitle, transcript.FirstPrompt)
}

// SessionLabel opens the fleet database and returns the label of one Claude
// session. A session the index has not seen has no label: "" with a nil error.
func SessionLabel(ctx context.Context, id string, options ...OpenOption) (label string, returnErr error) {
	database, err := Open(options...)
	if err != nil {
		return "", fmt.Errorf("open fleet database for the label of session %q: %w", id, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close fleet database after the label of session %q: %w", id, err),
			)
		}
	}()
	transcript, found, err := database.Transcript(ctx, id)
	if err != nil {
		return "", fmt.Errorf("read the label of session %q: %w", id, err)
	}
	if !found {
		return "", nil
	}
	return transcript.Label(), nil
}
