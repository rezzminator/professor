package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// unmarshalKeepingNumbers is json.Unmarshal for a user-owned file the installer
// rewrites: numbers decode as json.Number, so an integer beyond float64's 2^53
// (a counter, an id) is written back exactly as the user had it. Input
// json.Unmarshal refuses — empty, truncated, or trailing a second value — is
// refused here too.
func unmarshalKeepingNumbers(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("invalid JSON: data after the top-level value")
		}
		return err
	}
	return nil
}
