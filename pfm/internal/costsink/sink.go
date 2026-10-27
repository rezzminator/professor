// Package costsink owns the cost sink: a small OTLP/HTTP JSON receiver on 127.0.0.1 that a Claude
// launch points its OpenTelemetry exporters at. It keeps only the api_request log events and the
// claude_code.llm_request spans, joins them on request_id and appends one costledger row per API
// call. The sink runs detached from the chat it serves (Arm hands it the already-listening socket)
// and exits after a final drain once that chat's process is gone.
package costsink

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/costledger"
)

const (
	pathLogs   = "/v1/logs"
	pathTraces = "/v1/traces"
	// DefaultMaxBody caps one request body, compressed and decompressed alike.
	DefaultMaxBody = 8 << 20
	// DefaultWait is how long a half waits for its partner before it is written alone.
	DefaultWait = 20 * time.Second
)

// SinkOptions configures NewSink; zero Now, Wait and MaxBody take the defaults.
type SinkOptions struct {
	Dir     string
	Log     io.Writer
	Now     func() time.Time
	Wait    time.Duration
	MaxBody int64
}

// Sink is the HTTP receiver: it decodes OTLP/JSON POSTs into halves, joins them and appends the
// rows to the ledger. It never panics out of a request and never refuses a well-formed POST.
type Sink struct {
	dir          string
	log          io.Writer
	logMu        sync.Mutex
	now          func() time.Time
	maxBody      int64
	joiner       *Joiner
	faults       atomic.Int64
	last         atomic.Int64
	unattributed atomic.Int64
	warned       sync.Map
}

// NewSink returns a Sink appending to the ledger under opts.Dir.
func NewSink(opts SinkOptions) *Sink {
	sink := &Sink{dir: opts.Dir, log: opts.Log, now: opts.Now, maxBody: opts.MaxBody}
	if sink.now == nil {
		sink.now = clock.Real.Now
	}
	if sink.maxBody <= 0 {
		sink.maxBody = DefaultMaxBody
	}
	wait := opts.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	sink.joiner = NewJoiner(wait, sink.write)
	return sink
}

// Faults counts the POSTs and rows the sink refused or failed to write.
func (s *Sink) Faults() int { return int(s.faults.Load()) }

// Unattributed counts observations missing a session or a usable request correlation.
// It counts observations, not unique calls: identity-less retries cannot be deduplicated.
func (s *Sink) Unattributed() int { return int(s.unattributed.Load()) }

func (s *Sink) noteIdentity(path, session, identity string) {
	if session != "" && identity != "" {
		return
	}
	s.unattributed.Add(1)
	if _, seen := s.warned.LoadOrStore(path, true); !seen {
		s.Logf("%s: unattributed telemetry retained; missing session or request correlation; "+
			"identity-less observations may overlap or repeat (see cost ledger)", path)
	}
}

// LastPost is when the sink last received a POST, zero before the first.
func (s *Sink) LastPost() time.Time {
	nanos := s.last.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// Flush writes the halves that have waited out their time.
func (s *Sink) Flush() { s.joiner.Flush(s.now()) }

// Drain writes every pending half now.
func (s *Sink) Drain() { s.joiner.Drain() }

// Logf appends one line to the sink's log, the file `pfm doctor` shows the tail of.
func (s *Sink) Logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	line := fmt.Sprintf("%s cost-sink: %s\n", s.now().UTC().Format(timeLayoutMS), fmt.Sprintf(format, args...))
	s.logMu.Lock()
	defer s.logMu.Unlock()
	_, _ = io.WriteString(s.log, line)
}

func (s *Sink) fault(format string, args ...any) {
	s.faults.Add(1)
	s.Logf(format, args...)
}

func (s *Sink) write(row costledger.Row) {
	if err := costledger.Append(s.dir, row); err != nil {
		s.fault("ledger write for %s refused: %v", row.RequestID, err)
	}
}

