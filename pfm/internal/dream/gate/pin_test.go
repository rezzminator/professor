package gate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPinAcceptsCanonicalSortedUniqueAbsolutePaths(t *testing.T) {
	raw := []byte("/corpus/a.jsonl\n/corpus/b.jsonl\n")
	digest := sha256.Sum256(raw)
	pin, err := Pin(raw, []byte(hex.EncodeToString(digest[:])+"\n"))
	if err != nil {
		t.Fatalf("Pin() error = %v", err)
	}
	if pin.Digest != hex.EncodeToString(digest[:]) || len(pin.Paths) != 2 || !bytes.Equal(pin.Raw, raw) {
		t.Fatalf("Pin() = %#v", pin)
	}
}

func TestPinFailsClosed(t *testing.T) {
	valid := []byte("/a\n/b\n")
	digest := sha256.Sum256(valid)
	validPin := []byte(hex.EncodeToString(digest[:]) + "\n")
	tests := []struct {
		name  string
		paths []byte
		pin   []byte
		want  string
	}{
		{"malformed pin", valid, []byte("bad\n"), "not one SHA-256"},
		{"extra pin line", valid, append(append([]byte(nil), validPin...), 'x', '\n'), "not one SHA-256"},
		{"empty paths", nil, validPin, "empty"},
		{"relative path", []byte("a\n"), validPin, "blank, relative, or control-character"},
		{"control path", []byte("/a\tbad\n"), validPin, "blank, relative, or control-character"},
		{"unsorted", []byte("/b\n/a\n"), validPin, "not sorted and unique"},
		{"duplicate", []byte("/a\n/a\n"), validPin, "not sorted and unique"},
		{"digest mismatch", []byte("/a\n/c\n"), validPin, "path pin mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Pin(test.paths, test.pin)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Pin() error = %v, want %q", err, test.want)
			}
		})
	}
}
