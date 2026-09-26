package harvestpy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInflateCap reports a compressed document that inflates past the caller's
// limit (a decompression bomb): named by the sidecar, never written whole.
var ErrInflateCap = errors.New("the compressed document inflates past the cap")

type inflateRequest struct {
	Op    string `json:"op"`
	Codec string `json:"codec"`
	Path  string `json:"path"`
	Out   string `json:"out"`
	Limit int64  `json:"limit"`
}

// Inflate decompresses the codec file at path into out, at most limit bytes,
// with the sidecar's stdlib decoder (xz: lzma) — the Go module carries no xz
// decoder. A body past limit answers ErrInflateCap.
func (converter *Converter) Inflate(ctx context.Context, codec, path, out string, limit int64) error {
	body, err := json.Marshal(inflateRequest{Op: "inflate", Codec: codec, Path: path, Out: out, Limit: limit})
	if err != nil {
		return fmt.Errorf("marshal harvestpy inflate request: %w", err)
	}
	line, stderr, err := converter.request(ctx, body)
	if err != nil {
		return err
	}
	var response struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		ErrorClass string `json:"error_class"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("decode harvestpy inflate response JSON: %w (stderr: %s)", err, stderr)
	}
	switch {
	case response.OK:
		return nil
	case response.ErrorClass == "DecompressionBomb":
		return fmt.Errorf("%w: %s", ErrInflateCap, response.Error)
	}
	return converterFailure(response.ErrorClass, response.Error, stderr)
}
