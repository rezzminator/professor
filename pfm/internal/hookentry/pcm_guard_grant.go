package hookentry

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// pcmGuardGrantFile, in a project's .claude/, grants an agent directories
	// under that .claude/ (pcmGuardJudge.granted).
	pcmGuardGrantFile = "pcm-guard.json"
	pcmGuardClaudeDir = ".claude"
)

// granted reports a project grant admitting the caller to abs: each of its
// forms, as written and physical, unprotected or under a directory its
// project's .claude/pcm-guard.json grants the caller's exact agent_type
// ({"writers": {"agent": [".claude/dir", …]}}). Only a path protected for
// lying under a .claude element qualifies: never a CLAUDE.md, the clone's
// templates/, a config dir or the grant file, never the main chat, never
// in engine mode.
func (judge *pcmGuardJudge) granted(abs string) bool {
	if judge.scope.engine || judge.agent == "" {
		return false
	}
	for _, form := range []string{abs, pcmGuardPhysical(abs)} {
		if protected, _ := judge.scope.classOne(form); protected && !judge.grantedOne(form) {
			return false
		}
	}
	return true
}

func (judge *pcmGuardJudge) grantedOne(path string) bool {
	if strings.EqualFold(filepath.Base(path), "CLAUDE.md") {
		return false
	}
	if in, _ := judge.scope.templates(path); in {
		return false
	}
	for _, config := range judge.scope.configs {
		if _, ok := pcmGuardUnder(path, config); ok {
			return false
		}
	}
	elems := strings.Split(path, string(filepath.Separator))
	last := len(elems) - 1
	for last >= 0 && !strings.EqualFold(elems[last], pcmGuardClaudeDir) {
		last--
	}
	if last < 0 {
		return false
	}
	root := cmp.Or(strings.Join(elems[:last], string(filepath.Separator)), string(filepath.Separator))
	grantFile := filepath.Join(root, pcmGuardClaudeDir, pcmGuardGrantFile)
	if path == grantFile {
		return false
	}
	return slices.ContainsFunc(judge.grantDirs(grantFile), func(dir string) bool {
		dir = filepath.Join(root, dir)
		return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
	})
}

// grantDirs reads the directories grantFile grants the caller, relative to
// its project. A missing file grants none in silence; a link, an unreadable
// or malformed file, or an entry outside .claude/ grants none with one
// stderr line, so the deny stands.
func (judge *pcmGuardJudge) grantDirs(grantFile string) []string {
	if dirs, ok := judge.grants[grantFile]; ok {
		return dirs
	}
	if judge.grants == nil {
		judge.grants = map[string][]string{}
	}
	judge.grants[grantFile] = nil
	writers, err := pcmGuardReadGrant(grantFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		fmt.Fprintf(judge.scope.warn, "pfm internal pcm-guard: grant file %s grants nothing: %v\n", grantFile, err)
		return nil
	}
	var dirs []string
	for _, dir := range writers[judge.agent] {
		clean := filepath.Clean(dir)
		first, _, nested := strings.Cut(clean, string(filepath.Separator))
		if !filepath.IsLocal(clean) || first != pcmGuardClaudeDir || !nested {
			fmt.Fprintf(judge.scope.warn, "pfm internal pcm-guard: grant file %s: %q is no directory under"+
				" .claude/; it grants nothing\n", grantFile, dir)
			continue
		}
		dirs = append(dirs, clean)
	}
	judge.grants[grantFile] = dirs
	return dirs
}

// pcmGuardReadGrant decodes a grant file's writers. A symlink is refused:
// its target could lie outside the protected .claude/.
func pcmGuardReadGrant(grantFile string) (map[string][]string, error) {
	info, err := os.Lstat(grantFile)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, errors.New("it is a symlink")
	}
	raw, err := os.ReadFile(grantFile)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var grant struct {
		Writers map[string][]string `json:"writers"`
	}
	if err := json.Unmarshal(raw, &grant); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return grant.Writers, nil
}
