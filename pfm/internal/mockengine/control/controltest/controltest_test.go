package controltest

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestCommandsReturnOnceTheMockAcks: each call writes its command line and returns what the mock acked
// for it, a stand-in mock acking as the mock engine does.
func TestCommandsReturnOnceTheMockAcks(t *testing.T) {
	fifo := FIFO(t, filepath.Join(t.TempDir(), "control"))
	reader, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	acks := map[string]string{"next": "next 300ms", "advance 1.5s": "advance 1.5s", "nudge": "nudge 0s"}
	received := make(chan string, len(acks))
	go func() {
		lines := bufio.NewScanner(reader)
		for lines.Scan() {
			received <- lines.Text()
			file, err := os.OpenFile(control.LogPath(fifo), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				t.Errorf("open the control log of %s: %v", fifo, err)
				return
			}
			_, _ = file.WriteString(acks[lines.Text()] + "\n")
			_ = file.Close()
		}
	}()
	if moved := Next(t, fifo); moved != 300*time.Millisecond {
		t.Fatalf("Next = %s, want the acked 300ms", moved)
	}
	AdvanceBy(t, fifo, 1500*time.Millisecond)
	Nudge(t, fifo)
	for _, want := range []string{"next", "advance 1.5s", "nudge"} {
		if got, _ := testwait.Receive(t, "the stand-in mock to read "+want, received); got != want {
			t.Fatalf("the mock read %q, want %q", got, want)
		}
	}
}

// TestReleaseLetsABlockedScriptGoOn: a reader blocked opening the FIFO, as `read -r _ < fifo` is, reads
// Release's line.
func TestReleaseLetsABlockedScriptGoOn(t *testing.T) {
	fifo := FIFO(t, filepath.Join(t.TempDir(), "hold"))
	read := make(chan string, 1)
	go func() {
		file, err := os.Open(fifo)
		if err != nil {
			read <- err.Error()
			return
		}
		defer func() { _ = file.Close() }()
		line, _ := bufio.NewReader(file).ReadString('\n')
		read <- line
	}()
	Release(t, fifo)
	if got, _ := testwait.Receive(t, "the blocked reader to read the release", read); got != "go\n" {
		t.Fatalf("the blocked reader read %q, want %q", got, "go\n")
	}
}
