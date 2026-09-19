package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

// hasValidCodexCredentials reports whether home/auth.json is the real Codex
// CLI shape: access_token and account_id both live INSIDE tokens. An absent
// file is the ordinary "no account here" case (ok=false, err=nil). Any other
// read failure (permission denied, etc.) is NOT folded into that silence —
// it comes back as a non-nil error the caller must surface, never swallow.
func hasValidCodexCredentials(home string) (bool, error) {
	body, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var marker struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	valid := json.Unmarshal(body, &marker) == nil &&
		strings.TrimSpace(marker.Tokens.AccessToken) != "" &&
		strings.TrimSpace(marker.Tokens.AccountID) != ""
	return valid, nil
}

// openCodeStoreExists recognizes both a materialized session database and an
// authenticate-only OpenCode data home. The latter is the state produced by a
// subscription login before the first headless session has been written. An
// absent home is the ordinary "no account here" case (false, nil); a stat that
// FAILED is an error the caller surfaces, never folded into that silence.
func openCodeStoreExists(home string) (bool, error) {
	for _, name := range []string{"opencode.db", "auth.json"} {
		path := filepath.Join(home, name)
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return true, nil
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	return false, nil
}

// openCodeAccountHome is the OpenCode data home account discovery reads: the
// engine descriptor's default root under home, relocated by the descriptor's
// own PFM_* jail variable. The env read goes through internal/paths — the one
// place a PFM_* override is resolved — so a jailed test never reaches the
// operator's real ~/.local/share/opencode.
func openCodeAccountHome(home string) string {
	descriptor := pfmengine.MustLookup(pfmengine.OpenCode)
	root := descriptor.DefaultRoots(home)[0]
	if roots := strings.TrimSpace(paths.EnvOr(descriptor.RootEnv, "")); roots != "" {
		if first := filepath.SplitList(roots); len(first) != 0 && strings.TrimSpace(first[0]) != "" {
			root = filepath.Clean(first[0])
		}
	}
	return root
}
