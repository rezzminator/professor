//go:build jail

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestWatchTransitionsAnswersAnUnknownSeatWithSeen(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "watch", "ghost", "--transitions"}, &stdout, &stderr)
	if code != codeDeadChat || stdout.String() != "SEEN ghost not-found\n" {
		t.Fatalf(
			"rc = %d stdout = %q stderr = %q, want rc %d and the SEEN row",
			code,
			stdout.String(),
			stderr.String(),
			codeDeadChat,
		)
	}
}

// TestWatchScanFailureIsAnErrorLineNeverAbsence: when the fleet scan cannot
// look, the watch says so on stdout, where a monitor reads, in both modes.
func TestWatchScanFailureIsAnErrorLineNeverAbsence(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
	for _, testCase := range []struct {
		name     string
		args     []string
		wantCode int
		want     string
	}{
		{"legacy", []string{"chat", "watch", "ghost"}, 2, "ERROR ghost inspect cache database {root}/not-a-directory/index.db: stat {root}/not-a-directory/index.db: not a directory\npfm chat: inspect cache database {root}/not-a-directory/index.db: stat {root}/not-a-directory/index.db: not a directory\n"},
		{"transitions", []string{"chat", "watch", "ghost", "--transitions"}, 1, "ERROR ghost inspect cache database {root}/not-a-directory/index.db: stat {root}/not-a-directory/index.db: not a directory\npfm chat watch: ghost: inspect cache database {root}/not-a-directory/index.db: stat {root}/not-a-directory/index.db: not a directory\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(testCase.args, &stdout, &stderr)
			if got := rooted(
				stdout.String()+stderr.String(),
				filepath.Dir(blocker),
			); code != testCase.wantCode ||
				got != testCase.want {
				t.Fatalf("rc = %d stdout+stderr = %q, want %q", code, got, testCase.want)
			}
		})
	}
}

// TestWatchPollBelowTheFloorRunsAtTheFloorWithOneWarning: a --poll under the
// 10-second minimum is not refused; the watch runs at the floor and says so
// once on stderr, leaving stdout to the event lines.
func TestWatchPollBelowTheFloorRunsAtTheFloorWithOneWarning(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	for _, poll := range []string{"3", "0", "-5"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"chat", "watch", "ghost", "--transitions", "--poll", poll}, &stdout, &stderr)
		want := "pfm chat watch: --poll " + poll + " is below the 10-second minimum; polling every 10 seconds\n"
		if code != codeDeadChat || stdout.String() != "SEEN ghost not-found\n" || stderr.String() != want {
			t.Fatalf("--poll %s: rc = %d stdout = %q stderr = %q, want rc %d, the SEEN row and stderr %q",
				poll, code, stdout.String(), stderr.String(), codeDeadChat, want)
		}
	}
}
