package doctor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestDoctorReportsEachVSCodeProductLinkAndIndexState pins issue #24 9b: no
// doctor row covered the VS Code wiring at all, so 9a's link-without-index
// registration was invisible to `pfm doctor`. A jail ledger recording two
// product links — one whose product index already registers pfm's entry,
// one whose index is a bare empty array — must produce two rows and count
// exactly the MISSING one as a warning; a host with no ledger at all reports
// "not managed" and adds no warning.
func TestDoctorReportsEachVSCodeProductLinkAndIndexState(t *testing.T) {
	home := t.TempDir()
	managedRoot := filepath.Join(home, ".local", "share", "pfm", "install")
	source := filepath.Join(managedRoot, "vscode", "professor")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}

	rootRegistered := filepath.Join(home, "product-registered")
	rootMissing := filepath.Join(home, "product-missing")
	targetRegistered := filepath.Join(rootRegistered, "extensions", "professor")
	targetMissing := filepath.Join(rootMissing, "extensions", "professor")
	for _, target := range []string{targetRegistered, targetMissing} {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}

	registeredIndex := filepath.Join(filepath.Dir(targetRegistered), "extensions.json")
	entry := map[string]any{
		"identifier":       map[string]any{"id": "professor.professor"},
		"version":          "0.1.0",
		"relativeLocation": "professor",
		"location":         map[string]any{"$mid": 1, "path": targetRegistered, "scheme": "file"},
	}
	encodedIndex, err := json.Marshal([]map[string]any{entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registeredIndex, encodedIndex, 0o644); err != nil {
		t.Fatal(err)
	}
	missingIndex := filepath.Join(filepath.Dir(targetMissing), "extensions.json")
	if err := os.WriteFile(missingIndex, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	ledger := map[string]any{"version": 1, "extensions": []string{targetRegistered, targetMissing}}
	encodedLedger, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedRoot, "vscode-ownership.json"), encodedLedger, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	warnings := printVSCodeDoctor(&out, home, "", warningFilter{})
	output := out.String()
	rows := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "doctor: vscode product=") {
			rows++
		}
	}
	if rows != 2 {
		t.Fatalf("vscode product rows = %d, want 2:\n%s", rows, output)
	}
	if !strings.Contains(output, "product="+rootRegistered+" link=ok index=registered") {
		t.Fatalf("registered product row missing or wrong:\n%s", output)
	}
	if !strings.Contains(output, "product="+rootMissing+" link=ok index=MISSING") {
		t.Fatalf("missing-index product row missing or wrong:\n%s", output)
	}
	if warnings != 1 {
		t.Fatalf("warnings = %d, want 1 (only the MISSING product):\n%s", warnings, output)
	}

	var absent bytes.Buffer
	absentWarnings := printVSCodeDoctor(&absent, t.TempDir(), "", warningFilter{})
	if !strings.Contains(absent.String(), "doctor: vscode not managed (pfm install --vscode never ran)") {
		t.Fatalf("ledger-absent host did not report not-managed:\n%s", absent.String())
	}
	if absentWarnings != 0 {
		t.Fatalf("ledger-absent host warnings = %d, want 0", absentWarnings)
	}
}

