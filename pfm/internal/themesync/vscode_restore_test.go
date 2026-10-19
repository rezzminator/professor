package themesync

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVSCodePendingRestoresWithoutStateOrInstallation(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "vscode-applied.json")
	chrome := filepath.Join(dir, vscodeChromeRecordFile)
	write := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(global, `{"keys":{"titleBar.activeBackground":{},"[Fixture]":{"keys":{"window.activeBorder":{}}}}}`)
	write(
		chrome,
		`{"vscode-remote://ssh-remote+fixture/folder":{"keys":{"statusBar.border":{}}},"/gone.code-workspace":{"keys":{"window.inactiveBorder":{}}}}`,
	)
	pending, err := VSCodePendingRestores(dir)
	want := []VSCodeRestore{
		{
			Record: global,
			Target: "Global user settings",
			Keys:   []string{"[Fixture].window.activeBorder", "titleBar.activeBackground"},
		},
		{Record: chrome, Target: "/gone.code-workspace", Keys: []string{"window.inactiveBorder"}},
		{Record: chrome, Target: "vscode-remote://ssh-remote+fixture/folder", Keys: []string{"statusBar.border"}},
	}
	if err != nil || !reflect.DeepEqual(pending, want) || !RestorePending(dir) {
		t.Fatalf("pending = %+v, %v; want %+v and RestorePending true", pending, err, want)
	}
	write(global, `{"keys":{}}`)
	write(chrome, `{}`)
	if RestorePending(dir) {
		t.Fatal("empty records still require restoration")
	}
}

func TestVSCodePendingRestoresRejectsUnreadableRecords(t *testing.T) {
	for _, content := range []string{"{", "null", "[]", `{"keys":null}`, `{"keys":{"[Theme]":{"keys":null}}}`} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "vscode-applied.json")
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := VSCodePendingRestores(dir); err == nil || !strings.Contains(err.Error(), file) {
				t.Fatalf("bad recovery record read as absence: %v", err)
			}
			if !RestorePending(dir) {
				t.Fatal("bad recovery record did not keep restoration pending")
			}
		})
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "vscode-applied.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := VSCodePendingRestores(dir); err == nil {
		t.Fatal("directory read as absent ownership file")
	}
}

func TestOffRetainsGlobalOwnershipWithoutStateOrVSCode(t *testing.T) {
	h := newHarness(t)
	h.write(".local/state/pfm/theme/vscode-applied.json", `{"keys":{"window.activeBorder":{}}}`)
	report, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "vscode", VerdictPending, "Global user settings: window.activeBorder")
	if !RestorePending(h.stateDir) ||
		!strings.Contains(h.read(".local/state/pfm/theme/current.json"), `"enabled": false`) {
		t.Fatal("off lost deferred Global restore or did not disable the contract")
	}
}
