package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestHerdrIndicatorConfigCachesUnchangedBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	write := func(body string) {
		t.Helper()
		temp := path + ".next"
		if err := os.WriteFile(temp, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, path); err != nil {
			t.Fatal(err)
		}
	}
	loads := 0
	settings := herdrIndicatorConfig{path: path, load: func(path string) (pfmconfig.Runtime, error) {
		loads++
		var runtime pfmconfig.Runtime
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return runtime, nil
		}
		if err != nil {
			return runtime, err
		}
		err = json.Unmarshal(body, &runtime.Config)
		return runtime, err
	}}
	write(`{"chat":{"herdrTabStatus":"text"}}`)
	for tick := 0; tick < 20; tick++ {
		if mode, err := settings.mode(); err != nil || mode != "text" {
			t.Fatalf("mode: %q, %v", mode, err)
		}
	}
	if loads != 1 {
		t.Fatalf("unchanged ticks loaded %d bodies, want 1", loads)
	}
	write(`{"chat":{"herdrTabStatus":"emoji"}}`)
	if mode, err := settings.mode(); err != nil || mode != "emoji" {
		t.Fatalf("live mode edit: %q, %v", mode, err)
	}
	if loads != 2 {
		t.Fatalf("changed config loads=%d, want 2", loads)
	}
	write(`{broken`)
	for tick := 0; tick < 2; tick++ {
		if _, err := settings.mode(); err == nil {
			t.Fatal("malformed live config hid mode error")
		}
	}
	if loads != 3 {
		t.Fatalf("unchanged malformed config loaded %d bodies, want 3 total", loads)
	}
	write(`{"chat":{"herdrTabStatus":"off"}}`)
	if mode, err := settings.mode(); err != nil || mode != "off" {
		t.Fatalf("recovery: %q, %v", mode, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 2; tick++ {
		if _, err := settings.mode(); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 5 {
		t.Fatalf("missing config loaded %d bodies, want 5 total", loads)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.mode(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory config error=%v", err)
	}
}