func TestDoctorVSCodeClaudeEnvironment(t *testing.T) {
	const settingsHint = ` · don't want this? add "vscode-settings" to doctor.ignoreWarnings in /cfg/pfm.config.json` + "\n"
	for _, value := range []string{"", "other", "primary"} {
		t.Run(value, func(t *testing.T) {
			home := t.TempDir()
			settings := filepath.Join(home, "settings.json")
			raw := `{"claudeCode.environmentVariables":[{"name":"CLAUDE_CONFIG_DIR","value":"` + value + `"}]}`
			if err := os.WriteFile(settings, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(home, ".local", "share", "pfm", "install")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			ledger, _ := json.Marshal(
				map[string]any{
					"version": 1,
					"files": []map[string]any{
						{"path": settings, "platform": "linux", "envOwned": true, "envValue": value},
					},
				},
			)
			if err := os.WriteFile(filepath.Join(root, "vscode-ownership.json"), ledger, 0o600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			warnings := printVSCodeDoctor(&out, home, "primary", warningFilter{configPath: "/cfg/pfm.config.json"})
			want := 0
			line := ""
			switch value {
			case "":
				want = 1
				line = "doctor: vscode settings=" + settings + " CLAUDE_CONFIG_DIR missing — run pfm install --yes --vscode" + settingsHint
			case "other":
				want = 1
				line = "doctor: vscode settings=" + settings + " CLAUDE_CONFIG_DIR=other, want primary — run pfm install --yes --vscode" + settingsHint
			}
			if warnings != want || (line != "" && !strings.Contains(out.String(), line)) {
				t.Fatalf("warnings=%d got %q want %q", warnings, out.String(), line)
			}
		})
	}
}

// TestDoctorVSCodeWarningsCarryIDsAndHonourIgnoreWarnings drives the whole
// ignore path the doctor run takes (printIgnorableDoctor) over one jail
// host with a missing extension link (vscode-link) and a settings file whose
// CLAUDE_CONFIG_DIR is wrong (vscode-settings): with no config or an empty
// list every warning counts and ends with the hint naming its ID and the
// config path; an ignored ID prints its row as IGNORED and leaves the tally;
// an unknown ID is itself a warning and silences nothing.
func TestDoctorVSCodeWarningsCarryIDsAndHonourIgnoreWarnings(t *testing.T) {
	const configPath = "/cfg/pfm.config.json"
	linkRow := func(root string) string { return "doctor: vscode product=" + root + " link=MISSING" }
	settingsRow := func(path string) string {
		return "doctor: vscode settings=" + path + " CLAUDE_CONFIG_DIR=other, want primary"
	}
	hint := func(id, where string) string {
		return ` · don't want this? add "` + id + `" to doctor.ignoreWarnings in ` + where + "\n"
	}
	noPath := "the machine config (no config path resolved — set " + paths.EnvConfig + ")"
	for _, test := range []struct {
		name     string
		machine  config.Config
		warnings int
		want     func(root, settings string) []string
	}{
		{
			name:     "config absent",
			machine:  config.Config{},
			warnings: 2,
			want: func(root, settings string) []string {
				return []string{
					linkRow(root) + " — run pfm install --yes" + hint("vscode-link", noPath),
					settingsRow(settings) + " — run pfm install --yes --vscode" + hint("vscode-settings", noPath),
				}
			},
		},
		{
			name:     "empty list",
			machine:  config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{}}},
			warnings: 2,
			want: func(root, settings string) []string {
				return []string{
					linkRow(root) + " — run pfm install --yes" + hint("vscode-link", configPath),
					settingsRow(settings) + " — run pfm install --yes --vscode" + hint("vscode-settings", configPath),
				}
			},
		},
		{
			name:     "link ignored",
			machine:  config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{"vscode-link"}}},
			warnings: 1,
			want: func(root, settings string) []string {
				return []string{
					linkRow(root) + " IGNORED (doctor.ignoreWarnings)\n",
					settingsRow(settings) + " — run pfm install --yes --vscode" + hint("vscode-settings", configPath),
				}
			},
		},
		{
			name: "both ignored",
			machine: config.Config{
				Path:   configPath,
				Doctor: config.Doctor{IgnoreWarnings: []string{"vscode-settings", "vscode-link"}},
			},
			warnings: 0,
			want: func(root, settings string) []string {
				return []string{
					linkRow(root) + " IGNORED (doctor.ignoreWarnings)\n",
					settingsRow(settings) + " IGNORED (doctor.ignoreWarnings)\n",
				}
			},
		},
		{
			name:     "unknown id",
			machine:  config.Config{Path: configPath, Doctor: config.Doctor{IgnoreWarnings: []string{"vscode-lnk"}}},
			warnings: 3,
			want: func(root, settings string) []string {
				return []string{
					`doctor: config doctor.ignoreWarnings has unknown warning id "vscode-lnk" in ` + configPath +
						" — it silences nothing; known ids: vscode-index, vscode-inspect, vscode-link, vscode-settings\n",
					linkRow(root) + " — run pfm install --yes" + hint("vscode-link", configPath),
					settingsRow(settings) + " — run pfm install --yes --vscode" + hint("vscode-settings", configPath),
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "product")
			target := filepath.Join(root, "extensions", "professor")
			settings := filepath.Join(home, "settings.json")
			raw := `{"claudeCode.environmentVariables":[{"name":"CLAUDE_CONFIG_DIR","value":"other"}]}`
			if err := os.WriteFile(settings, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			managed := filepath.Join(home, ".local", "share", "pfm", "install")
			if err := os.MkdirAll(managed, 0o700); err != nil {
				t.Fatal(err)
			}
			ledger, err := json.Marshal(map[string]any{
				"version":    1,
				"extensions": []string{target},
				"files": []map[string]any{
					{"path": settings, "platform": "linux", "envOwned": true, "envValue": "other"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(managed, "vscode-ownership.json"), ledger, 0o600); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			warnings := printIgnorableDoctor(&out, test.machine, home, "primary")
			output := out.String()
			for _, line := range test.want(root, settings) {
				if !strings.Contains(output, line) {
					t.Fatalf("output lacks %q:\n%s", line, output)
				}
			}
			if warnings != test.warnings {
				t.Fatalf("warnings = %d, want %d:\n%s", warnings, test.warnings, output)
			}
		})
	}
}
