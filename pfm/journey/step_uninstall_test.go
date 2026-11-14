//go:build journey

package journey

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// ownerKeep is the file infra/fence/journey/prepare.sh leaves in ~/.claude before pfm is installed.
const ownerKeep = "the owner wrote this before pfm was installed\n"

// ownerKeepMember is the value of the member prepare.sh writes into ~/.claude/settings.json before pfm
// is installed.
const ownerKeepMember = "the owner set this before pfm was installed"

// shieldGuard is the guard an older pfm wrapped Herdr's Claude hook in while herdr-shield held its panes.
const shieldGuard = `[ -n "${HERDR_SHIELD_INNER:-}" ] || `

// uninstall: the owner stops Herdr and uninstalls pfm, and sees its summary; pfm's services are gone,
// the file the owner kept in ~/.claude, the skill he keeps in ~/.agents/skills and the owner's Claude
// settings are as they were, and what install wrote there is taken back: no link into the clone left in
// any registry install links into, no {plugin}@professor entry or record, no professor marketplace, and Herdr's
// own hook back in its plain form from the guarded form an older pfm left (planted before the uninstall).
func uninstall(c *harness.Ctx, s State) State {
	settingsPath := filepath.Join(s.Home, ".claude", "settings.json")
	before := read(c, "cat", settingsPath)
	if ids := professorEntries(c, settingsPath, before, "enabledPlugins"); len(ids) == 0 {
		c.Failf("%s holds no {plugin}@professor entry for the uninstall to take back:\n%s", settingsPath, before)
	}
	plain := herdrHooks(c, settingsPath, before)
	if len(plain) != 1 || strings.HasPrefix(plain[0], shieldGuard) {
		c.Failf("%s holds Herdr hooks %q before the uninstall, want one in its plain form", settingsPath, plain)
	}
	plantShieldGuard(c, settingsPath, before, plain[0])
	ownSkill := filepath.Join(s.Home, ".agents", "skills", "owner-skill", "SKILL.md")
	r := c.Read("sh", "-c", `mkdir -p "${1%/*}" && printf '%s' "$2" > "$1"`, "sh", ownSkill, ownerKeep)
	if r.Code != 0 {
		c.Failf("writing the owner's own skill %s exited %d: %s%s", ownSkill, r.Code, r.Stdout, r.Stderr)
	}
	if links := cloneLinks(c, s); !slices.ContainsFunc(links, func(l string) bool {
		return strings.HasPrefix(l, filepath.Join(s.Home, ".agents", "skills")+"/")
	}) || !slices.ContainsFunc(links, func(l string) bool {
		return strings.HasPrefix(l, filepath.Join(s.Home, ".claude", "rules")+"/")
	}) {
		c.Failf("links into the clone before the uninstall %q, want some in ~/.agents/skills and ~/.claude/rules",
			links)
	}
	term := c.Terminal("plain")
	term.WaitFor(prompt)
	if slices.ContainsFunc(herdrSessions(c), func(r State) bool { return r.Session == "" }) {
		if code := shellRun(term, "herdr-stop", "herdr server stop", 20*time.Second); code != 0 {
			c.Failf("herdr server stop exited %d:\n%s", code, term.Screen())
		}
	}
	if code := shellRun(term, "uninstall", "pfm uninstall", 60*time.Second); code != 0 {
		c.Failf("pfm uninstall exited %d:\n%s", code, term.Screen())
	}
	if screen := term.Screen(); !strings.Contains(screen, "summary changed=") {
		c.Failf("the uninstall's last screen has no summary:\n%s", screen)
	}
	c.Poll("pfm's services gone", stepBound, func() (bool, string) {
		got := services(c)
		return got == "", got
	})
	// The uninstall keeps pfm's logs, as it keeps its state: the log root still holds the activity log.
	if r := c.Read("test", "-s", filepath.Join(logRoot(s.Home), "pfm.jsonl")); r.Code != 0 {
		c.Failf("%s is gone or empty after the uninstall, want pfm's logs kept", logRoot(s.Home))
	}
	if got := read(c, "cat", filepath.Join(s.Home, ".claude", "owner-keep.txt")); got != ownerKeep {
		c.Failf("~/.claude/owner-keep.txt = %q after the uninstall, want %q", got, ownerKeep)
	}
	settings := read(c, "cat", filepath.Join(s.Home, ".claude", "settings.json"))
	var member struct {
		OwnerKeep *string `json:"ownerKeep"`
	}
	if err := json.Unmarshal(
		[]byte(settings),
		&member,
	); err != nil || member.OwnerKeep == nil ||
		*member.OwnerKeep != ownerKeepMember {
		c.Failf(
			"~/.claude/settings.json after the uninstall (%v) = %q, want JSON whose ownerKeep is %q",
			err,
			settings,
			ownerKeepMember,
		)
	}
	if ids := professorEntries(c, settingsPath, settings, "enabledPlugins"); len(ids) != 0 {
		c.Failf("%s keeps enabledPlugins %q after the uninstall, want none @professor", settingsPath, ids)
	}
	if got := herdrHooks(c, settingsPath, settings); !slices.Equal(got, plain) {
		c.Failf("%s holds Herdr hooks %q after the uninstall, want Herdr's own %q", settingsPath, got, plain)
	}
	plugins := filepath.Join(s.Home, ".cc", "1", "plugins")
	installed := filepath.Join(plugins, "installed_plugins.json")
	if ids := professorEntries(c, installed, readIfPresent(c, installed), "plugins"); len(ids) != 0 {
		c.Failf("%s keeps %q after the uninstall, want no @professor record", installed, ids)
	}
	known := filepath.Join(plugins, "known_marketplaces.json")
	if ids := professorEntries(c, known, readIfPresent(c, known), ""); len(ids) != 0 {
		c.Failf("%s keeps marketplace %q after the uninstall, want no professor marketplace", known, ids)
	}
	if links := cloneLinks(c, s); len(links) != 0 {
		c.Failf("links into the clone after the uninstall, want none:\n%s", strings.Join(links, "\n"))
	}
	if got := read(c, "cat", ownSkill); got != ownerKeep {
		c.Failf("the owner's own skill %s = %q after the uninstall, want %q", ownSkill, got, ownerKeep)
	}
	for _, gone := range []string{
		filepath.Join(plugins, "cache", "professor"),
		filepath.Join(plugins, "marketplaces", "professor"),
		filepath.Join(s.Home, ".local", "share", "professor-dev"),
	} {
		if r := c.Read("test", "-e", gone); r.Code == 0 {
			c.Failf("%s is still there after the uninstall", gone)
		}
	}
	return s
}