// ServeHTTP answers POST /v1/logs and /v1/traces with 200 {}, any other path 404, any other
// method 405.
func (s *Sink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.fault("panic serving %s: %v", r.URL.Path, recovered)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()
	if r.URL.Path != pathLogs && r.URL.Path != pathTraces {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.last.Store(s.now().UnixNano())
	body, status, err := s.readBody(w, r)
	if err != nil {
		s.fault("%s: %v", r.URL.Path, err)
		http.Error(w, err.Error(), status)
		return
	}
	now := s.now()
	if r.URL.Path == pathLogs {
		err = s.ingestLogs(body, now)
	} else {
		err = s.ingestSpans(body, now)
	}
	answer := []byte("{}")
	var refused *RefusedError
	switch {
	case errors.As(err, &refused):
		// The batch was well-formed and its other records are joined: each refused one is a fault.
		s.faults.Add(int64(refused.Count - 1))
		s.fault("%s: %v", r.URL.Path, err)
		answer = s.partialSuccess(r.URL.Path, refused)
	case err != nil:
		s.fault("%s: %v", r.URL.Path, err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(answer)
}

// partialSuccess is the OTLP/HTTP answer to a batch accepted in part: partialSuccess naming how
// many records were refused and why, the count a string as proto3 JSON spells an int64.
func (s *Sink) partialSuccess(path string, refused *RefusedError) []byte {
	field := "rejectedSpans"
	if path == pathLogs {
		field = "rejectedLogRecords"
	}
	answer, err := json.Marshal(map[string]map[string]string{"partialSuccess": {
		field:          strconv.Itoa(refused.Count),
		"errorMessage": refused.Reason,
	}})
	if err != nil {
		s.Logf("%s: partial success answer not encoded, answering {}: %v", path, err)
		return []byte("{}")
	}
	return answer
}

// readBody reads the request body, gunzipping when asked, never holding more than the cap.
func (s *Sink) readBody(w http.ResponseWriter, r *http.Request) ([]byte, int, error) {
	var reader io.Reader = http.MaxBytesReader(w, r.Body, s.maxBody)
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		unzip, err := gzip.NewReader(reader)
		if err != nil {
			return nil, tooLargeOr(err, http.StatusBadRequest), fmt.Errorf("gzip body: %w", err)
		}
		defer func() { _ = unzip.Close() }() // a gzip reader's Close only releases it
		reader = io.LimitReader(unzip, s.maxBody+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, tooLargeOr(err, http.StatusBadRequest), fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > s.maxBody {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("body over the %d byte cap", s.maxBody)
	}
	return body, http.StatusOK, nil
}

func tooLargeOr(err error, fallback int) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return fallback
}

func (s *Sink) ingestLogs(body []byte, now time.Time) error {
	halves, err := DecodeLogs(body)
	if err != nil {
		return err
	}
	refused := 0
	for index := range halves {
		half := &halves[index]
		if half.SessionID != "" && !costledger.ValidSessionID(half.SessionID) {
			refused++
			continue
		}
		row := logOnlyRow(*half, now)
		s.noteIdentity(pathLogs, half.SessionID, row.Identity())
		s.joiner.AddLog(*half, now)
	}
	return unsafeSessions(refused)
}

func (s *Sink) ingestSpans(body []byte, now time.Time) error {
	halves, err := DecodeSpans(body)
	if err != nil {
		return err
	}
	refused := 0
	for index := range halves {
		half := &halves[index]
		if half.SessionID != "" && !costledger.ValidSessionID(half.SessionID) {
			refused++
			continue
		}
		row := spanOnlyRow(*half, now)
		s.noteIdentity(pathTraces, half.SessionID, row.Identity())
		s.joiner.AddSpan(*half, now)
	}
	return unsafeSessions(refused)
}

func unsafeSessions(count int) error {
	if count == 0 {
		return nil
	}
	return &RefusedError{Count: count, Reason: fmt.Sprintf("%d records refused: unsafe session id", count)}
}
