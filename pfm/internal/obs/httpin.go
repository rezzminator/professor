package obs

import (
	"log/slog"
	"net/http"
)

// compHTTPIn is the component every inbound HTTP record belongs to.
const compHTTPIn = "http.in"

// Handler wraps next in the http.in middleware (spec § Middleware): one record
// per request, written when next returns, carrying method, route (the label
// the mount site gives this chain), path (never the query), status, the bytes
// written, and dur_ms. Headers and bodies never reach it. A 5xx logs at
// ERROR, a 4xx at WARN, everything else at INFO; a handler that wrote nothing
// is recorded as the 200 the server sends for it.
//
// The writer handed to next keeps http.Flusher and Unwrap, because the MCP
// streamable transport type-asserts Flusher on every SSE event.
func Handler(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := request.Context()
		timing := current(ctx).timing
		started := timing.Now()
		recording := &inboundWriter{ResponseWriter: writer}
		next.ServeHTTP(recording, request)
		elapsed := timing.Now().Sub(started).Milliseconds()

		status := recording.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest:
			level = slog.LevelWarn
		}
		Logger(Component(ctx, compHTTPIn)).LogAttrs(ctx, level, "http.in.request",
			slog.String("op", "request"),
			slog.String("method", request.Method),
			slog.String("route", route),
			slog.String("path", request.URL.Path),
			slog.Int("status", status),
			slog.Int64("bytes", recording.bytes),
			slog.Int64(FieldDur, elapsed),
		)
	})
}

// inboundWriter counts what the handler wrote and remembers the status it
// set; every write goes straight through.
type inboundWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (writer *inboundWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *inboundWriter) Write(chunk []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	written, err := writer.ResponseWriter.Write(chunk)
	writer.bytes += int64(written)
	return written, err
}

// Flush forwards to the underlying writer when it streams; a writer that does
// not flush behaves as it always did.
func (writer *inboundWriter) Flush() {
	if flusher, streams := writer.ResponseWriter.(http.Flusher); streams {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (writer *inboundWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
