package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorSystemPromptRows(t *testing.T) {
	for _, test := range []struct {
		name, mode, want string
		// isDefault: the mode is the shipped default, no file setting it.
		built, clone, isDefault bool
		warnings                int
	}{
		{
			name: "professor with the prompt built", mode: config.SystemPromptProfessor, built: true,
			want: "doctor: system-prompt claude[1] mode=professor ok\n",
		},
		{
			name: "production with the prompt built", mode: config.SystemPromptProduction, built: true, warnings: 1,
			want: "doctor: system-prompt claude[1] WARN mode=production: fleet prompt built but not injected — " +
				"set claude.systemPrompt to professor (prompt {prompt})\n",
		},
		{
			name: "production without a clone", mode: config.SystemPromptProduction,
			want: "doctor: system-prompt claude[1] mode=production ok (no fleet prompt built)\n",
		},
		{
			name: "default professor without a clone", mode: config.SystemPromptProfessor, isDefault: true,
			want: "doctor: system-prompt claude[1] mode=production ok (default professor needs a Professor clone; " +
				"none recorded, so launches carry no fleet prompt)\n",
		},
		{
			name: "default professor with the prompt built", mode: config.SystemPromptProfessor, isDefault: true,
			built: true,
			want:  "doctor: system-prompt claude[1] mode=professor ok\n",
		},
		{
			name: "explicit professor without a clone", mode: config.SystemPromptProfessor, warnings: 1,
			want: "doctor: system-prompt claude[1] WARN mode=professor: no Professor clone recorded, so no fleet " +
				"prompt exists and every launch says so — fix: install from a Professor clone, or set " +
				"claude.systemPrompt to production\n",
		},
		{
			name: "professor with the clone but no prompt built", mode: config.SystemPromptProfessor, clone: true,
			warnings: 1,
			want: "doctor: system-prompt claude[1] WARN mode=professor: fleet prompt {prompt} not built, so every " +
				"launch says so — fix: run pfm install --yes from the Professor clone\n",
		},
		{
			name: "lean with the prompt built", mode: config.SystemPromptLean, built: true,
			want: "doctor: system-prompt claude[1] mode=lean ok\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			prompt := ""
			if test.built || test.clone {
				clone := t.TempDir()
				if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
					t.Fatal(err)
				}
				prompt = paths.GeneratedHarnessPromptIn(clone, pfmengine.Claude)
			}
			if test.built {
				if err := os.MkdirAll(filepath.Dir(prompt), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(prompt, []byte("fleet prompt\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			machine := config.Config{
				Claude:   config.ClaudePrefs{SystemPrompt: test.mode, SystemPromptDefault: test.isDefault},
				Accounts: []config.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}},
			}
			var output bytes.Buffer
			warnings := printSystemPromptDoctor(&output, home, machine)
			want := strings.ReplaceAll(test.want, "{prompt}", prompt)
			if got := output.String(); got != want || warnings != test.warnings {
				t.Fatalf("warnings=%d rows=%q, want %d %q", warnings, got, test.warnings, want)
			}
		})
	}
}
