package update

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestUpdateCheckAlias(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		config     bool
		wantCode   int
		wantStderr string
		jsonOutput bool
	}{
		{
			name:       "report without baseline",
			wantCode:   3,
			wantStderr: "pfm update check: kept for older instructions; the current command is pfm doctor --project-updates\n",
			jsonOutput: true,
		},
		{
			name:       "stray positional",
			args:       []string{"check", "extra"},
			wantCode:   2,
			wantStderr: "usage: pfm update check [--root DIR] [--json]\n",
		},
		{
			name:       "config unreadable",
			args:       []string{"check"},
			config:     true,
			wantCode:   3,
			wantStderr: "pfm update check: config: ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := updateTestRuntime(t)
			runtimes := []pfmconfig.Runtime{runtime}
			args := tc.args
			if tc.jsonOutput {
				args = []string{"check", "--root", t.TempDir(), "--json"}
			}
			if tc.config {
				path := filepath.Join(t.TempDir(), "pfm.config.json")
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvConfig, path)
				runtimes = nil
			}
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr, runtimes...); code != tc.wantCode {
				t.Fatalf("Run() code = %d, want %d; stderr = %q", code, tc.wantCode, stderr.String())
			}
			if tc.config {
				if !strings.HasPrefix(stderr.String(), tc.wantStderr) {
					t.Fatalf("stderr = %q, want prefix %q", stderr.String(), tc.wantStderr)
				}
			} else if stderr.String() != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.wantStderr)
			}
			if !tc.jsonOutput {
				if stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want empty", stdout.String())
				}
				return
			}
			var report struct {
				Terminal string `json:"terminal"`
			}
			decoder := json.NewDecoder(&stdout)
			if err := decoder.Decode(&report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if !strings.HasPrefix(report.Terminal, "FAILED — .professor/baseline.json not found") {
				t.Fatalf("terminal = %q, want baseline failure", report.Terminal)
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("after one report: %v, want EOF", err)
			}
		})
	}
}
