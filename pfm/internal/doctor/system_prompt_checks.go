package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printSystemPromptDoctor prints each configured Claude account's effective
// claude.systemPrompt mode (claudelaunch.PromptMode): a warning when the mode
// is production while this host's composed fleet prompt exists, since the
// prompt is built but no launch carries it, and when the mode is professor
// while the prompt is not there, since every launch then warns it is missing.
// The shipped default on a host with no Professor clone recorded is its own
// ok row: the fleet prompt needs a clone. A prompt it cannot look for is its
// own warning, never "not built". It returns the warnings it printed.
func printSystemPromptDoctor(stdout io.Writer, home string, machine config.Config) int {
	accounts := machine.Accounts
	if len(accounts) == 0 {
		accounts = []config.Account{{ID: 0}}
	}
	warnings := 0
	for _, account := range accounts {
		prefs := machine.EffectiveClaude(account.ID)
		mode := claudelaunch.PromptMode(home, prefs)
		row := fmt.Sprintf("doctor: system-prompt claude[%d]", account.ID)
		if prefs.SystemPromptDefault && mode != prefs.SystemPrompt {
			fmt.Fprintf(stdout, "%s mode=production ok (default professor needs a Professor clone; none recorded, "+
				"so launches carry no fleet prompt)\n", row)
			continue
		}
		if mode != config.SystemPromptProduction && mode != config.SystemPromptProfessor {
			fmt.Fprintf(stdout, "%s mode=%s ok\n", row, mode)
			continue
		}
		prompt, err := claudelaunch.PromptFile(home)
		if err == nil {
			_, err = os.Stat(prompt)
		}
		noClone := errors.Is(err, paths.ErrNoSourceRepoMarker)
		absent := noClone || errors.Is(err, fs.ErrNotExist)
		switch {
		case mode == config.SystemPromptProfessor && err == nil:
			fmt.Fprintf(stdout, "%s mode=professor ok\n", row)
		case mode == config.SystemPromptProfessor && noClone:
			warnings++
			fmt.Fprintf(stdout, "%s WARN mode=professor: no Professor clone recorded, so no fleet prompt exists and "+
				"every launch says so — fix: install from a Professor clone, or set claude.systemPrompt to production\n",
				row)
		case mode == config.SystemPromptProfessor && absent:
			warnings++
			fmt.Fprintf(stdout, "%s WARN mode=professor: fleet prompt %s not built, so every launch says so — fix: "+
				"run pfm install --yes from the Professor clone\n", row, prompt)
		case absent:
			fmt.Fprintf(stdout, "%s mode=production ok (no fleet prompt built)\n", row)
		case err != nil:
			warnings++
			fmt.Fprintf(stdout, "%s WARN mode=%s: could not look for the fleet prompt: %v\n", row, mode, err)
		default:
			warnings++
			fmt.Fprintf(
				stdout,
				"%s WARN mode=production: fleet prompt built but not injected — "+
					"set claude.systemPrompt to professor (prompt %s)\n",
				row,
				prompt,
			)
		}
	}
	return warnings
}
