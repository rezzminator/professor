package themesync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestDetectAppearance(t *testing.T) {
	defaults := []string{"defaults", "read", "-g", "AppleInterfaceStyle"}
	cases := []struct {
		name    string
		result  deps.RunResult
		runErr  error
		want    Appearance
		wantErr string
	}{
		{name: "Dark is dark", result: deps.RunResult{Stdout: []byte("Dark\n")}, want: Dark},
		{
			name: "missing key is light",
			result: deps.RunResult{
				ExitCode: 1,
				Stderr: []byte(
					"The domain/default pair of (kCFPreferencesAnyApplication, AppleInterfaceStyle) does not exist\n",
				),
			},
			want: Light,
		},
		{
			name:    "any other failure is an error",
			result:  deps.RunResult{ExitCode: 1, Stderr: []byte("boom")},
			wantErr: "boom",
		},
		{name: "an unknown value is an error", result: deps.RunResult{Stdout: []byte("Sepia\n")}, wantErr: "Sepia"},
		{name: "a start failure is an error", runErr: errors.New("no such file"), wantErr: "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := new(deps.FakeRunner)
			runner.Script(defaults, tc.result, tc.runErr)
			got, err := DetectAppearance(context.Background(), runner)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("DetectAppearance = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
