package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// wireLogDefault writes the activity log's safe default visibly (spec
// § Control): when pfm.config.json exists and has no `log` key, it gains
// config.InstallLogBlock through a `change` row; a `log` key that exists —
// whatever it says — is never overwritten, so an installed choice survives
// every re-install and update. An absent file is a skip: `pfm config init`
// owns creation. A file that cannot be read or parsed fails the step — the
// planner (config.LogDefaultInsertion) never reads a damaged file as "nothing
// to do".
func (installer *engine) wireLogDefault() error {
	path := installer.options.MCPConfigPath
	if path == "" {
		installer.skip("log default: no pfm.config.json path known — nothing to write")
		return nil
	}
	content, changed, err := pfmconfig.LogDefaultInsertion(path)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("log default: " + path + " does not exist (pfm config init creates it)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("plan the log default: %w", err)
	}
	if !changed {
		installer.ok("log block present in " + path + " (kept)")
		return nil
	}
	block, err := json.Marshal(pfmconfig.InstallLogBlock())
	if err != nil {
		return fmt.Errorf("render the log default: %w", err)
	}
	return installer.change(
		fmt.Sprintf("write log default %s into %s", block, path),
		func() error { return atomicfile.Write(path, content, 0o600) },
	)
}
