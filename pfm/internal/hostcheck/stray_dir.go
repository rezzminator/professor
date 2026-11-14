package hostcheck

import (
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func strayDir(env Env) ([]Row, error) {
	var rows []Row
	for _, name := range []string{".git", ".codex", ".agents"} {
		path := filepath.Join(filepath.Dir(config.DefaultAccountDir(env.Home, 1)), name)
		info := inspectPath(&rows, "stray-dir", path)
		if info == nil {
			continue
		}
		problem, fix := "not a directory", "inspect, then rm -r "+path
		if info.IsDir() {
			before := len(rows)
			entries := readDir(&rows, "stray-dir", path)
			if len(rows) != before {
				continue
			}
			problem = fmt.Sprintf("stray dir holds %d entries", len(entries))
			if len(entries) == 0 {
				problem, fix = "empty stray dir", "rmdir "+path
			}
		}
		rows = append(rows, Row{Warn, "stray-dir", path, problem, fix})
	}
	return rows, nil
}
