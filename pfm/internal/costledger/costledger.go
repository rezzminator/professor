// Package costledger owns the per-call cost ledger: {dir}/{session_id}.jsonl, one JSON line per
// Anthropic API call of one Claude Code session, appended by the cost sink and read by the status
// line, `pfm tokens` and (as optional data, never imported) callmeter.
package costledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	dirMode         = 0o700
	fileMode        = 0o600
	maxSessionIDLen = 128
	maxLineBytes    = 1 << 20
)

// Join names which halves of an API call a row was built from.
const (
	JoinLogSpan = "log+span"
	JoinLog     = "log"
	JoinSpan    = "span"
)

// UnknownAgent is the agent_id of a log-only row: the agent that made the call could not be learned.
const UnknownAgent = "?"

// Row is one ledger line. Field order is the on-disk key order of the contract.
type Row struct {
	RequestID           string `json:"request_id"`
	TS                  string `json:"ts"`
	SessionID           string `json:"session_id"`
	AgentID             string `json:"agent_id"`
	ParentAgentID       string `json:"parent_agent_id"`
	QuerySource         string `json:"query_source"`
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CostUSDMicros       *int64 `json:"cost_usd_micros"`
	Speed               string `json:"speed"`
	Effort              string `json:"effort"`
	Join                string `json:"join"`
}

// Ledger is one session's deduped rows in first-appearance order.
type Ledger struct {
	Present bool
	Rows    []Row
	Faults  int
}

// Totals is the sum over a set of rows.
type Totals struct {
	Micros              int64
	UnknownCost         int
	Rows                int
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// ValidSessionID reports whether id may name a ledger file: 1..128 characters of [A-Za-z0-9._-]
// and neither "." nor "..". Any local process can POST a session id to the sink, so it is
// checked before it is ever joined into a path.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > maxSessionIDLen || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// Path is the ledger file of a session under dir.
func Path(dir, sessionID string) (string, error) {
	if !ValidSessionID(sessionID) {
		return "", fmt.Errorf(
			"cost ledger: session id %q is not 1..%d characters of [A-Za-z0-9._-]",
			sessionID,
			maxSessionIDLen,
		)
	}
	return filepath.Join(dir, sessionID+".jsonl"), nil
}

// Append writes row as one line, one write(2) on an O_APPEND descriptor, to its session's file
// under dir, creating the directory 0700 and the file 0600.
func Append(dir string, row Row) error {
	if row.RequestID == "" {
		return errors.New("cost ledger: a row needs a request_id")
	}
	path, err := Path(dir, row.SessionID)
	if err != nil {
		return err
	}
	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("cost ledger: encode row %s: %w", row.RequestID, err)
	}
	line = append(line, '\n')
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("cost ledger: create %s: %w", dir, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, fileMode)
	if err != nil {
		return fmt.Errorf("cost ledger: open %s: %w", path, err)
	}
	if n, err := file.Write(line); err != nil || n != len(line) {
		_ = file.Close()
		return fmt.Errorf("cost ledger: write %s (%d of %d bytes): %v", path, n, len(line), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cost ledger: close %s: %w", path, err)
	}
	return nil
}

func joinRank(join string) int {
	switch join {
	case JoinLogSpan:
		return 3
	case JoinLog:
		return 2
	case JoinSpan:
		return 1
	}
	return 0
}

// ForSession parses and dedupes one session's ledger by request_id: the best join wins, the later line
// among equals. A line that is not a JSON object is a counted fault, a missing file is
// Present == false with no error.
func ForSession(dir, sessionID string) (Ledger, error) {
	path, err := Path(dir, sessionID)
	if err != nil {
		return Ledger{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Ledger{}, nil
	}
	if err != nil {
		return Ledger{}, fmt.Errorf("cost ledger: open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }() // read-only: a close error loses nothing
	ledger := Ledger{Present: true}
	index := map[string]int{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row Row
		if err := json.Unmarshal(line, &row); err != nil || row.RequestID == "" {
			ledger.Faults++
			continue
		}
		at, seen := index[row.RequestID]
		switch {
		case !seen:
			index[row.RequestID] = len(ledger.Rows)
			ledger.Rows = append(ledger.Rows, row)
		case joinRank(row.Join) >= joinRank(ledger.Rows[at].Join):
			ledger.Rows[at] = row
		}
	}
	if err := scanner.Err(); err != nil {
		return Ledger{}, fmt.Errorf("cost ledger: read %s: %w", path, err)
	}
	return ledger, nil
}

// Sum totals rows; a row with a null cost is counted in UnknownCost, never as zero.
func Sum(rows []Row) Totals {
	var total Totals
	for index := range rows {
		row := &rows[index]
		total.Rows++
		if row.CostUSDMicros == nil {
			total.UnknownCost++
		} else {
			total.Micros += *row.CostUSDMicros
		}
		total.InputTokens += row.InputTokens
		total.OutputTokens += row.OutputTokens
		total.CacheReadTokens += row.CacheReadTokens
		total.CacheCreationTokens += row.CacheCreationTokens
	}
	return total
}

// Side returns, in order, the rows whose request_id held does not report: calls no transcript of
// the session records (web search and fetch helpers, summaries, suggestions).
func (l Ledger) Side(held func(requestID string) bool) []Row {
	var side []Row
	for index := range l.Rows {
		if !held(l.Rows[index].RequestID) {
			side = append(side, l.Rows[index])
		}
	}
	return side
}

// Prune removes the *.jsonl files under dir whose mtime is older than maxAge before now and
// returns how many it removed. A missing dir is nothing to prune.
func Prune(dir string, now time.Time, maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cost ledger: list %s: %w", dir, err)
	}
	removed := 0
	var failures []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, fmt.Sprintf("stat %s: %v", entry.Name(), err))
			continue
		}
		if now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			failures = append(failures, fmt.Sprintf("remove %s: %v", entry.Name(), err))
			continue
		}
		removed++
	}
	if len(failures) > 0 {
		return removed, fmt.Errorf("cost ledger: prune %s: %s", dir, strings.Join(failures, "; "))
	}
	return removed, nil
}
