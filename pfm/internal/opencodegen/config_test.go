package opencodegen

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestSerializeConfigKeepsUnownedKey(t *testing.T) {
	raw := json.RawMessage(`"fixture"`)
	got := serializeOpenCodeConfig(map[string]json.RawMessage{"model": raw}, nil, false)
	if !strings.Contains(got, `"model": "fixture"`) {
		t.Fatalf("config dropped unowned key: %s", got)
	}
}

// TestSerializeConfigDeniesGitWritesInAFixedOrderAfterAllow reads the emitted
// bash rules the way OpenCode does — each pattern a whole-command glob, the
// last match winning — and asks real commands: every Git write verb, bare or
// behind `git -C <dir>`, is denied; a read-only command whose words merely
// start with or contain a verb (`git merge-base`, a path `src/reset.go`)
// still runs.
func TestSerializeConfigDeniesGitWritesInAFixedOrderAfterAllow(t *testing.T) {
	got := serializeOpenCodeConfig(nil, nil, false)
	var config struct {
		Permission struct {
			Bash json.RawMessage `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal(parseOpenCodeJSONC([]byte(got)), &config); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(config.Permission.Bash)))
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("bash rules %s: %v", config.Permission.Bash, err)
	}
	var patterns, actions []string
	for decoder.More() {
		key, keyErr := decoder.Token()
		value, valueErr := decoder.Token()
		if keyErr != nil || valueErr != nil {
			t.Fatalf("bash rules %s: %v %v", config.Permission.Bash, keyErr, valueErr)
		}
		patterns = append(patterns, key.(string))
		actions = append(actions, value.(string))
	}
	if len(patterns) == 0 || patterns[0] != "*" || actions[0] != opencodeAllow {
		t.Fatalf("bash rules must open with \"*\": allow so every deny after it wins: %s", config.Permission.Bash)
	}
	decide := func(command string) string {
		action := ""
		for index, pattern := range patterns {
			glob := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
			if regexp.MustCompile(glob).MatchString(command) {
				action = actions[index]
			}
		}
		return action
	}
	for command, want := range map[string]string{
		"git commit -m fix":                   opencodeDeny,
		"git commit":                          opencodeDeny,
		"git push origin develop":             opencodeDeny,
		"git tag v1.2.3":                      opencodeDeny,
		"git merge feature":                   opencodeDeny,
		"git rebase -i HEAD~2":                opencodeDeny,
		"git reset --hard":                    opencodeDeny,
		"git cherry-pick abc123":              opencodeDeny,
		"git revert abc123":                   opencodeDeny,
		"git am < patch.mbox":                 opencodeDeny,
		"git -C repo commit -m fix":           opencodeDeny,
		"git -C /work/repo push":              opencodeDeny,
		"git -C repo am":                      opencodeDeny,
		"gh release create v1.2.3":            opencodeDeny,
		"git status":                          opencodeAllow,
		"git merge-base --is-ancestor a b":    opencodeAllow,
		"git -C repo merge-base a b":          opencodeAllow,
		"git -C repo diff -- src/reset.go":    opencodeAllow,
		"git -C repo log -- ambient.go":       opencodeAllow,
		"git -C repo show HEAD:amend/file.go": opencodeAllow,
		"go test ./...":                       opencodeAllow,
	} {
		if got := decide(command); got != want {
			t.Errorf("OpenCode would %s %q, want %s", got, command, want)
		}
	}
}
