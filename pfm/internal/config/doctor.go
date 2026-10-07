package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// keyDoctorIgnoreWarnings is the machine-config key listing the doctor
// warnings an operator has chosen to silence.
const keyDoctorIgnoreWarnings = "doctor.ignoreWarnings"

// warningIDPattern is the shape of a doctor warning ID: lowercase letters and
// digits joined by single hyphens, such as "vscode-link".
var warningIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Doctor is the machine config's doctor block.
type Doctor struct {
	// IgnoreWarnings is doctor.ignoreWarnings: the warning IDs `pfm doctor`
	// prints as IGNORED and leaves out of its warning tally, the way a
	// linter's ignore comment names the rule it silences. Load checks each
	// entry's shape; whether a well-formed ID names a real warning is
	// doctor's to answer, since only doctor knows its IDs, and an unknown
	// one is a doctor warning, so a typo silences nothing.
	IgnoreWarnings []string
	// AcceptedMCP records exact external declarations the operator keeps in place.
	AcceptedMCP []AcceptedMCP
}

// AcceptedMCP is one deliberately retained third-party MCP declaration.
type AcceptedMCP struct {
	Path   string `json:"path"`
	Server string `json:"server"`
	Reason string `json:"reason"`
}

const keyDoctorAcceptedMCP = "doctor.acceptedMCP"

type rawDoctor struct {
	AcceptedMCP    *[]AcceptedMCP `json:"acceptedMCP,omitempty"`
	IgnoreWarnings *[]string      `json:"ignoreWarnings,omitempty"`
}

// applyDoctor validates the doctor block at load: a malformed ID is a config
// error naming its index, never an entry quietly dropped.
func applyDoctor(result *Config, raw *rawDoctor) error {
	if raw == nil {
		return nil
	}
	if raw.AcceptedMCP != nil {
		seen := map[string]bool{}
		for index, item := range *raw.AcceptedMCP {
			field := ""
			switch {
			case !filepath.IsAbs(item.Path) || filepath.Clean(item.Path) != item.Path:
				field = "path must be a clean absolute path"
			case strings.TrimSpace(item.Server) == "" || strings.TrimSpace(item.Server) != item.Server:
				field = "server must be nonempty and unpadded"
			case strings.TrimSpace(item.Reason) == "":
				field = "reason must be nonempty"
			case seen[item.Path+"\x00"+item.Server]:
				field = "duplicate path and server"
			}
			if field != "" {
				return fmt.Errorf("config %s: %s[%d] %s", result.Path, keyDoctorAcceptedMCP, index, field)
			}
			seen[item.Path+"\x00"+item.Server] = true
		}
		result.Doctor.AcceptedMCP = append([]AcceptedMCP{}, (*raw.AcceptedMCP)...)
		result.Sources[keyDoctorAcceptedMCP] = SourceFile
	}
	if raw.IgnoreWarnings == nil {
		return nil
	}
	ids := make([]string, 0, len(*raw.IgnoreWarnings))
	for index, id := range *raw.IgnoreWarnings {
		if !warningIDPattern.MatchString(id) {
			return fmt.Errorf(
				"config %s: %s[%d] must be a doctor warning id such as %q (lowercase letters and digits joined by single hyphens), got %q",
				result.Path,
				keyDoctorIgnoreWarnings,
				index,
				"vscode-link",
				id,
			)
		}
		ids = append(ids, id)
	}
	result.Doctor.IgnoreWarnings = ids
	result.Sources[keyDoctorIgnoreWarnings] = SourceFile
	return nil
}

func marshalDoctor(config Config) map[string]any {
	value := map[string]any{}
	if len(config.Doctor.IgnoreWarnings) != 0 || config.Source(keyDoctorIgnoreWarnings) == SourceFile {
		ids := config.Doctor.IgnoreWarnings
		if ids == nil {
			ids = []string{}
		}
		value["ignoreWarnings"] = ids
	}
	if len(config.Doctor.AcceptedMCP) != 0 || config.Source(keyDoctorAcceptedMCP) == SourceFile {
		items := config.Doctor.AcceptedMCP
		if items == nil {
			items = []AcceptedMCP{}
		}
		value["acceptedMCP"] = items
	}
	return value
}
