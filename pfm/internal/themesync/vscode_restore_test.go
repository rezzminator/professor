package themesync

import (
	"context"
	"fmt"
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

// One target inside a record that cannot be parsed is pending as unreadable
// beside the targets that can; it never hides them.
func TestVSCodePendingRestoresListsAnUnreadableTargetBesideTheRest(t *testing.T) {
	dir := t.TempDir()
	chrome := filepath.Join(dir, vscodeChromeRecordFile)
	if err := os.WriteFile(
		chrome,
		[]byte(`{"/a.code-workspace":{"keys":null},"/b.code-workspace":{"keys":{"statusBar.border":{}}}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	pending, err := VSCodePendingRestores(dir)
	want := []VSCodeRestore{
		{Record: chrome, Target: "/a.code-workspace", Keys: []string{
			"(unreadable: parse " + chrome + " target /a.code-workspace: keys: expected an ownership object, got null)",
		}},
		{Record: chrome, Target: "/b.code-workspace", Keys: []string{"statusBar.border"}},
	}
	if err != nil || !reflect.DeepEqual(pending, want) || !RestorePending(dir) {
		t.Fatalf("pending = %+v, %v; want %+v and RestorePending true", pending, err, want)
	}
}

func TestVSCodePendingRestoresRejectsUnreadableRecords(t *testing.T) {
	for content, want := range map[string]string{"{": "parse VS Code recovery record {dir}/vscode-applied.json: unexpected end of JSON input", "null": "parse VS Code recovery record {dir}/vscode-applied.json: expected an ownership object, got null", "[]": "parse VS Code recovery record {dir}/vscode-applied.json: json: cannot unmarshal array into Go value of type map[string]jsontext.Value"} {
		t.Run(content, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "vscode-applied.json")
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := VSCodePendingRestores(dir)
			if got := strings.ReplaceAll(fmt.Sprint(err), dir, "{dir}"); got != want {
				t.Fatalf("VSCodePendingRestores = %q, want %q", got, want)
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
	_, err := VSCodePendingRestores(dir)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		dir,
		"{dir}",
	), "read VS Code recovery record {dir}/vscode-applied.json: read {dir}/vscode-applied.json: is a directory"; got != want {
		t.Fatalf("VSCodePendingRestores(directory) = %q, want %q", got, want)
	}
}

func TestOffRetainsGlobalOwnershipWithoutStateOrVSCode(t *testing.T) {
	h := newHarness(t)
	h.write(".local/state/pfm/theme/vscode-applied.json", `{"keys":{"window.activeBorder":{}}}`)
	report, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(
		t,
		report,
		"vscode",
		VerdictPending,
		"VS Code colours stay in {tmp}/.local/state/pfm/theme/vscode-applied.json target Global user settings: window.activeBorder until VS Code with the Professor extension consumes the disabled current.json",
	)
	if !RestorePending(h.stateDir) ||
		!strings.Contains(h.read(".local/state/pfm/theme/current.json"), `"enabled": false`) {
		t.Fatal("off lost deferred Global restore or did not disable the contract")
	}
}
