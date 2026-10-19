package installer

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestLookupDarwinLoginShell(t *testing.T) {
	checkDarwinLoginShellCases(t, lookupDarwinLoginShell)
}

func checkDarwinLoginShellCases(t *testing.T, lookup func(context.Context, deps.Runner, int) (string, error)) {
	for _, tc := range []struct {
		name, search, attribute, want, wantError string
		searchErr, readErr                       error
	}{
		{name: "native array", search: "fixture\t\tUniqueID = (\n    123\n)\n", attribute: "UserShell: /bin/zsh\n"},
		{
			name: "space in shell path", search: "fixture UniqueID = 123",
			attribute: "UserShell:\n /fixture/My Shell/zsh\n", want: "/fixture/My Shell/zsh",
		},
		{name: "wrong array uid", search: "fixture UniqueID = (\n 456\n)", wantError: "invalid record"},
		{name: "multi uid array", search: "fixture UniqueID = (\n 123,\n 456\n)", wantError: "invalid record"},
		{name: "login shell", search: "fixture\tUniqueID = 123\n", attribute: "UserShell: /bin/zsh\n"},
		{name: "search failed", searchErr: errors.New("directory unavailable"), wantError: "directory unavailable"},
		{name: "empty search", wantError: "invalid record"},
		{name: "wrong uid", search: "fixture UniqueID = 456", wantError: "invalid record"},
		{name: "ambiguous", search: "fixture UniqueID = 123\nother UniqueID = 123", wantError: "invalid record"},
		{name: "invalid name", search: ".. UniqueID = 123", wantError: "invalid record"},
		{name: "read failed", search: "fixture UniqueID = 123", readErr: errors.New("read denied"), wantError: "read denied"},
		{name: "missing attribute", search: "fixture UniqueID = 123", wantError: "invalid attribute"},
		{
			name: "wrong attribute", search: "fixture UniqueID = 123",
			attribute: "Shell: /bin/zsh", wantError: "invalid attribute",
		},
		{
			name: "multivalue", search: "fixture UniqueID = 123",
			attribute: "UserShell: /bin/zsh\n /bin/sh", wantError: "invalid attribute",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			runner.Script([]string{"dscl", "/Search", "-search", "/Users", "UniqueID", "123"}, deps.RunResult{
				Stdout: []byte(tc.search),
			}, tc.searchErr)
			runner.Script(
				[]string{"dscl", "/Search", "-read", path.Join("/Users", "fixture"), "UserShell"},
				deps.RunResult{
					Stdout: []byte(tc.attribute),
				},
				tc.readErr,
			)
			got, err := lookup(context.Background(), runner, 123)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("lookup = %q, %v; want error %q", got, err, tc.wantError)
				}
			} else {
				want := tc.want
				if want == "" {
					want = "/bin/zsh"
				}
				if err != nil || got != want {
					t.Fatalf("lookup = %q, %v; want login shell %q", got, err, want)
				}
			}
		})
	}
}
