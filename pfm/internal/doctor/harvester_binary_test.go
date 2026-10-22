package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHarvesterDoctorDelegatesOnlyThroughBinary(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		enabled, present, executable, skip bool
		exit                               int
		err                                error
		wantFailure                        int
		want                               string
	}{
		{name: "disabled"},
		{name: "missing", enabled: true, wantFailure: 1, want: "unavailable error="},
		{name: "not executable", enabled: true, present: true, wantFailure: 1, want: "not executable"},
		{name: "healthy", enabled: true, present: true, executable: true, want: "cache healthy"},
		{name: "conversion skipped", enabled: true, present: true, executable: true, skip: true, want: "cache healthy"},
		{name: "failed check", enabled: true, present: true, executable: true, exit: 1, wantFailure: 1, want: "failed exit=1"},
		{name: "failed execution", enabled: true, present: true, executable: true, err: errors.New("permission denied"), wantFailure: 1, want: "failed error=permission denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			binary := filepath.Join(home, ".local", "bin", "harvester")
			if tc.present {
				if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
					t.Fatal(err)
				}
				if tc.executable {
					if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(binary, []byte("binary"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runtime := config.Runtime{
				Paths: paths.Values{Home: home},
				Config: config.Config{
					MCPServers: map[string]config.MCPServer{config.MCPServerHarvester: {Enabled: tc.enabled}},
				},
			}
			runner := &deps.FakeRunner{}
			runner.Script(
				[]string{binary, "doctor"},
				deps.RunResult{Stdout: []byte("cache healthy\n"), Stderr: []byte("probe details\n"), ExitCode: tc.exit},
				tc.err,
			)
			var output strings.Builder
			failures := printHarvesterDoctor(context.Background(), &output, runtime, tc.skip, runner)
			if failures != tc.wantFailure || !strings.Contains(output.String(), tc.want) {
				t.Fatalf("failures=%d output=%s", failures, output.String())
			}
			calls := runner.Calls()
			if !tc.enabled {
				if output.Len() != 0 || len(calls) != 0 {
					t.Fatalf("disabled output=%s calls=%v", output.String(), calls)
				}
			} else if !strings.Contains(output.String(), "doctor: harvester\n") {
				t.Fatalf("missing heading: %s", output.String())
			}
			if tc.present && tc.executable {
				want := []string{binary, "doctor"}
				if tc.skip {
					want = append(want, "--skip-conversion")
				}
				if len(calls) != 1 || !slices.Equal(calls[0].Argv, want) ||
					!strings.Contains(output.String(), "probe details") {
					t.Fatalf("calls=%v output=%s", calls, output.String())
				}
			} else if len(calls) != 0 {
				t.Fatalf("invalid binary executed: %v", calls)
			}
			if tc.enabled && (!tc.present || !tc.executable) && !strings.Contains(output.String(), binary) {
				t.Fatalf("missing binary path: %s", output.String())
			}
		})
	}
}
