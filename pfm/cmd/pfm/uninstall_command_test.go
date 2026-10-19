package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestUninstallExplicitLeaveVSCodeColors(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	for _, leave := range []bool{false, true} {
		called := false
		runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
			called = true
			if options.LeaveVSCodeColors != leave || options.Mode != installer.ModeUninstall {
				t.Fatalf("uninstall leave=%v mode=%v, want leave=%v", options.LeaveVSCodeColors, options.Mode, leave)
			}
			return installer.Report{}, nil
		}
		var args []string
		if leave {
			args = []string{"--leave-vscode-colors"}
		}
		var out, errOut bytes.Buffer
		runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
		if code := runUninstall(args, &out, &errOut, runtime); code != 0 || !called {
			t.Fatalf("uninstall code=%d called=%v stderr=%s", code, called, errOut.String())
		}
	}
}
