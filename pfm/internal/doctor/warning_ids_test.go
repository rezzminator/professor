package doctor

import (
	"bytes"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestWarningFilterHintsIgnoresAndRejectsUnknownIDs pins the
// doctor.ignoreWarnings rule every ID-carrying warning prints through: an
// un-ignored warning ends with the question-form hint naming its ID, the key
// and the resolved config path (or says no path resolved), and counts one;
// an ignored one prints its row as IGNORED and counts zero; an entry naming
// no warning is itself a warning row and ignores nothing.
func TestWarningFilterHintsIgnoresAndRejectsUnknownIDs(t *testing.T) {
	const row, configPath = "doctor: vscode product=/p link=MISSING", "/cfg/pfm.config.json"
	hint := ` · don't want this? add "vscode-link" to doctor.ignoreWarnings in `
	for _, test := range []struct {
		name          string
		machine       config.Config
		fix           string
		wantFilterOut string
		wantUnknown   int
		wantOut       string
		wantCount     int
	}{
		{
			name:      "config absent names no invented path",
			machine:   config.Config{},
			fix:       "run pfm install --yes",
			wantOut:   row + " — run pfm install --yes" + hint + "the machine config (no config path resolved — set " + paths.EnvConfig + ")\n",
			wantCount: 1,
		},
		{
			name:      "empty list hints at the resolved path",
			machine:   config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{}}},
			fix:       "run pfm install --yes",
			wantOut:   row + " — run pfm install --yes" + hint + configPath + "\n",
			wantCount: 1,
		},
		{
			name:      "a warning with no fix still hints",
			machine:   config.Config{Path: configPath},
			wantOut:   row + hint + configPath + "\n",
			wantCount: 1,
		},
		{
			name:      "ignored prints IGNORED and is not counted",
			machine:   config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{"vscode-link"}}},
			fix:       "run pfm install --yes",
			wantOut:   row + " IGNORED (doctor.ignoreWarnings)\n",
			wantCount: 0,
		},
		{
			name:      "another ignored id leaves this one counted",
			machine:   config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{"vscode-index"}}},
			wantOut:   row + hint + configPath + "\n",
			wantCount: 1,
		},
		{
			name:    "unknown id is a warning and silences nothing",
			machine: config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{"vscode-lnk"}}},
			wantFilterOut: `doctor: config doctor.ignoreWarnings has unknown warning id "vscode-lnk" in ` + configPath +
				" — it silences nothing; known ids: vscode-index, vscode-inspect, vscode-link, vscode-settings\n",
			wantUnknown: 1,
			wantOut:     row + hint + configPath + "\n",
			wantCount:   1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var filterOut, out bytes.Buffer
			filter, unknown := newWarningFilter(&filterOut, test.machine)
			if filterOut.String() != test.wantFilterOut || unknown != test.wantUnknown {
				t.Fatalf(
					"filter rows %q (%d), want %q (%d)",
					filterOut.String(),
					unknown,
					test.wantFilterOut,
					test.wantUnknown,
				)
			}
			if count := filter.warn(
				&out,
				warnVSCodeLink,
				row,
				test.fix,
			); count != test.wantCount ||
				out.String() != test.wantOut {
				t.Fatalf(
					"warn printed %q counting %d, want %q counting %d",
					out.String(),
					count,
					test.wantOut,
					test.wantCount,
				)
			}
		})
	}
}
