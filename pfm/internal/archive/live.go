package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// uuidPattern matches a session id wherever one appears — an argv, a
// transcript pathname, a rollout filename.
var uuidPattern = regexp.MustCompile(
	`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// LiveSessions is every chat that is running RIGHT NOW, by id.
//
// It is recomputed on every run and never read from a cached list: moving a
// transcript out from under a running chat either corrupts it or spawns a
// phantom, and a list that was true a minute ago is not a safety check.
//
// Three independent readings, because no single one covers both engines:
//
//   - every process's argv, which carries `claude --resume <uuid>` and
//     `codex … resume <uuid>`;
//   - a codex process's OPEN rollout descriptor and its exported thread id,
//     which is all a fresh codex session ever states;
//   - the sid crumbs, which map a live socket to the transcript its chat is
//     writing even when the process states nothing at all.
//
// A reading that could not run is reported as an error, never as an empty
// result: `archive --apply` treats this list as the whole safety gate between
// itself and a running chat's transcript, so a process table or sid directory
// that could not be read must refuse the decision rather than silently narrow
// it — the same law reap/proc.go's NewProcessTree applies to the process
// table.
func LiveSessions(
	proc gather.ProcFS,
	codexHome string,
	sidDir string,
	binaries ...string,
) (map[string]struct{}, error) {
	codexBinary := ""
	if len(binaries) > 0 {
		codexBinary = binaries[0]
	}
	live := make(map[string]struct{})
	pids, err := proc.PIDs()
	if err != nil {
		return nil, fmt.Errorf("list processes for the live-session reading: %w", err)
	}
	sessionsRoot := filepath.Join(codexHome, "sessions")
	for _, pid := range pids {
		cmdline, err := proc.Cmdline(pid)
		if err != nil {
			continue
		}
		for _, argument := range cmdline {
			for _, id := range uuidPattern.FindAllString(argument, -1) {
				live[strings.ToLower(id)] = struct{}{}
			}
		}
		if !gather.IsCodexCommand(cmdline, codexBinary) {
			continue
		}
		if environment, err := proc.Environ(pid); err == nil {
			if id := environment["CODEX_THREAD_ID"]; id != "" {
				live[strings.ToLower(id)] = struct{}{}
			}
		}
		links, err := proc.FDLinks(pid)
		if err != nil {
			continue
		}
		for _, link := range links {
			if !strings.HasPrefix(filepath.Clean(link.Target), sessionsRoot) {
				continue
			}
			for _, id := range uuidPattern.FindAllString(link.Target, -1) {
				live[strings.ToLower(id)] = struct{}{}
			}
		}
	}

	entries, err := os.ReadDir(sidDir)
	if os.IsNotExist(err) {
		// No chat has ever spawned on this box, so there is nothing to read —
		// genuine absence, not a failed reading.
		return live, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the sid crumb directory %s: %w", sidDir, err)
	}
	for _, entry := range entries {
		if _, _, ok := gather.ParseCrumbName(entry.Name()); !ok {
			continue
		}
		content, err := os.ReadFile(filepath.Join(sidDir, entry.Name()))
		if err != nil {
			continue
		}
		for _, id := range uuidPattern.FindAllString(string(content), -1) {
			live[strings.ToLower(id)] = struct{}{}
		}
	}
	return live, nil
}
