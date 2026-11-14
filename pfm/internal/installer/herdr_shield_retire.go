package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// This door retires hosts that ran herdr-shield: an older pfm left each Claude
// store's Herdr hook guarded with retiredShieldGuard, and the owner may still
// run Herdr with herdr-shield as its default_shell. Nothing else in pfm names it.
const (
	retiredShieldProgram = "herdr-shield"
	retiredShieldGuard   = `[ -n "${HERDR_SHIELD_INNER:-}" ] || `
)

// HerdrShieldRetirement is the door's verdict on one Claude store whose single
// Herdr hook is herdr-shield's guarded form (Guarded): Skip, when Herdr's
// default_shell still names herdr-shield, is the line naming the owner's steps
// and the store stays as it is; else the hook unwraps to Herdr's plain form.
// Err is a Herdr config that could not be read: neither verdict holds.
type HerdrShieldRetirement struct {
	Guarded bool
	Skip    string
	Err     error
}

// RetireHerdrShieldHook judges store against the Herdr config home and env
// name. Unwrapping while a shielded pane lives would let Herdr's own hook claim
// it; keeping the guard while install refreshes the store would make Herdr add
// a second, unguarded hook, so only a store whose one hook is guarded is judged.
func RetireHerdrShieldHook(store herdr.ClaudeStore, home string, env paths.Env) HerdrShieldRetirement {
	if len(store.Hooks) != 1 || !strings.HasPrefix(store.Hooks[0], retiredShieldGuard) {
		return HerdrShieldRetirement{}
	}
	verdict := HerdrShieldRetirement{Guarded: true}
	status, err := InspectHerdrShell(home, env)
	if err != nil {
		verdict.Err = fmt.Errorf("herdr claude integration in %s: read Herdr's default_shell: %w", store.Settings, err)
		return verdict
	}
	if retiredShieldShell(status.Value) {
		verdict.Skip = fmt.Sprintf(
			"herdr claude integration in %s: Herdr's default_shell in %s still names %s, so its guarded hook stays"+
				" — end the chats in Herdr, remove default_shell = %q from %s, restart Herdr, then run pfm install --yes again",
			store.Settings, status.Path, retiredShieldProgram, status.Value, status.Path,
		)
	}
	return verdict
}

// retiredShieldShell reports whether a default_shell value runs herdr-shield.
func retiredShieldShell(value string) bool {
	program := value
	if words, err := cmdparse.SplitShellWords(value); err == nil && len(words) > 0 {
		program = words[0]
	}
	return filepath.Base(program) == retiredShieldProgram
}

// unwrapHerdrShieldHook rewrites the one guarded Herdr hook in the physical
// settings.json to Herdr's plain form, every other byte kept, in place through
// the physical path so a symlinked account settings.json stays a link. A hook
// that is not exactly one JSON string literal in the file is an error: the
// file is never rewritten from a guess.
func unwrapHerdrShieldHook(settings string) error {
	hooks, err := herdr.ClaudeIntegrationHooks(settings)
	if err != nil {
		return fmt.Errorf("herdr claude integration: %w", err)
	}
	if len(hooks) != 1 || !strings.HasPrefix(hooks[0], retiredShieldGuard) {
		return fmt.Errorf(
			"herdr claude integration: %s holds %q, want one hook guarded by %s", settings, hooks, retiredShieldProgram,
		)
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		return fmt.Errorf("herdr claude integration: read %s: %w", settings, err)
	}
	info, err := os.Stat(settings)
	if err != nil {
		return fmt.Errorf("herdr claude integration: stat %s: %w", settings, err)
	}
	guarded, err := jsonStringLiteral(hooks[0])
	if err != nil {
		return err
	}
	plain, err := jsonStringLiteral(strings.TrimPrefix(hooks[0], retiredShieldGuard))
	if err != nil {
		return err
	}
	if found := bytes.Count(raw, guarded); found != 1 {
		return fmt.Errorf(
			"herdr claude integration: %s holds the hook literal %s %d times, want exactly 1",
			settings,
			guarded,
			found,
		)
	}
	if err := atomicfile.Write(settings, bytes.Replace(raw, guarded, plain, 1), info.Mode().Perm()); err != nil {
		return fmt.Errorf("herdr claude integration: write %s: %w", settings, err)
	}
	return nil
}

// jsonStringLiteral is value as the JSON string literal Claude's settings.json
// holds it, unescaped HTML as Claude writes it.
func jsonStringLiteral(value string) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode Claude hook command %q: %w", value, err)
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}
