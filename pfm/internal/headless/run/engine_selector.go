package run

import (
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// ApplyEngineSelector resolves the headless CLI's `--engine` selector onto
// request: an empty selector takes the configured default engine, and every
// other value parses through the engine registry, so an unknown name is the
// registry's own error rather than a second spelling of it here.
//
// `codex` and `cx` remain accepted selectors, but both run through OpenCode's
// `run` protocol: pfm supplies OpenCode the system prompt, schema, tool and
// MCP controls its CLI has no flags for. The selector's own ask.codex model
// and effort preferences are resolved BEFORE the engine id is replaced, so an
// operator who configured `--engine codex` still gets the model they chose.
//
// It lives here, not in cmd/pfm, because the mapping is part of the headless
// contract: any surface that offers the same selector must map it identically
// or the two doors disagree about what `codex` means.
func ApplyEngineSelector(request *Request, selector string) error {
	var selected pfmengine.ID
	var err error
	if strings.TrimSpace(selector) == "" {
		selected, err = request.Config.DefaultEngine()
	} else {
		selected, err = pfmengine.Parse(selector)
	}
	if err != nil {
		return err
	}
	request.EngineSelector = strings.TrimSpace(selector)
	request.Engine = selected
	if selected != pfmengine.Codex {
		return nil
	}
	prefs := request.Config.Ask.PrefsFor(pfmengine.Codex)
	if request.Model == "" {
		request.Model = prefs.Model
	}
	if request.Effort == "" {
		request.Effort = prefs.Effort
	}
	request.Engine = pfmengine.OpenCode
	return nil
}
