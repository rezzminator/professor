package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func classifySessionStore(env LayoutEnv) []LayoutFinding {
	store := filepath.Join(env.Home, ".claude")
	findings := []LayoutFinding{}
	for _, dir := range accountDirs(env) {
		if physicalSettingsPath(dir) == physicalSettingsPath(store) {
			continue
		}
		var live []string
		var liveErr error
		scanned := false
		for _, entry := range SessionPaths {
			path := filepath.Join(dir, entry)
			want := filepath.Join(store, entry)
			finding, info, exists := layoutLstat(layoutRowSessionStore, path)
			if finding.Err == nil {
				switch {
				case !exists:
					finding.Verdict = VerdictCreate
				case info.Mode()&os.ModeSymlink != 0:
					target, err := os.Readlink(path)
					if err != nil {
						finding.Err = err
						break
					}
					if !filepath.IsAbs(target) {
						target = filepath.Join(dir, target)
					}
					target = filepath.Clean(target)
					finding.Source = target
					if target == want {
						break
					}
					if physicalSettingsPath(target) == physicalSettingsPath(want) &&
						accountLinkTarget(target, entry, accountDirs(env)) {
						finding.Verdict = VerdictRepoint
					} else {
						finding.Verdict = VerdictRefuse
					}
					finding.Detail = target
				case info.IsDir():
					children, err := os.ReadDir(path)
					if err != nil {
						finding.Err = err
						break
					}
					finding.Verdict = VerdictMerge
					finding.Detail = fmt.Sprintf("%d entries", len(children))
					judgeSessionOwnership(env, &finding, path)
				default:
					finding.Verdict, finding.Detail = VerdictRefuse, "not a directory or link"
				}
			}
			if finding.Err == nil && (!exists || finding.Source == want) {
				storeInfo, err := os.Stat(want)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					finding.Verdict = VerdictCreate
				case err != nil:
					finding.Verdict = VerdictRefuse
					finding.Err = fmt.Errorf("stat store entry %s: %w", want, err)
				case !storeInfo.IsDir():
					finding.Verdict, finding.Detail = VerdictRefuse, "store entry is not a directory"
				}
			}
			if finding.Verdict != VerdictOK {
				if !scanned {
					live, liveErr = liveChatPIDs(env.ProcRoot, dir)
					scanned = true
				}
				if liveErr != nil {
					finding.Err = liveErr
				} else if len(live) > 0 {
					finding.Verdict, finding.Detail = VerdictRefuse, "live chats: "+strings.Join(live, ",")
				}
			}
			findings = append(findings, finding)
		}
	}
	return findings
}

func accountLinkTarget(target, entry string, dirs []string) bool {
	for _, dir := range dirs {
		if target == filepath.Join(dir, entry) {
			return true
		}
	}
	return false
}
