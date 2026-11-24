package control

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestSendWritesOneLineOnlyToAHeldFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "control")
	if err := SendLine(fifo, Next); !errors.Is(err, ErrNoReader) {
		t.Fatalf("SendLine to the missing FIFO %s = %v, want ErrNoReader", fifo, err)
	}
	if err := Make(fifo); err != nil {
		t.Fatal(err)
	}
	if err := Make(fifo); err != nil {
		t.Fatalf("Make over the existing FIFO %s = %v, want it kept", fifo, err)
	}
	if err := SendLine(fifo, Next); !errors.Is(err, ErrNoReader) {
		t.Fatalf("SendLine to the unheld FIFO %s = %v, want ErrNoReader", fifo, err)
	}
	reader, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if err := SendLine(fifo, Advance+" 1s"); err != nil {
		t.Fatalf("SendLine to the held FIFO %s = %v", fifo, err)
	}
	got := make([]byte, 64)
	n, err := reader.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:n]) != "advance 1s\n" {
		t.Fatalf("the FIFO %s carried %q, want %q", fifo, got[:n], "advance 1s\n")
	}
}

func TestAcksReadsTheLogLineByLine(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "control")
	for _, row := range []struct {
		name, log string
		want      []string
	}{
		{name: "no log yet", want: nil},
		{name: "empty log", log: "", want: nil},
		{name: "two acks", log: "next 200ms\nnudge 0s\n", want: []string{"next 200ms", "nudge 0s"}},
	} {
		t.Run(row.name, func(t *testing.T) {
			if row.name != "no log yet" {
				if err := os.WriteFile(LogPath(fifo), []byte(row.log), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Acks(fifo)
			if err != nil || !reflect.DeepEqual(got, row.want) {
				t.Fatalf("Acks(%s) = %q, %v, want %q", LogPath(fifo), got, err, row.want)
			}
		})
	}
}
