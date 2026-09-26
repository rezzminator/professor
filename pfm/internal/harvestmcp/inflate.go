package harvestmcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

// Inflate opens one compressed document the Go side carries no decoder for
// (harvest.Inflater): the body goes to a private scratch file, the sidecar
// inflates it beside it, and the scratch is removed.
func (converter pythonConverter) Inflate(
	ctx context.Context,
	codec string,
	body []byte,
	limit int64,
) (inner []byte, returnErr error) {
	directory, err := os.MkdirTemp("", "pfm-harvest-inflate-")
	if err != nil {
		return nil, fmt.Errorf("create inflate scratch: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove inflate scratch: %w", cleanupErr))
		}
	}()
	input, output := filepath.Join(directory, "input."+codec), filepath.Join(directory, "inner")
	if err := os.WriteFile(input, body, 0o600); err != nil {
		return nil, fmt.Errorf("write inflate scratch: %w", err)
	}
	if err := converter.worker.Inflate(ctx, codec, input, output, limit); err != nil {
		if errors.Is(err, harvestpy.ErrInflateCap) {
			return nil, fmt.Errorf("%w (%v)", harvest.ErrDecompressionBomb, err)
		}
		return nil, err
	}
	return os.ReadFile(output)
}

var _ harvest.Inflater = pythonConverter{}
