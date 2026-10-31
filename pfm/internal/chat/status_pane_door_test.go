package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestCheckAwaitReadsTheVisibleScreenOnEveryHost: the startup-dialog check
// reads a tmux chat through the one pane door's visible-screen read — the
// frame Herdr's read has always been — never a history scroll that can still
// hold a dismissed dialog.
func TestCheckAwaitReadsTheVisibleScreenOnEveryHost(t *testing.T) {
	values := hungValues(t)
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	script := "#!/bin/sh\necho \"$*\" >> " + argv + "\n" +
		"printf '  Do you trust the files in this folder?\\n  1. Yes, I trust this folder\\n  2. No, exit\\n'\n"
	if err := testjail.WriteExecutable(filepath.Join(bin, "tmux"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+paths.OSEnv{}.Get("PATH"))
	chat := headless.Chat{
		Name: "fresh", ID: "sid-fresh", Engine: pfmengine.Claude, Live: true,
		Socket: "cc-fresh-1-1", Pane: "%3",
	}
	err := CheckAwait(context.Background(), chat, &config.Runtime{Paths: values})
	if !errors.Is(err, headless.ErrTrustDialog) {
		t.Fatalf("CheckAwait() = %v, want the trust dialog named", err)
	}
	read, readErr := os.ReadFile(argv)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(read)), "\n") {
		if strings.Contains(line, "capture-pane") && strings.Contains(line, " -S ") {
			t.Fatalf("CheckAwait() scrolled history instead of reading the screen: %q", line)
		}
	}
	if !strings.Contains(string(read), "capture-pane") {
		t.Fatalf("CheckAwait() never read the pane: %q", read)
	}
}
