package workbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

//go:embed opencode_herdr_plugin.mjs
var openCodeHerdrPlugin string

// herdrPluginBinaryToken is the placeholder the embedded plugin holds where
// the staged copy carries the pfm binary's absolute path.
const herdrPluginBinaryToken = `"__PFM_BINARY__"`

// StageOpenCodeHerdrPlugin atomically replaces an absent or stale Herdr
// reporter plugin with the embedded one, carrying pfmBinary — the absolute
// path the plugin spawns for every report, never a bare PATH name.
func StageOpenCodeHerdrPlugin(path, pfmBinary string) error {
	body, err := herdrPluginBody(pfmBinary)
	if err != nil {
		obs.Logger(context.Background()).Error("OpenCode Herdr plugin", "path", path, obs.FieldErr, err)
		return err
	}
	current, err := os.ReadFile(path)
	if err == nil && string(current) == body {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		wrapped := fmt.Errorf("read OpenCode Herdr plugin %s: %w", path, err)
		obs.Logger(context.Background()).Error("OpenCode Herdr plugin", "path", path, obs.FieldErr, wrapped)
		return wrapped
	}
	if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
		wrapped := fmt.Errorf("write OpenCode Herdr plugin %s: %w", path, err)
		obs.Logger(context.Background()).Error("OpenCode Herdr plugin", "path", path, obs.FieldErr, wrapped)
		return wrapped
	}
	return nil
}

func herdrPluginBody(pfmBinary string) (string, error) {
	if !strings.HasPrefix(pfmBinary, "/") {
		return "", fmt.Errorf("OpenCode Herdr plugin needs an absolute pfm binary path, got %q", pfmBinary)
	}
	encoded, err := json.Marshal(pfmBinary)
	if err != nil {
		return "", fmt.Errorf("encode pfm binary path %q: %w", pfmBinary, err)
	}
	if strings.Count(openCodeHerdrPlugin, herdrPluginBinaryToken) != 1 {
		return "", fmt.Errorf("embedded OpenCode Herdr plugin lacks its %s placeholder", herdrPluginBinaryToken)
	}
	return strings.Replace(openCodeHerdrPlugin, herdrPluginBinaryToken, string(encoded), 1), nil
}
