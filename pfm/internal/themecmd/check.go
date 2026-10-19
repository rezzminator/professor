package themecmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// profileCheck is one profile file as `pfm theme check` reports it: the VS
// Code theme install would render from it, or the error install would refuse
// it with.
type profileCheck struct {
	Path                string `json:"path"`
	OK                  bool   `json:"ok"`
	ID                  string `json:"id,omitempty"`
	Label               string `json:"label,omitempty"`
	UITheme             string `json:"uiTheme,omitempty"`
	Colors              int    `json:"colors,omitempty"`
	TokenColors         int    `json:"tokenColors,omitempty"`
	SemanticTokenColors int    `json:"semanticTokenColors,omitempty"`
	Error               string `json:"error,omitempty"`
}

// checkOutcome is `pfm theme check --json`.
type checkOutcome struct {
	Profiles []profileCheck `json:"profiles"`
}

// check is `pfm theme check [file…]`: every check install runs on a profile
// (installer.CheckProfile), run on the files named or, with none, on every
// profile the recorded source clone ships, writing nothing. Exit 0 only when
// every profile passes; a profile list that could not be read is exit 1,
// never an empty pass.
func (run runner) check(args []string) int {
	cmd, code, ok := run.parse(verbCheck, args)
	if !ok {
		return code
	}
	files := cmd.positional
	if len(files) == 0 {
		var err error
		if files, err = run.sourceProfiles(); err != nil {
			return run.fail(err)
		}
	}
	outcome := checkOutcome{Profiles: make([]profileCheck, 0, len(files))}
	code = 0
	for _, file := range files {
		result := profileCheck{Path: file}
		if absolute, err := filepath.Abs(file); err == nil {
			result.Path = absolute
		}
		rendered, err := installer.CheckProfile(result.Path)
		if err == nil {
			err = countThemeRules(rendered.Content, &result)
		}
		if err != nil {
			result.Error = err.Error()
			code = 1
		} else {
			result.OK = true
			result.ID, result.Label, result.UITheme = rendered.ID, rendered.Label, rendered.UITheme
		}
		outcome.Profiles = append(outcome.Profiles, result)
	}
	if cmd.asJSON {
		if !run.printJSON(outcome) {
			return 1
		}
		return code
	}
	drawChecks(run, outcome.Profiles)
	return code
}

// sourceProfiles are the profiles of the source clone pfm install recorded;
// a missing marker or an empty templates/themes/ is an error naming where it
// looked.
func (run runner) sourceProfiles() ([]string, error) {
	env, err := run.deps.Env()
	if err != nil {
		return nil, err
	}
	repo, err := paths.ReadSourceRepoMarker(env.Home)
	if err != nil {
		return nil, fmt.Errorf("check: no profile named and no source clone to scan (%w); name the profile files", err)
	}
	files, err := installer.SourceProfiles(repo)
	if err != nil {
		return nil, fmt.Errorf("check: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("check: no profile in %s", filepath.Join(repo, "templates", "themes"))
	}
	return files, nil
}

// countThemeRules reads the rendered theme's size into result: how many
// workbench colours, TextMate rules and semantic rules it sets.
func countThemeRules(content []byte, result *profileCheck) error {
	var document struct {
		Colors              map[string]json.RawMessage `json:"colors"`
		TokenColors         []json.RawMessage          `json:"tokenColors"`
		SemanticTokenColors map[string]json.RawMessage `json:"semanticTokenColors"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return errors.Join(errors.New("rendered VS Code theme does not read back"), err)
	}
	result.Colors, result.TokenColors = len(document.Colors), len(document.TokenColors)
	result.SemanticTokenColors = len(document.SemanticTokenColors)
	return nil
}

// drawChecks prints one row per profile — `ok` with the VS Code theme it
// renders, or `FAIL` with install's refusal — then the tally.
func drawChecks(run runner, checks []profileCheck) {
	table := tabwriter.NewWriter(run.stdout, 0, 0, 2, ' ', 0)
	passed := 0
	for _, result := range checks {
		if !result.OK {
			continue
		}
		passed++
		fmt.Fprintf(table, "ok\t%s\t%s\t%d colours · %d token rules · %d semantic\t%s\n",
			result.Label, result.UITheme, result.Colors, result.TokenColors, result.SemanticTokenColors, result.Path)
	}
	if err := table.Flush(); err != nil {
		fmt.Fprintf(run.stderr, "pfm theme check: write the table: %v\n", err)
	}
	for _, result := range checks {
		if !result.OK {
			fmt.Fprintf(run.stdout, "FAIL %s\n", pyvalue.OneLine(result.Error))
		}
	}
	noun := "profiles"
	if len(checks) == 1 {
		noun = "profile"
	}
	fmt.Fprintf(run.stdout, "%d of %d %s pass\n", passed, len(checks), noun)
}
