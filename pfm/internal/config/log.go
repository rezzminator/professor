package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Log is the activity log's machine policy: the level
// internal/obs writes at when the build's own default is not wanted, one
// level per component that wants its own, and the size- and time-capped
// rotation it keeps per pfm home.
//
// Level is empty when the file says nothing, which means "let the build
// decide": a -alpha VERSION logs at debug, a release at info. A stored level
// is canonical (`warning` in the file is `warn` here). KeepDays 0 disables
// the time limit.
type Log struct {
	Level      string
	Components map[string]string
	KeepFiles  int
	MaxMB      int
	KeepDays   int
}

// LogLevels are the level names log.level and log.components accept — the one
// list internal/obs parses, so no surface accepts what another refuses.
var LogLevels = obs.LevelNames

// InstallLogLevel is the level `pfm install` and a fresh `pfm config init` pin
// into the file: one line per action plus every warning and error, whatever
// the build. A file that carries it never takes the -alpha debug default.
const InstallLogLevel = "info"

// DefaultLog is the activity log policy when the file says nothing.
func DefaultLog() Log {
	// internal/obs owns the rotation and falls back to the same numbers for
	// a zero-valued Log, so there is exactly one default per knob.
	return Log{KeepFiles: obs.DefaultKeepFiles, MaxMB: obs.DefaultMaxMB, KeepDays: obs.DefaultKeepDays}
}

// InstallLogBlock is the `log` block `pfm install` writes into a
// pfm.config.json that has none: the switch, visible where an adopter would
// look for it. It never replaces a block that exists (LogDefaultInsertion).
func InstallLogBlock() map[string]any {
	return map[string]any{"level": InstallLogLevel, "keepDays": obs.DefaultKeepDays}
}

// MarshalLog renders a resolved Log as its file block, the shape Marshal emits
// for every other section. An unset level is left out: the file spelling of
// "the build decides" is no key at all.
func MarshalLog(log Log) map[string]any {
	value := map[string]any{"keepFiles": log.KeepFiles, "maxMB": log.MaxMB, "keepDays": log.KeepDays}
	if log.Level != "" {
		value["level"] = log.Level
	}
	if len(log.Components) != 0 {
		value["components"] = log.Components
	}
	return value
}

// LogDefaultInsertion plans the installer's one write into pfm.config.json:
// when the file at path exists and has no `log` key, the returned content is
// the file with InstallLogBlock added and changed is true. A file carrying a
// `log` key — whatever it says — is never rewritten. An absent file is an
// error wrapping fs.ErrNotExist: it is nobody's to create here (`pfm config
// init` owns creation), and the caller says so. A file that cannot be read or
// parsed is an error too — never "nothing to do".
func LogDefaultInsertion(path string) (content []byte, changed bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read config %s for the log default: %w", path, err)
	}
	top := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, false, configJSONError(path, err)
	}
	if _, present := top["log"]; present {
		return nil, false, nil
	}
	block, err := json.Marshal(InstallLogBlock())
	if err != nil {
		return nil, false, fmt.Errorf("encode the log default: %w", err)
	}
	top["log"] = block
	content, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode config %s: %w", path, err)
	}
	return append(content, '\n'), true, nil
}

type rawLog struct {
	Level      *string           `json:"level,omitempty"`
	Components map[string]string `json:"components,omitempty"`
	KeepFiles  *int              `json:"keepFiles,omitempty"`
	MaxMB      *int              `json:"maxMB,omitempty"`
	KeepDays   *int              `json:"keepDays,omitempty"`
}

// applyLog overlays the log keys the file actually set onto result, the way
// applyCompactNudge overlays its own: a bad value is a refused config, never a
// silently substituted default, because a log nobody can size is a log nobody
// reads.
func applyLog(result *Config, raw *rawLog) error {
	if raw == nil {
		return nil
	}
	if raw.Level != nil {
		level, err := canonicalLogLevel(*raw.Level)
		if err != nil {
			return fmt.Errorf("config %s: log.level %w", result.Path, err)
		}
		result.Log.Level = level
		result.Sources["log.level"] = SourceFile
	}
	if len(raw.Components) != 0 {
		result.Log.Components = make(map[string]string, len(raw.Components))
		for name, value := range raw.Components {
			if !obs.KnownComponent(name) {
				return fmt.Errorf(
					"config %s: log.components: %q is not one of %s",
					result.Path, name, strings.Join(obs.Components, ", "),
				)
			}
			level, err := canonicalLogLevel(value)
			if err != nil {
				return fmt.Errorf("config %s: log.components.%s %w", result.Path, name, err)
			}
			result.Log.Components[name] = level
		}
		result.Sources["log.components"] = SourceFile
	}
	if raw.KeepFiles != nil {
		if *raw.KeepFiles < 1 || *raw.KeepFiles > 64 {
			return fmt.Errorf(
				"config %s: log.keepFiles must be 1..64 (files kept per pfm home), got %d",
				result.Path,
				*raw.KeepFiles,
			)
		}
		result.Log.KeepFiles = *raw.KeepFiles
		result.Sources["log.keepFiles"] = SourceFile
	}
	if raw.MaxMB != nil {
		if *raw.MaxMB < 1 || *raw.MaxMB > 1024 {
			return fmt.Errorf(
				"config %s: log.maxMB must be 1..1024 (megabytes per file), got %d",
				result.Path,
				*raw.MaxMB,
			)
		}
		result.Log.MaxMB = *raw.MaxMB
		result.Sources["log.maxMB"] = SourceFile
	}
	if raw.KeepDays != nil {
		if *raw.KeepDays < 0 || *raw.KeepDays > 3650 {
			return fmt.Errorf(
				"config %s: log.keepDays must be 0..3650 (days a rotated file is kept; 0 disables), got %d",
				result.Path,
				*raw.KeepDays,
			)
		}
		result.Log.KeepDays = *raw.KeepDays
		result.Sources["log.keepDays"] = SourceFile
	}
	return nil
}

// RedactSecrets preserves JSON shape while replacing secret-looking object
// fields. It is intentionally generic so future credentials are safe by
// default without another display-path audit.
func RedactSecrets(content []byte) []byte {
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return content
	}
	redactJSON(value)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return content
	}
	return encoded.Bytes()
}

func redactJSON(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "credential") ||
				strings.Contains(lower, "password") ||
				strings.Contains(lower, "passphrase") ||
				strings.Contains(lower, "apikey") {
				typed[key] = "<redacted>"
				continue
			}
			redactJSON(child)
		}
	case []any:
		for _, child := range typed {
			redactJSON(child)
		}
	}
}

// canonicalLogLevel reads one level the file set through the one parser and
// returns its canonical spelling. The empty string is accepted as-is: it is
// how the file says "let the build decide". The error is the tail of a
// "<key> must be one of …" message.
func canonicalLogLevel(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	level, err := obs.ParseLevel(value)
	if err != nil {
		return "", fmt.Errorf("must be one of %s, got %q", strings.Join(LogLevels, ", "), value)
	}
	return obs.LevelName(level), nil
}
