package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type fileProcFS struct{ root string }

func (proc fileProcFS) Environ(pid int) (map[string]string, error) {
	content, err := os.ReadFile(filepath.Join(proc.root, strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, entry := range strings.Split(string(content), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result, nil
}

func (proc fileProcFS) Stat(pid int) (ProcStat, error) {
	content, err := os.ReadFile(filepath.Join(proc.root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return ProcStat{}, err
	}
	raw := string(content)
	closeParen := strings.LastIndex(raw, ") ")
	if closeParen < 0 {
		return ProcStat{}, fmt.Errorf("malformed proc stat for pid %d: missing closing command delimiter", pid)
	}
	fields := strings.Fields(raw[closeParen+2:])
	if len(fields) < 2 {
		return ProcStat{}, fmt.Errorf(
			"malformed proc stat for pid %d: expected parent field, got %d trailing fields",
			pid,
			len(fields),
		)
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcStat{}, fmt.Errorf("malformed proc stat for pid %d: invalid parent pid %q: %w", pid, fields[1], err)
	}
	return ProcStat{ParentPID: parent}, nil
}
