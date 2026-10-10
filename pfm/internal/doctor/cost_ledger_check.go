package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	// costSinkTailLines is how many of the sink log's last lines doctor shows.
	costSinkTailLines = 5
	// costSinkTailWindow bounds the bytes read from the end of the sink log: the log is
	// append-only, so the tail never needs its head.
	costSinkTailWindow = 8 << 10
)

// printCostLedgerDoctor names the cost ledger's switch (claude.costLedger, where its value came
// from) and directory, then the last lines of the sink log: the file a launcher or the detached
// sink writes its failures to, so a chat whose status line says "sink failed" has its cause one
// command away. A log nothing has written is ABSENT; one that could not be read is
// StateUnavailable with the reason and one warning, because "we failed to look" is not "there is
// nothing there". The switch row is a report, never a warning: off is the user's choice.
func printCostLedgerDoctor(stdout io.Writer, runtime config.Runtime, env paths.Env) int {
	state := claudelaunch.CostLedgerOn
	if !runtime.Config.Claude.CostLedger {
		state = claudelaunch.CostLedgerOff
	}
	fmt.Fprintf(
		stdout, "doctor: cost ledger state=%s source=%s dir=%s\n",
		state, runtime.Config.Source(config.KeyClaudeCostLedger), paths.CostLedgerDir(env, runtime.Paths.Home),
	)
	logPath := paths.CostSinkLog(runtime.Paths.Home)
	size, tail, err := readLogTail(logPath, costSinkTailLines)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stdout, "doctor: cost sink log path=%s state=absent\n", logPath)
		return 0
	case err != nil:
		fmt.Fprintf(stdout, "doctor: cost sink log path=%s state=%s error=%v\n", logPath, StateUnavailable, err)
		return 1
	}
	fmt.Fprintf(stdout, "doctor: cost sink log path=%s bytes=%d\n", logPath, size)
	for _, line := range tail {
		fmt.Fprintf(stdout, "doctor: cost sink log: %s\n", line)
	}
	return 0
}

// readLogTail returns the file's size and its last count non-empty lines, reading at most
// costSinkTailWindow bytes from its end; a line the window cuts is dropped, never shown half.
func readLogTail(path string, count int) (int64, []string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = file.Close() }() // read-only: a close error loses nothing
	info, err := file.Stat()
	if err != nil {
		return 0, nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return 0, nil, fmt.Errorf("%s is a directory, not a log file", path)
	}
	start := max(info.Size()-costSinkTailWindow, 0)
	window := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(window, start); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, fmt.Errorf("read %s: %w", path, err)
	}
	if start > 0 {
		cut := bytes.IndexByte(window, '\n')
		if cut < 0 {
			return info.Size(), nil, nil
		}
		window = window[cut+1:]
	}
	var lines []string
	for _, line := range strings.Split(string(window), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return info.Size(), lines[max(len(lines)-count, 0):], nil
}
