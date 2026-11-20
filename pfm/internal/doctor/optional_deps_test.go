package doctor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestPrintOptionalDepsDoctorNamesADependencyThatAppearedSinceInstall(t *testing.T) {
	cases := []struct {
		name         string
		record       string // "" = no record file
		lookupErr    error
		wantWarnings int
		want         string
	}{
		{
			name:         "no record",
			wantWarnings: 0,
			want:         "doctor: deps_since_install record=absent — no install has recorded the optional dependencies it found — fix: pfm install --yes\n",
		},
		{
			name:         "herdr appeared",
			record:       `{"checked":["herdr","uv"],"present":[]}`,
			wantWarnings: 1,
			want:         "doctor: deps_since_install WARN appeared since the last install, not yet wired: herdr — fix: pfm install --yes\n",
		},
		{
			name: "herdr already recorded", record: `{"checked":["herdr","uv"],"present":["herdr"]}`,
			want: "doctor: deps_since_install ok none appeared since the last install\n",
		},
		{
			name: "registry row newer than the record", record: `{"checked":["uv"],"present":[]}`,
			want: "doctor: deps_since_install ok none appeared since the last install\n",
		},
		{
			name: "lookup could not run beside one that appeared", record: `{"checked":["herdr"],"present":[]}`,
			lookupErr: errors.New("permission denied"), wantWarnings: 2,
			want: "doctor: deps_since_install WARN could not look: look up uv: permission denied\n" +
				"doctor: deps_since_install WARN appeared since the last install, not yet wired: herdr — fix: pfm install --yes\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.record != "" {
				path := filepath.Join(home, ".local", "share", "pfm", "install", "optional-deps.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(testCase.record), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &deps.FakeRunner{}
			runner.ScriptLookPath("herdr", "/fixture/bin/herdr", nil)
			if testCase.lookupErr != nil {
				runner.ScriptLookPath("uv", "", testCase.lookupErr)
			}
			var out bytes.Buffer
			warnings := printOptionalDepsDoctor(&out, home, runner)
			if warnings != testCase.wantWarnings || out.String() != testCase.want {
				t.Errorf(
					"warnings=%d output=%q\nwant %d %q",
					warnings,
					out.String(),
					testCase.wantWarnings,
					testCase.want,
				)
			}
		})
	}
}

func TestPrintOptionalDepsDoctorUnreadableRecordWarns(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".local", "share", "pfm", "install", "optional-deps.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	warnings := printOptionalDepsDoctor(&out, home, &deps.FakeRunner{})
	want := "doctor: deps_since_install WARN record unreadable: parse " + path + ": unexpected end of JSON input — fix: pfm install --yes\n"
	if warnings != 1 || out.String() != want {
		t.Errorf("warnings=%d output=%q\nwant 1 %q", warnings, out.String(), want)
	}
}
