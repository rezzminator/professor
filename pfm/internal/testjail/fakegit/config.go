package fakegit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var fakeGitSectionLine = regexp.MustCompile(`^\[([A-Za-z0-9.-]+)(?: "([^"]*)")?\]$`)

func (r *fakeGitRepo) configLines() []string {
	raw, err := os.ReadFile(filepath.Join(r.gitDir, "config"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

func (r *fakeGitRepo) configCheck() (int, error) {
	for number, line := range r.configLines() {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' || fakeGitSectionLine.MatchString(line) ||
			(line[0] != '[' && !strings.ContainsAny(line[:1], "=\"")) {
			continue
		}
		return number + 1, fmt.Errorf("bad config line")
	}
	return 0, nil
}

func fakeGitConfigSection(key string) (header, name string) {
	parts := strings.Split(key, ".")
	name = parts[len(parts)-1]
	if len(parts) == 2 {
		return "[" + parts[0] + "]", name
	}
	return "[" + parts[0] + " \"" + strings.Join(parts[1:len(parts)-1], ".") + "\"]", name
}

func (c *fakeGitCall) configGet(r *fakeGitRepo, key string) (string, bool) {
	if value, ok := c.overrides[strings.ToLower(key)]; ok {
		return value, true
	}
	header, name := fakeGitConfigSection(key)
	value, found, section := "", false, ""
	for _, line := range r.configLines() {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		if section == header && strings.EqualFold(strings.TrimSpace(k), name) {
			value, found = strings.TrimSpace(v), true
		}
	}
	return value, found
}

func (r *fakeGitRepo) configSet(key, value string) error {
	header, name := fakeGitConfigSection(key)
	lines, section, at := r.configLines(), "", -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section = trimmed
			if section == header {
				at = i + 1
			}
			continue
		}
		if section != header {
			continue
		}
		at = i + 1
		if k, _, _ := strings.Cut(trimmed, "="); strings.EqualFold(strings.TrimSpace(k), name) {
			lines[i] = "\t" + name + " = " + value
			return r.writeConfig(lines)
		}
	}
	if at < 0 {
		lines = append(lines, header, "\t"+name+" = "+value)
	} else {
		lines = append(lines[:at], append([]string{"\t" + name + " = " + value}, lines[at:]...)...)
	}
	return r.writeConfig(lines)
}

func (r *fakeGitRepo) writeConfig(lines []string) error {
	return os.WriteFile(filepath.Join(r.gitDir, "config"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
