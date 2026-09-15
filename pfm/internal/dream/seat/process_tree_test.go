package seat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestProcessTreeInspectionProvesRootAndEnumeratesDescendants(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 100, 1, []string{"/opt/node/bin/node", "/opt/node/bin/codex", "--strict-config"})
	writeProcessFixture(t, procRoot, 101, 100, []string{"codex"})
	writeProcessFixture(t, procRoot, 102, 101, []string{"helper"})
	writeProcessFixture(t, procRoot, 200, 1, []string{"unrelated"})

	verification, err := (ProcTree{Root: procRoot}).Inspect(context.Background(), 100)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !verification.RootReadable || verification.Root.PID != 100 ||
		verification.Root.Command != "node:codex" {
		t.Fatalf("root proof = %#v", verification)
	}
	if verification.ProcessesEnumerated != 4 || verification.RelationsEnumerated != 4 {
		t.Fatalf("enumeration proof = %#v", verification)
	}
	want := []ProcessRecord{
		{PID: 101, ParentPID: 100, StartTicks: 1010, Command: "codex"},
		{PID: 102, ParentPID: 101, StartTicks: 1020, Command: "helper"},
	}
	if !reflect.DeepEqual(verification.Descendants, want) {
		t.Fatalf("descendants = %#v, want %#v", verification.Descendants, want)
	}
}

func TestProcessTreeInspectionCannotRenderUnreadableRootAsClean(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 200, 1, []string{"unrelated"})

	verification, err := (ProcTree{Root: procRoot}).Inspect(context.Background(), 100)
	if err == nil || !strings.Contains(err.Error(), "pane root") {
		t.Fatalf("Inspect() error = %v, want unreadable pane root", err)
	}
	if verification.RootReadable {
		t.Fatalf("missing root rendered readable: %#v", verification)
	}
}

func TestProcessTreeEmptyDescendantsFailAfterEnumeration(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 100, 1, []string{"node", "/opt/bin/codex", "--strict-config"})

	verification, err := (ProcTree{Root: procRoot}).Inspect(context.Background(), 100)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	verification.PaneRootResolved = true
	if verification.ProcessesEnumerated != 1 || verification.RelationsEnumerated != 1 ||
		verification.Descendants == nil || len(verification.Descendants) != 0 {
		t.Fatalf("empty descendant proof = %#v", verification)
	}
	if err := validateProcessTreeVerification(verification); err == nil ||
		!strings.Contains(err.Error(), "no direct native Codex child") {
		t.Fatalf("enumerated root-only tree error = %v", err)
	}
}

func TestProcessTreeGateAllowsOnlyDirectNativeCodexBootstrap(t *testing.T) {
	verification := ProcessTreeVerification{
		PaneRootResolved:    true,
		PaneRootPID:         100,
		RootReadable:        true,
		Root:                ProcessRecord{PID: 100, ParentPID: 1, StartTicks: 1000, Command: "node:codex"},
		ProcessesEnumerated: 2,
		RelationsEnumerated: 2,
		Descendants: []ProcessRecord{
			{PID: 101, ParentPID: 100, StartTicks: 1010, Command: "codex"},
		},
	}
	if err := validateProcessTreeVerification(verification); err != nil {
		t.Fatalf("legitimate launcher -> native codex failed: %v", err)
	}
}

func TestProcessTreeGateRejectsNativeCodexMCPDescendants(t *testing.T) {
	verification := ProcessTreeVerification{
		PaneRootResolved:    true,
		PaneRootPID:         100,
		RootReadable:        true,
		Root:                ProcessRecord{PID: 100, ParentPID: 1, StartTicks: 1000, Command: "node:codex"},
		ProcessesEnumerated: 5,
		RelationsEnumerated: 5,
		Descendants: []ProcessRecord{
			{PID: 101, ParentPID: 100, StartTicks: 1010, Command: "codex"},
			{PID: 102, ParentPID: 101, StartTicks: 1020, Command: "npm"},
			{PID: 103, ParentPID: 102, StartTicks: 1030, Command: "sh"},
			{PID: 104, ParentPID: 103, StartTicks: 1040, Command: "node"},
		},
	}
	if err := validateProcessTreeVerification(verification); err == nil ||
		!strings.Contains(err.Error(), "external/MCP descendant") {
		t.Fatalf("validateProcessTreeVerification() error = %v", err)
	}
}

