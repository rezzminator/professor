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

//go:embed opencode_engine_guard_plugin.js
var openCodeEngineGuardPlugin string

// herdrPluginBinaryToken is the placeholder an embedded plugin holds where
// the staged copy carries the pfm binary's absolute path.
const herdrPluginBinaryToken = `"__PFM_BINARY__"`

// StageOpenCodeHerdrPlugin atomically replaces an absent or stale Herdr
// reporter plugin with the embedded one, carrying pfmBinary — the absolute
// path the plugin spawns for every report, never a bare PATH name.
func StageOpenCodeHerdrPlugin(path, pfmBinary string) error {
	return stageOpenCodePlugin("OpenCode Herdr plugin", openCodeHerdrPlugin, path, pfmBinary)
}

// StageOpenCodeEngineGuardPlugin atomically replaces an absent or stale
// engine-guard plugin with the embedded one, carrying pfmBinary, the guard
// it runs before every OpenCode write tool.
func StageOpenCodeEngineGuardPlugin(path, pfmBinary string) error {
	return stageOpenCodePlugin("OpenCode engine-guard plugin", openCodeEngineGuardPlugin, path, pfmBinary)
}

// OpenCodeEngineGuardPluginBody is the engine-guard plugin as staged with
// pfmBinary, for a caller judging whether a staged copy is current.
func OpenCodeEngineGuardPluginBody(pfmBinary string) (string, error) {
	return openCodePluginBody("OpenCode engine-guard plugin", openCodeEngineGuardPlugin, pfmBinary)
}

func stageOpenCodePlugin(label, template, path, pfmBinary string) error {
	body, err := openCodePluginBody(label, template, pfmBinary)
	if err != nil {
		obs.Logger(context.Background()).Error(label, "path", path, obs.FieldErr, err)
		return err
	}
	current, err := os.ReadFile(path)
	if err == nil && string(current) == body {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		wrapped := fmt.Errorf("read %s %s: %w", label, path, err)
		obs.Logger(context.Background()).Error(label, "path", path, obs.FieldErr, wrapped)
		return wrapped
	}
	if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
		wrapped := fmt.Errorf("write %s %s: %w", label, path, err)
		obs.Logger(context.Background()).Error(label, "path", path, obs.FieldErr, wrapped)
		return wrapped
	}
	return nil
}

func openCodePluginBody(label, template, pfmBinary string) (string, error) {
	if !strings.HasPrefix(pfmBinary, "/") {
		return "", fmt.Errorf("%s needs an absolute pfm binary path, got %q", label, pfmBinary)
	}
	encoded, err := json.Marshal(pfmBinary)
	if err != nil {
		return "", fmt.Errorf("encode pfm binary path %q: %w", pfmBinary, err)
	}
	if strings.Count(template, herdrPluginBinaryToken) != 1 {
		return "", fmt.Errorf("embedded %s lacks its %s placeholder", label, herdrPluginBinaryToken)
	}
	return strings.Replace(template, herdrPluginBinaryToken, string(encoded), 1), nil
}
