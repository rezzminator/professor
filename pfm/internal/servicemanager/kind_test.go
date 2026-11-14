package servicemanager

import (
	"errors"
	"slices"
	"testing"
)

func TestScopeArgvLeavesAUserServiceOnlyUnderSystemd(t *testing.T) {
	argv := []string{"tmux", "new-session"}
	found := func(name string) (string, error) {
		if name != SystemdRunBinary {
			t.Fatalf("looked up %q, want %q", name, SystemdRunBinary)
		}
		return "/usr/bin/systemd-run", nil
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	inService := func(name string) string { return map[string]string{"INVOCATION_ID": "abc"}[name] }
	outside := func(string) string { return "" }
	cases := []struct {
		name     string
		kind     Kind
		getenv   func(string) string
		lookPath func(string) (string, error)
		want     []string
		wantErr  string
	}{
		{
			name: "systemd inside a service", kind: KindSystemd, getenv: inService, lookPath: found,
			want: []string{"/usr/bin/systemd-run", "--user", "--collect", "--scope", "--", "tmux", "new-session"},
		},
		{name: "systemd outside a service", kind: KindSystemd, getenv: outside, lookPath: missing, want: argv},
		{name: "launchd inside a service", kind: KindLaunchd, getenv: inService, lookPath: missing, want: argv},
		{
			name:     "systemd inside a service without systemd-run",
			kind:     KindSystemd,
			getenv:   inService,
			lookPath: missing,
			wantErr:  "running inside a systemd user service requires systemd-run to create a durable chat scope: not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ScopeArgv(tc.kind, tc.getenv, tc.lookPath, argv)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr || got != nil {
					t.Fatalf("ScopeArgv = %q, %v; want nil, %q", got, err, tc.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("ScopeArgv = %q, %v; want %q, nil", got, err, tc.want)
			}
		})
	}
}

func TestWithoutInvocationIDDropsOnlyTheMarker(t *testing.T) {
	got := WithoutInvocationID([]string{"A=1", "INVOCATION_ID=abc", "INVOCATION_IDX=2"})
	if want := []string{"A=1", "INVOCATION_IDX=2"}; !slices.Equal(got, want) {
		t.Fatalf("WithoutInvocationID = %q, want %q", got, want)
	}
}
