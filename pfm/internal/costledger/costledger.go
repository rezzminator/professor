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
	TraceID             string `json:"trace_id,omitempty"`
	SpanID              string `json:"span_id,omitempty"`
}

// Identity is a real request id, else an llm span's own OTLP trace/span pair.
// Logs carry the enclosing span context, reused across calls, so it is not their identity.
// Prefixes distinguish internal key namespaces, never fabricated request ids.
func (r Row) Identity() string {
	if r.RequestID != "" {
		return "request:" + r.RequestID
	}
	if r.Join == JoinSpan && validOTLPID(r.TraceID, 32) && validOTLPID(r.SpanID, 16) {
		return "span:" + strings.ToLower(r.TraceID) + ":" + strings.ToLower(r.SpanID)
	}
	return ""
}

func validOTLPID(id string, size int) bool {
	if len(id) != size || strings.Trim(id, "0") == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
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

// ObservationsDir holds session-attributed rows without request ids, outside
// the legacy session-file contract consumed by callmeter.
func ObservationsDir(dir string) string { return filepath.Join(dir, "observations") }

// SessionIDs lists session ledgers, including request-less observations.
func SessionIDs(dir string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, root := range []string{dir, ObservationsDir(dir)} {
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("cost ledger: list %s: %w", root, err)
		}
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".jsonl")
			if entry.IsDir() || !ok || !ValidSessionID(id) || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func ledgerPath(dir string, row Row) (string, error) {
	if row.SessionID == "" {
		return filepath.Join(dir, "unattributed", "records.jsonl"), nil
	}
	if row.RequestID == "" {
		return Path(ObservationsDir(dir), row.SessionID)
	}
	return Path(dir, row.SessionID)
}

// Unattributed reads observations for which telemetry provided no session. They are not
// assigned to a chat; rows without correlation remain separate, potentially overlapping observations.
func Unattributed(dir string) (Ledger, error) {
	return readLedger(filepath.Join(dir, "unattributed", "records.jsonl"), "")
}

// Append writes row as one line, one write(2) on an O_APPEND descriptor, to its session's file
// under dir, creating the directory 0700 and the file 0600.
func Append(dir string, row Row) error {
	if row.RequestID == "" && joinRank(row.Join) == 0 {
		return fmt.Errorf("cost ledger: row has invalid join %q", row.Join)
	}
	path, err := ledgerPath(dir, row)
	if err != nil {
		return err
	}
	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("cost ledger: encode row %s: %w", row.RequestID, err)
	}
	line = append(line, '\n')
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("cost ledger: create directory for %s: %w", path, err)
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
	ledger, err := readLedger(path, sessionID)
	if err != nil {
		return Ledger{}, err
	}
	observations, err := readLedger(filepath.Join(ObservationsDir(dir), sessionID+".jsonl"), sessionID)
	if err != nil {
		return Ledger{}, err
	}
	ledger.Present = ledger.Present || observations.Present
	ledger.Faults += observations.Faults
	ledger.Rows = append(ledger.Rows, observations.Rows...)
	return ledger, nil
}

func readLedger(path, sessionID string) (Ledger, error) {
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
		if err := json.Unmarshal(line, &row); err != nil || !bytes.HasPrefix(line, []byte("{")) ||
			row.SessionID != sessionID || (row.RequestID == "" && joinRank(row.Join) == 0) {
			ledger.Faults++
			continue
		}
		id := row.Identity()
		if id == "" {
			ledger.Rows = append(ledger.Rows, row)
			continue
		}
		at, seen := index[id]
		switch {
		case !seen:
			index[id] = len(ledger.Rows)
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
	removed, err := pruneDir(dir, now, maxAge)
	observations, observationErr := pruneDir(ObservationsDir(dir), now, maxAge)
	return removed + observations, errors.Join(err, observationErr)
}

func pruneDir(dir string, now time.Time, maxAge time.Duration) (int, error) {
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
