package reload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// holdByShield has herdr-shield's holder 5 run the seat host 90 as the pane's
// shell, its terminal's foreground group 300 running the engine.
func (f *reloadHerdrFixture) holdByShield() {
	zeros := strings.Repeat("0 ", 13)
	for pid, stat := range map[int]string{
		5:  "5 (herdr-shield) S 1 5 5 0 -1 " + zeros + "2 0\n",
		90: "90 (pfm) S 5 90 90 0 300 " + zeros + "9 0\n",
	} {
		dir := filepath.Join(f.proc, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	testjail.StageHerdrShield(f.t, testjail.HerdrShieldListing(5, 2, 90, 9, f.canonical, f.request.SocketPath), 0)
}

// shieldedProcess is pane.process_info of a pane herdr-shield holds: Herdr sees its
// attach client while the engine (pid, argv) runs in the foreground group 300
// of the holder's shell, the seat host 90; pid 0 leaves that group empty, as
// between the old engine's exit and the new one's start.
func (f *reloadHerdrFixture) shieldedProcess(pid int, argv []string) (any, string) {
	for _, gone := range []int{101, 202} {
		if err := os.RemoveAll(filepath.Join(f.proc, fmt.Sprint(gone))); err != nil {
			return nil, err.Error()
		}
	}
	attached := map[string]any{"type": "pane_process_info", "process_info": map[string]any{
		"pane_id": f.canonical, "shell_pid": 77,
		"foreground_processes": []any{map[string]any{"pid": 77, "name": "herdr-shield"}},
	}}
	if pid == 0 {
		return attached, ""
	}
	dir := filepath.Join(f.proc, fmt.Sprint(pid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err.Error()
	}
	stat := fmt.Sprintf("%d (claude) S 90 300 90 0 300 %s50 0\n", pid, strings.Repeat("0 ", 13))
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
		return nil, err.Error()
	}
	if err := os.WriteFile(
		filepath.Join(dir, "cmdline"),
		[]byte(strings.Join(argv, "\x00")+"\x00"),
		0o600,
	); err != nil {
		return nil, err.Error()
	}
	return attached, ""
}