func TestProcessTreeGateRejectsLookalikePaneRoots(t *testing.T) {
	for _, command := range []string{
		"codex",
		"node",
		"sh",
	} {
		verification := ProcessTreeVerification{
			PaneRootResolved:    true,
			PaneRootPID:         100,
			RootReadable:        true,
			Root:                ProcessRecord{PID: 100, ParentPID: 1, StartTicks: 1000, Command: command},
			ProcessesEnumerated: 1,
			RelationsEnumerated: 1,
		}
		if err := validateProcessTreeVerification(verification); err == nil ||
			!strings.Contains(err.Error(), "not the Codex Node launcher") {
			t.Fatalf("command %q error = %v", command, err)
		}
	}
}

func TestProcessTreeSanitizesSecretsBeforeVerification(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 100, 1, []string{"node", "/opt/bin/codex", "--token=ROOT_SECRET"})
	writeProcessFixture(t, procRoot, 101, 100, []string{"/opt/bin/codex", "--token=NATIVE_SECRET"})
	writeProcessFixture(t, procRoot, 102, 101, []string{"npm", "--token=MCP_SECRET"})

	verification, err := (ProcTree{Root: procRoot}).Inspect(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Phase: "seat.process-tree.verified", ProcessTree: &verification}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("%#v\n%s", verification, encoded)
	for _, secret := range []string{"ROOT_SECRET", "NATIVE_SECRET", "MCP_SECRET"} {
		if strings.Contains(text, secret) {
			t.Fatalf("verification leaked %s: %s", secret, text)
		}
	}
	if verification.Root.Command != "node:codex" ||
		!reflect.DeepEqual(
			[]string{verification.Descendants[0].Command, verification.Descendants[1].Command},
			[]string{"codex", "npm"},
		) {
		t.Fatalf("sanitized command shapes = %#v", verification)
	}
}

func TestCommandShapeRequiresCodexAsTheNodeScriptArgument(t *testing.T) {
	for _, test := range []struct {
		argv []string
		want string
	}{
		{argv: []string{"/opt/bin/node", "/opt/bin/codex", "--token=secret"}, want: "node:codex"},
		{argv: []string{"/opt/bin/node", "/opt/bin/other", "/later/codex"}, want: "node"},
		{argv: []string{"/opt/bin/codex", "--token=secret"}, want: "codex"},
	} {
		if got := commandShape(test.argv); got != test.want {
			t.Fatalf("commandShape(%q) = %q, want %q", test.argv, got, test.want)
		}
	}
}

func TestProcessTreeRequiresInjectedAbsoluteProcRoot(t *testing.T) {
	for _, root := range []string{"", "relative/proc"} {
		if _, err := (ProcTree{Root: root}).Inspect(context.Background(), 100); err == nil ||
			!strings.Contains(err.Error(), "proc root") {
			t.Fatalf("ProcTree{Root:%q}.Inspect() error = %v", root, err)
		}
	}
}

func writeProcessFixture(t *testing.T, root string, pid, parent int, argv []string) {
	t.Helper()
	directory := filepath.Join(root, strconv.Itoa(pid))
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	fields := []string{"S", strconv.Itoa(parent)}
	for len(fields) <= 19 {
		fields = append(fields, "0")
	}
	fields[19] = strconv.Itoa(pid * 10)
	stat := strconv.Itoa(pid) + " (fixture process) " + strings.Join(fields, " ") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "cmdline"),
		[]byte(strings.Join(argv, "\x00")+"\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}
