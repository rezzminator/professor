package doctor

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/index"
)

func TestDoctorEngineRosterMatrix(t *testing.T) {
	tests := []struct {
		name        string
		machine     pfmconfig.Config
		want        string
		wantWarning int
	}{
		{
			name:        "zero zero",
			machine:     pfmconfig.Config{},
			want:        "doctor: roster cc=0 cx=0 ox=0 default=none error=no engines configured: Claude roster empty; Codex roster empty; OpenCode store absent\n",
			wantWarning: 1,
		},
		{
			name:    "claude only",
			machine: pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1}}},
			want:    "doctor: roster cc=1 cx=0 ox=0 default=cc\n",
		},
		{
			name: "codex only",
			machine: pfmconfig.Config{
				Ask:           pfmconfig.AskConfig{Engine: pfmengine.Claude},
				CodexAccounts: []pfmconfig.CodexAccount{{ID: 1}},
			},
			want: "doctor: roster cc=0 cx=1 ox=0 default=cx\n",
		},
		{
			name: "both",
			machine: pfmconfig.Config{
				Ask:           pfmconfig.AskConfig{Engine: pfmengine.Codex},
				Accounts:      []pfmconfig.Account{{ID: 1}, {ID: 2}},
				CodexAccounts: []pfmconfig.CodexAccount{{ID: 1}},
			},
			want: "doctor: roster cc=2 cx=1 ox=0 default=cx\n",
		},
		{
			name: "opencode only",
			machine: pfmconfig.Config{
				OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1}},
			},
			want: "doctor: roster cc=0 cx=0 ox=1 default=ox\n",
		},
		{
			name: "opencode requested but store absent",
			machine: pfmconfig.Config{
				Ask: pfmconfig.AskConfig{Engine: pfmengine.OpenCode},
			},
			want:        "doctor: roster cc=0 cx=0 ox=0 default=none error=no engines configured: Claude roster empty; Codex roster empty; OpenCode store absent\n",
			wantWarning: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			warnings := printEngineDoctor(&stdout, test.machine)
			if stdout.String() != test.want || warnings != test.wantWarning {
				t.Fatalf(
					"printEngineDoctor() output=%q warnings=%d, want %q warnings=%d",
					stdout.String(),
					warnings,
					test.want,
					test.wantWarning,
				)
			}
			if test.wantWarning != 0 && !strings.Contains(stdout.String(), "error=") {
				t.Fatalf("zero-engine row hid its error: %q", stdout.String())
			}
		})
	}
}

func TestDoctorWarnsWhenANewEngineRegistersOnlySomeCapabilities(t *testing.T) {
	const helper = "PFM_DOCTOR_PARTIAL_ENGINE_HELPER"
	if os.Getenv(helper) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestDoctorWarnsWhenANewEngineRegistersOnlySomeCapabilities$")
		command.Env = append(os.Environ(), helper+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("partial-engine doctor proof failed: %v\n%s", err, output)
		}
		return
	}
	id := pfmengine.ID("zz")
	pfmengine.Register(pfmengine.Descriptor{
		ID: id, Name: "Zed", Short: "Zed", LongName: "zed", Binary: "zed",
		SocketPrefix: "zz-", RootEnv: "PFM_ZZ_ROOT",
		DefaultRoots: func(home string) []string { return []string{home} },
	})
	index.RegisterSource(id, fourthSource{})
	gather.RegisterMatcher(id, fourthMatcher{})
	var stdout bytes.Buffer
	warnings := PrintEngineCapabilities(&stdout, testDependencies())
	if warnings == 0 || !strings.Contains(stdout.String(), "zz=index,matcher") ||
		!strings.Contains(strings.ToLower(stdout.String()), "missing") {
		t.Fatalf("partial engine was silent: warnings=%d output=%q", warnings, stdout.String())
	}
}

func TestDoctorEngineCapabilities(t *testing.T) {
	var stdout bytes.Buffer
	if warnings := PrintEngineCapabilities(&stdout, testDependencies()); warnings != 0 {
		t.Fatalf("printEngineCapabilities() warnings=%d output=%q", warnings, stdout.String())
	}
	want := "doctor: engines cc=index,launcher,matcher,usage,headless,ask cx=index,launcher,matcher,usage,headless,ask ox=index,matcher\n"
	if stdout.String() != want {
		t.Fatalf("printEngineCapabilities()=%q, want %q", stdout.String(), want)
	}
}
