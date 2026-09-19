package obs

import (
	"fmt"
	"log/slog"
	"reflect"
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
	"cookie:", "set-cookie", "x-api-key", "x-subscription-token",
}

// apiKeyQuery matches a bare "key=" API-key-style query parameter at a query
// boundary (?key= or &key=, the Google Books API's shape) — anchored on the
// separator so it never fires on an ordinary word that merely ends in "key="
// (monkey=1, turkey=roast).
var apiKeyQuery = regexp.MustCompile(`(?i)[?&]key=`)

// jwtSegment matches a JSON Web Token's compact serialization: three
// base64url segments separated by dots, the header segment starting "eyJ"
// (base64 of `{"`). Matched against the untruncated, original-case text —
// a JWT is case-sensitive.
var jwtSegment = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// secretKeyNames are map-key / field names that mark whatever they hold as a
// credential regardless of the VALUE's own shape: a nested field spelled
// "password"/"token"/"secret"/… rather than flattened into the "key=value"
// text secretMarkers matches directly (Go's map stringification uses ":",
// not "=", so {"password": "hunter2"} never trips a "password=" marker).
var secretKeyNames = []string{
	"password", "passwd", "token", "secret", "apikey", "api_key", "api-key",
	"authorization", "cookie", "x-api-key", "x-subscription-token",
}

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
	"line":    true, // Lane B: one harvestpy stderr line (Process.Stderr), added only when comp=harvestpy logs at DEBUG
	"class":   true, // harvestpy.stderr's default-level stand-in for line: its bounded leading word
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

// scrubValue refuses a credential-looking value and caps the rest. A string
// is checked directly; an Any-kind value (a slice, a map, an error, a
// fmt.Stringer) is walked by secretValue BEFORE it is flattened to text, so a
// secret nested inside it — by shape or by its own key's name — is caught
// even when the flattened text would not show it.
func scrubValue(value slog.Value) slog.Value {
	resolved := value.Resolve()
	switch resolved.Kind() {
	case slog.KindString:
		text := resolved.String()
		if textHasSecret(text) {
			return slog.StringValue(redactedValue)
		}
		return slog.StringValue(TruncateValue(text))
	case slog.KindAny:
		unwrapped := resolved.Any()
		if secretValue(reflect.ValueOf(unwrapped), 0) {
			return slog.StringValue(redactedValue)
		}
		return slog.StringValue(TruncateValue(fmt.Sprint(unwrapped)))
	default:
		return resolved
	}
}

// textHasSecret reports whether text itself, lowered, contains a marker
// substring or an anchored api-key-style query parameter, or carries a JWT
// (checked against the original, untruncated, original-case text — a JWT is
// case-sensitive).
func textHasSecret(text string) bool {
	lowered := strings.ToLower(text)
	for _, marker := range secretMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	if apiKeyQuery.MatchString(text) {
		return true
	}
	return jwtSegment.MatchString(text)
}

// secretKeyName reports whether name — a map key or, by extension, a field
// name — is itself secret-shaped: "password", "token", "secret" and the
// like, matched as a substring so "reset_token" also trips it.
func secretKeyName(name string) bool {
	lowered := strings.ToLower(name)
	for _, marker := range secretKeyNames {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// maxScrubDepth bounds secretValue's recursion so a self-referential or
// pathologically deep value cannot spin the handler rather than log it.
const maxScrubDepth = 6

// secretValue walks a reflected Any-kind value looking for a reason to
// refuse the whole field: a string (or Stringer/error's flattened text)
// matching textHasSecret, a slice/array element that does, or a map entry
// whose value does OR whose key is itself secret-shaped (§ secretKeyName) —
// the case flat-text scanning misses because Go stringifies a map as
// `map[password:hunter2]`, never `password=hunter2`.
//
// The error check runs BEFORE the pointer/interface dereference below it: an
// error's Error() method is almost always on the POINTER receiver, so
// checking only the dereferenced struct — which implements no interface at
// all — would silently miss it.
func secretValue(rv reflect.Value, depth int) bool {
	if depth > maxScrubDepth || !rv.IsValid() {
		return false
	}
	if rv.CanInterface() {
		if err, ok := rv.Interface().(error); ok {
			return secretError(err, depth)
		}
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return false
		}
		return secretValue(rv.Elem(), depth+1)
	case reflect.Map:
		for _, key := range rv.MapKeys() {
			if secretKeyName(fmt.Sprint(key.Interface())) {
				return true
			}
			if secretValue(rv.MapIndex(key), depth+1) {
				return true
			}
		}
		return false
	case reflect.Slice, reflect.Array:
		for index := range rv.Len() {
			if secretValue(rv.Index(index), depth+1) {
				return true
			}
		}
		return false
	case reflect.String:
		return textHasSecret(rv.String())
	default:
		if !rv.CanInterface() {
			return false
		}
		return textHasSecret(fmt.Sprint(rv.Interface()))
	}
}

// secretError walks a wrapped error's Unwrap chain, checking each level's
// OWN Error() text — not just the outermost, in case a custom Error()
// method does not include what %w folded in.
func secretError(err error, depth int) bool {
	for err != nil && depth <= maxScrubDepth {
		if textHasSecret(err.Error()) {
			return true
		}
		switch unwrapped := err.(type) {
		case interface{ Unwrap() error }:
			err = unwrapped.Unwrap()
		case interface{ Unwrap() []error }:
			for _, inner := range unwrapped.Unwrap() {
				if secretError(inner, depth+1) {
					return true
				}
			}
			return false
		default:
			return false
		}
		depth++
	}
	return false
}
