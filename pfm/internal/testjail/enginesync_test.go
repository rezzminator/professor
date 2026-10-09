package testjail

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The package process runs under the default fake: a healthy engine-sync with
// nothing to do, answering each JSON shape the interface defines.
func TestDefaultEngineSyncAnswersEveryShape(t *testing.T) {
	binary := os.Getenv(envEngineSyncBin)
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"global", "--home", "/h", "--json"}, want: `{"ok":true,"events":[],"error":""}`},
		{args: []string{"doctor", "--machine", "--json"}, want: `{"ok":true,"rows":[]}`},
		{args: []string{"codex", "check", "/r", "--json"}, want: `"problems":[]`},
		{args: []string{"opencode", "build", "/r"}, want: "OPENCODE BUILD PASS"},
		{args: []string{"version"}, want: "engine-sync 0.0.0-testjail"},
	}
	for _, testCase := range cases {
		out, err := exec.Command(binary, testCase.args...).Output()
		if err != nil || !strings.Contains(string(out), testCase.want) {
			t.Fatalf("%s %q = %q, %v; want %q", binary, testCase.args, out, err, testCase.want)
		}
	}
}

func TestFakeEngineSyncRecordsEveryCall(t *testing.T) {
	record := FakeEngineSync(t, `{"ok":false}`, 1)
	binary := os.Getenv(envEngineSyncBin)
	for _, args := range [][]string{{"global", "--plan"}, {"global"}} {
		out, err := exec.Command(binary, args...).Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || strings.TrimSpace(string(out)) != `{"ok":false}` {
			t.Fatalf("%q = %q, %v; want the reply at exit 1", args, out, err)
		}
	}
	if got := EngineSyncCalls(t, record); strings.Join(got, "|") != "global --plan|global" {
		t.Fatalf("calls = %q", got)
	}
}

func TestMirrorBuilt(t *testing.T) {
	record := FakeEngineSync(t, CleanMirrorReply, 0)
	if MirrorBuilt(t, record, "codex", "/r") {
		t.Fatal("a fake that never ran reports a built mirror")
	}
	for _, args := range [][]string{{"codex", "check", "/r", "--json"}, {"codex", "build", "/r/child", "--json"}} {
		if err := exec.Command(os.Getenv(envEngineSyncBin), args...).Run(); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	if MirrorBuilt(t, record, "codex", "/r") {
		t.Fatal("a check, or a build of another directory, reports /r built")
	}
	if err := exec.Command(os.Getenv(envEngineSyncBin), "codex", "build", "/r", "--home", "/h", "--json").
		Run(); err != nil {
		t.Fatal(err)
	}
	if !MirrorBuilt(t, record, "codex", "/r") || MirrorBuilt(t, record, "opencode", "/r") {
		t.Fatal("the codex build of /r is not reported as exactly that")
	}
}
