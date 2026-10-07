package config

import (
	"fmt"
	"regexp"
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
}

type rawDoctor struct {
	IgnoreWarnings *[]string `json:"ignoreWarnings,omitempty"`
}

// applyDoctor validates the doctor block at load: a malformed ID is a config
// error naming its index, never an entry quietly dropped.
func applyDoctor(result *Config, raw *rawDoctor) error {
	if raw == nil || raw.IgnoreWarnings == nil {
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
