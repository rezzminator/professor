package obs

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// redactedValue replaces a value the rule refuses; the reason is spelled into
// it so a reader sees a REFUSED field rather than a missing one.
const redactedValue = "<redacted>"

// secretMarkers are the substrings that make a value a credential no matter
// which key carries it: an API key, an Authorization header (bearer or basic),
// an OAuth blob, or a credential-shaped field regardless of scheme spelling —
// a password/token/secret/api-key assignment, a cookie header. They are the
// LAST line, not the plan: a middleware logs the SHAPE of an argument (argc,
// host, path, bytes), never the argument. Matched against the lowered text,
// so the spelling here is already lower case; a marker needing "=" (rather
// than the bare word) is deliberate — "tokenizer" and "secretary" must not
// trip it.
var secretMarkers = []string{
	"sk-", "bearer ", "basic ", "authorization", "oauth",
	"password=", "passwd=", "token=", "secret=", "api_key=", "apikey=", "api-key",
	"cookie:", "set-cookie", "x-api-key",
}

// jwtSegment matches a JSON Web Token's compact serialization: three
// base64url segments separated by dots, the header segment starting "eyJ"
// (base64 of `{"`). Matched against the untruncated, original-case text —
// a JWT is case-sensitive.
var jwtSegment = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// declaredKeys is the activity log's allow-list: the only field keys a record
// may carry a real value under. Everything else is refused at the handler, so
// a new call site cannot introduce a field nobody reviewed — the one place the
// question "could this log a secret?" is answered for the whole engine.
var declaredKeys = map[string]bool{
	FieldTime:       true,
	slog.LevelKey:   true,
	slog.MessageKey: true,
	FieldCmd:        true,
	FieldPID:        true,
	FieldVersion:    true,
	FieldChat:       true,
	FieldSeat:       true,
	FieldEngine:     true,
	FieldSock:       true,
	FieldDur:        true,
	FieldErr:        true,
	FieldExit:       true,
	FieldComp:       true,
	// Operational fields every corner of the engine reports with: what it
	// decided, why, over what, and how much.
	"state":    true,
	"reason":   true,
	"decision": true,
	"step":     true,
	"target":   true,
	"path":     true,
	"tool":     true,
	"hook":     true,
	"count":    true,
	"bytes":    true,
	"argv":     true,
	"args":     true,
	"pane":     true,
	"window":   true,
	"session":  true,
	// The argument and result SHAPE fields the middleware doors report
	// (spec § Middleware): a count, a name, a code — never the value itself.
	"argc":    true,
	"host":    true,
	"status":  true,
	"rows":    true,
	"table":   true,
	"kind":    true,
	"op":      true,
	"route":   true,
	"method":  true,
	"prior":   true,
	"next":    true,
	"cause":   true,
	"subcmd":  true,
	"retries": true,
	"line":    true, // Lane B: one harvestpy stderr line (Process.Stderr), a WARN record per line
}

// Declared reports whether key is on the activity log's allow-list.
func Declared(key string) bool { return declaredKeys[key] }

// Scrub is THE redaction rule of the activity log, applied by every handler
// this package builds (file, stderr mirror and obs.Test alike) as slog's
// ReplaceAttr: slog's "time" becomes FieldTime, a key the allow-list does not
// declare loses its value, a declared key's value is refused when it looks
// like a credential, and what survives is cut at MaxValueBytes (TruncateValue).
//
// It is deliberately the LAST thing before the writer: a call site that gets
// its field wrong writes a refused value, never a secret, and never more than
// the field cap.
func Scrub(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) == 0 && attr.Key == slog.TimeKey {
		attr.Key = FieldTime
		if attr.Value.Kind() == slog.KindTime {
			attr.Value = slog.StringValue(attr.Value.Time().UTC().Format(time.RFC3339Nano))
		}
		return attr
	}
	if !Declared(attr.Key) {
		return slog.String(attr.Key, redactedValue)
	}
	return slog.Attr{Key: attr.Key, Value: scrubValue(attr.Value)}
}

// scrubValue refuses a credential-looking value and caps the rest. Non-string
// kinds are rendered first: a token handed over as fmt.Stringer or wrapped in
// an error is the same token.
func scrubValue(value slog.Value) slog.Value {
	resolved := value.Resolve()
	switch resolved.Kind() {
	case slog.KindString, slog.KindAny:
		text := resolved.String()
		if resolved.Kind() == slog.KindAny {
			text = fmt.Sprint(resolved.Any())
		}
		lowered := strings.ToLower(text)
		for _, marker := range secretMarkers {
			if strings.Contains(lowered, marker) {
				return slog.StringValue(redactedValue)
			}
		}
		if jwtSegment.MatchString(text) {
			return slog.StringValue(redactedValue)
		}
		return slog.StringValue(TruncateValue(text))
	default:
		return resolved
	}
}