// cloneLinks lists, "{link} -> {target}", every symlink in the registries pfm install links into
// (~/.claude/{agents,commands,rules,skills}, ~/.agents/skills, ~/.codex/skills) whose target resolves
// inside the professor clone.
func cloneLinks(c *harness.Ctx, s State) []string {
	r := c.Read("sh", "-c", `clone=$(cd "$1" && pwd -P) || exit 1; shift
for dir in "$@"; do
  [ -d "$dir" ] || continue
  find "$dir" -mindepth 1 -maxdepth 1 -type l | while IFS= read -r link; do
    target=$(readlink -f "$link") || target=$(readlink "$link")
    case "$target" in "$clone"/*) printf '%s -> %s\n' "$link" "$target" ;; esac
  done
done`, "sh", professorClone(s),
		filepath.Join(s.Home, ".claude", "agents"), filepath.Join(s.Home, ".claude", "commands"),
		filepath.Join(s.Home, ".claude", "rules"), filepath.Join(s.Home, ".claude", "skills"),
		filepath.Join(s.Home, ".agents", "skills"), filepath.Join(s.Home, ".codex", "skills"))
	if r.Code != 0 {
		c.Failf("listing the links into the clone exited %d: %s%s", r.Code, r.Stdout, r.Stderr)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(r.Stdout), " -> ", "->"))
}

// professorEntries names the keys of document's member (the document itself for "") that are pfm's:
// {plugin}@professor, or the professor marketplace at the top. Empty raw is an absent file: none.
func professorEntries(c *harness.Ctx, path, raw, member string) []string {
	if raw == "" {
		return nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		c.Failf("%s is not a JSON object (%v):\n%s", path, err, raw)
	}
	holder := document
	if member != "" {
		holder = nil
		if inner, ok := document[member]; ok {
			if err := json.Unmarshal(inner, &holder); err != nil {
				c.Failf("%s member %s is not an object (%v):\n%s", path, member, err, raw)
			}
		}
	}
	var ids []string
	for key := range holder {
		if strings.HasSuffix(key, "@professor") || member == "" && key == "professor" {
			ids = append(ids, key)
		}
	}
	slices.Sort(ids)
	return ids
}

// herdrHooks lists the hook commands of any event in Claude settings raw that run Herdr's
// herdr-agent-state.sh.
func herdrHooks(c *harness.Ctx, path, raw string) []string {
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		c.Failf("%s is not Claude settings (%v):\n%s", path, err, raw)
	}
	var hooks []string
	for _, entries := range settings.Hooks {
		for _, entry := range entries {
			for _, hook := range entry.Hooks {
				if strings.Contains(hook.Command, "herdr-agent-state.sh") {
					hooks = append(hooks, hook.Command)
				}
			}
		}
	}
	return hooks
}

// plantShieldGuard rewrites Herdr's plain hook in the settings file at path, whose content is raw, to the
// guarded form an older pfm wrote, every other byte kept, written in place so a symlinked file stays a link.
func plantShieldGuard(c *harness.Ctx, path, raw, plain string) {
	literal := func(value string) []byte {
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			c.Failf("encode hook %q: %v", value, err)
		}
		return bytes.TrimRight(out.Bytes(), "\n")
	}
	if found := bytes.Count([]byte(raw), literal(plain)); found != 1 {
		c.Failf("%s holds Herdr's hook literal %s %d times, want 1:\n%s", path, literal(plain), found, raw)
	}
	guarded := bytes.Replace([]byte(raw), literal(plain), literal(shieldGuard+plain), 1)
	if r := c.Read("sh", "-c", `printf '%s' "$2" > "$1"`, "sh", path, string(guarded)); r.Code != 0 {
		c.Failf("planting the guarded hook in %s exited %d: %s%s", path, r.Code, r.Stdout, r.Stderr)
	}
}

// readIfPresent is the file at path, or "" when there is none.
func readIfPresent(c *harness.Ctx, path string) string {
	r := c.Read("sh", "-c", `if [ -e "$1" ]; then cat "$1"; fi`, "sh", path)
	if r.Code != 0 {
		c.Failf("read %s exited %d: %s%s", path, r.Code, r.Stdout, r.Stderr)
	}
	return r.Stdout
}
