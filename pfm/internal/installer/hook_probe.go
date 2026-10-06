package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The states of one doctor hook row (docs/design/hooks/hooks.md § The pfm
// doctor check). MISSING reuses HostOverlayMissing's "missing".
const (
	stateOK = "ok"
	// stateMissing is an expected hook absent from its file; the same word the
	// host overlay rows use.
	stateMissing = string(HostOverlayMissing)
	// stateUntrusted is a pfm Codex hook present with no recorded Codex trust:
	// Codex may refuse to run an untrusted hook. A warning.
	stateUntrusted = "untrusted"
	// stateHookDrift is a pfm hook present in a shape pfm install converges
	// away: a wrong event or matcher, another binary path, an executable
	// that does not resolve, a duplicate, a missing async flag. A failure.
	stateHookDrift = "hook-drift"
	// stateDrift is the ownership ledger disagreeing with the file. A warning.
	stateDrift = "drift"
	// stateStale is a retired or unknown pfm hook still registered.
	stateStale = "stale"
	// stateUnreadable is a file that exists but could not be read, parsed or
	// shape-checked: an error, never absence, so it carries no MISSING rows.
	stateUnreadable = "unreadable"
	// stateNoClaudeConfig is a machine config naming no Claude config dir:
	// nothing to probe, said out loud rather than passed silently.
	stateNoClaudeConfig = "no-claude-config"
)

var (
	hookTargetClaude = pfmengine.MustLookup(pfmengine.Claude).LongName
	hookTargetCodex  = pfmengine.MustLookup(pfmengine.Codex).LongName
)

// HookProbeResult is one doctor hook row. What, Want and Got name the
// difference a hook-drift row found, and Want/Got carry the ledger and file
// counts of a ledger drift row.
type HookProbeResult struct {
	Hook  ExpectedHook
	State string
	Error string
	What  string
	Want  string
	Got   string
}

// ProbeExpectedHooks checks the launch hook binary once and the one hook pfm
// owns in each Codex hooks.json, plus Codex hook residue. Claude account settings are inspected by the pfm-settings host check.
func ProbeExpectedHooks(home string, config pfmconfig.Config) []HookProbeResult {
	results := []HookProbeResult{}
	if len(config.Accounts) == 0 {
		results = append(results, HookProbeResult{
			Hook: ExpectedHook{Target: hookTargetClaude}, State: stateNoClaudeConfig,
		})
	}
	binary := filepath.Join(home, ".local", "bin", "pfm")
	if templates := claudeHookTemplates(home); len(templates) > 0 {
		if fields := strings.Fields(templates[0].Command); len(fields) > 0 {
			binary = fields[0]
		}
	}
	if verdict := executableVerdict(binary); verdict != "" {
		results = append(results, HookProbeResult{
			Hook:  ExpectedHook{Target: hookTargetClaude, Name: binary},
			State: stateHookDrift, What: "executable", Want: binary, Got: verdict,
		})
	}
	results = append(results, probeCodexHooks(home, config, binary)...)
	ownershipPath := settingsHookOwnershipPath(managedRootForHome(home))
	ownership, _, err := readSettingsHookOwnership(ownershipPath)
	if err != nil {
		results = append(
			results,
			HookProbeResult{
				Hook:  ExpectedHook{Target: "ownership", File: ownershipPath},
				State: stateUnreadable,
				Error: err.Error(),
			},
		)
	} else {
		seen := map[string]bool{}
		// The ledger row of the one hook pfm owns in a Codex hooks.json is
		// expected, not drift; probeCodexHooks judges that hook itself.
		var ownedCodexHook *ExpectedHook
		if hook, hookErr := codexResumeUnkillHook(home); hookErr == nil {
			ownedCodexHook = &hook
		}
		for _, account := range config.CodexAccounts {
			path := physicalSettingsPath(filepath.Join(account.Home, "hooks.json"))
			if seen[path] {
				continue
			}
			seen[path] = true
			for _, key := range sortedHookKeys(ownership[path]) {
				if ownedCodexHook != nil && key.Event == ownedCodexHook.Event &&
					key.Matcher == ownedCodexHook.Matcher && key.Command == ownedCodexHook.Command {
					continue
				}
				results = append(results, HookProbeResult{
					Hook: ExpectedHook{
						Target:  "ownership",
						File:    path,
						Event:   key.Event,
						Matcher: key.Matcher,
						Command: key.Command,
						Name:    "unexpected",
					},
					State: stateDrift,
					Want:  strconv.Itoa(ownership[path][key]),
					Got:   "not-expected",
				})
			}
		}
	}
	return results
}

// readHookFile reads a settings.json or hooks.json. absent is true only when
// nothing is there; a dangling symlink is an error, never absence.
func readHookFile(path string) (raw []byte, absent bool, err error) {
	raw, err = os.ReadFile(path)
	if err == nil {
		return raw, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	info, statErr := os.Lstat(path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s is a dangling symlink", path)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("inspect %s: %w", path, statErr)
	}
	return nil, true, nil
}

// executableVerdict is "" for a usable executable, else what is wrong; a stat
// that failed for another reason says so rather than claiming absence.
func executableVerdict(path string) string {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "absent"
	case err != nil:
		return "stat-failed(" + err.Error() + ")"
	case !info.Mode().IsRegular():
		return "not-regular-file"
	case info.Mode().Perm()&0o111 == 0:
		return "not-executable"
	}
	return ""
}

// probeCodexHooks reads every configured Codex home's hooks.json for the one
// hook pfm owns there and for pfm residue. It never runs Codex or writes a
// file: an absent file or a missing SessionStart "resume" resume-unkill handler
// is a MISSING row, a handler with no recorded hook trust an UNTRUSTED row
// naming `pfm install --yes`, a healthy account none; STALE and UNREADABLE
// rows report residue and files that could not be read.
func probeCodexHooks(home string, config pfmconfig.Config, pfmBinary string) []HookProbeResult {
	var results []HookProbeResult
	expected, err := codexResumeUnkillHook(home)
	if err != nil {
		return []HookProbeResult{{
			Hook:  ExpectedHook{Target: hookTargetCodex, Name: codexResumeUnkillHookName},
			State: stateUnreadable, Error: err.Error(),
		}}
	}
	seen := map[string]bool{}
	for _, account := range config.CodexAccounts {
		path := filepath.Join(account.Home, "hooks.json")
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		target := fmt.Sprintf("%s[%d]", hookTargetCodex, account.ID)
		expectedHook := expected
		expectedHook.Target, expectedHook.File = target, path
		unreadable := func(err error) {
			results = append(results, HookProbeResult{
				Hook: ExpectedHook{Target: target, File: path}, State: stateUnreadable, Error: err.Error(),
			})
		}
		raw, absent, err := readHookFile(path)
		if err != nil {
			unreadable(err)
			continue
		}
		if absent {
			results = append(results, HookProbeResult{Hook: expectedHook, State: stateMissing})
			continue
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			unreadable(fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		if err := validateCodexHooks(document); err != nil {
			unreadable(err)
			continue
		}
		switch {
		case codexHookHandlerCount(document, expected) == 0:
			results = append(results, HookProbeResult{Hook: expectedHook, State: stateMissing})
		default:
			recorded, err := codexappendix.HookTrustState(account.Home, expected.Command)
			if err != nil {
				unreadable(err)
			} else if !recorded {
				results = append(results, HookProbeResult{
					Hook: expectedHook, State: stateUntrusted,
					Error: "no Codex trust is recorded for the hook, so Codex may refuse to run it",
				})
			}
		}
		for _, key := range sortedHookKeys(countSettingsHookCommands(document)) {
			if stale, found := staleHookResult(target, path, key, pfmBinary, home, true); found {
				results = append(results, stale)
			}
		}
	}
	return results
}

// staleHookResult reports a retired or unknown pfm hook: the shared retired
// table, Codex's own retired SessionStart shapes, and pfm's own shape naming
// a subcommand this binary does not implement (issue #24 finding 2).
func staleHookResult(
	target, file string,
	key settingsHookKey,
	pfmBinary, home string,
	codex bool,
) (HookProbeResult, bool) {
	stale := func(name, detail string) (HookProbeResult, bool) {
		return HookProbeResult{
			Hook: ExpectedHook{
				Target: target, File: file,
				Event: key.Event, Matcher: key.Matcher, Command: key.Command, Name: name,
			},
			State: stateStale, Error: detail,
		}, true
	}
	if name, retired := retiredHookCommandName(key.Command); retired {
		return stale(name, "retired hook command is still present")
	}
	if codex && key.Event == "SessionStart" {
		if name, retired := codexRetiredSessionStartHookName(key.Command, home); retired {
			return stale(name, "retired Codex hook command is still present")
		}
	}
	if name, unknown := unknownPFMHookCommand(key.Command, pfmBinary); unknown {
		return stale("unknown:"+name,
			"hook names a pfm subcommand this pfm does not implement (left by a newer or rolled-back pfm)")
	}
	return HookProbeResult{}, false
}

func sortedHookKeys(counts settingsHookCounts) []settingsHookKey {
	keys := make([]settingsHookKey, 0, len(counts))
	for key, count := range counts {
		if count > 0 {
			keys = append(keys, key)
		}
	}
	sortHookKeys(keys)
	return keys
}

func sortHookKeys(keys []settingsHookKey) {
	sort.Slice(keys, func(left, right int) bool {
		l, r := keys[left], keys[right]
		return l.Event+"\x00"+l.Matcher+"\x00"+l.Command < r.Event+"\x00"+r.Matcher+"\x00"+r.Command
	})
}

// HookProbeOverride is nil in production; a fleet test main may swap it for
// a deterministic stub exactly like ReportHooks' own probe (the same seam
// dependencyProbeOverride uses in cmd/pfm), so a jail can pin every hook "ok"
// without staging real settings.json content for it.
var HookProbeOverride func(home string, machine pfmconfig.Config) []HookProbeResult

// ReportHooks prints the launch executable, Codex hook and Codex residue checks.
func ReportHooks(stdout io.Writer, home string, machine pfmconfig.Config, _ bool) (warnings, failures int) {
	results := ProbeExpectedHooks(home, machine)
	if HookProbeOverride != nil {
		results = HookProbeOverride(home, machine)
	}
	for index := range results {
		result := &results[index]
		hook := result.Hook
		if result.State == stateNoClaudeConfig {
			warnings++
			fmt.Fprintln(stdout, "doctor: hook claude none — no Claude config dir is configured in the machine config")
			continue
		}
		if hook.Target == hookTargetClaude && result.What == "executable" {
			failures++
			fmt.Fprintf(
				stdout,
				"doctor: hook claude %s DRIFT what=executable want=%s got=%s — run pfm install\n",
				hook.Name,
				result.Want,
				result.Got,
			)
			continue
		}
		file := filepath.Base(hook.File)
		prefix := fmt.Sprintf("doctor: hook %s %s %s %s", hook.Target, file, hook.Event, hook.Name)
		switch result.State {
		case stateOK:
			fmt.Fprintln(stdout, prefix+" ok")
		case stateMissing:
			failures++
			fmt.Fprintf(stdout, "%s MISSING — run pfm install --yes\n", prefix)
		case stateUntrusted:
			warnings++
			fmt.Fprintf(stdout, "%s UNTRUSTED %s — run pfm install --yes\n", prefix, result.Error)
		case stateStale:
			failures++
			fmt.Fprintf(stdout, "%s STALE %s — run pfm install\n", prefix, hook.Name)
		case stateDrift:
			warnings++
			fmt.Fprintf(stdout, "%s DRIFT ledger ownership=%s file=%s\n", prefix, result.Want, result.Got)
		case stateUnreadable:
			failures++
			fmt.Fprintf(stdout, "doctor: hook %s %s UNREADABLE error=%s\n", hook.Target, file, result.Error)
		default:
			failures++
			fmt.Fprintf(stdout, "%s UNKNOWN-STATE state=%q\n", prefix, result.State)
		}
	}
	return warnings, failures
}
