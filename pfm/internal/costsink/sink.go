// Package costsink owns the cost sink: a small OTLP/HTTP JSON receiver on 127.0.0.1 that a Claude
// launch points its OpenTelemetry exporters at. It keeps only the api_request log events and the
// claude_code.llm_request spans, joins them on request_id and appends one costledger row per API
// call. The sink runs detached from the chat it serves (Arm hands it the already-listening socket)
// and exits after a final drain once that chat's process is gone.
package costsink

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	dir     string
	log     io.Writer
	logMu   sync.Mutex
	now     func() time.Time
	maxBody int64
	joiner  *Joiner
	faults  atomic.Int64
	last    atomic.Int64
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
	if err != nil {
		s.fault("%s: %v", r.URL.Path, err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, "{}")
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
	for index := range halves {
		half := &halves[index]
		if !costledger.ValidSessionID(half.SessionID) {
			s.fault(
				"api_request %s refused: session id %q is outside the ledger's character class",
				half.RequestID,
				half.SessionID,
			)
			continue
		}
		if half.TS == "" {
			half.TS = now.UTC().Format(timeLayoutMS)
		}
		s.joiner.AddLog(*half, now)
	}
	return err
}

func (s *Sink) ingestSpans(body []byte, now time.Time) error {
	halves, err := DecodeSpans(body)
	for index := range halves {
		half := &halves[index]
		if !costledger.ValidSessionID(half.SessionID) {
			s.fault(
				"llm_request span %s refused: session id %q is outside the ledger's character class",
				half.RequestID,
				half.SessionID,
			)
			continue
		}
		if half.EndTS == "" {
			half.EndTS = now.UTC().Format(timeLayoutMS)
		}
		s.joiner.AddSpan(*half, now)
	}
	return err
}
