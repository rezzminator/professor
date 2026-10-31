package testjail

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestFenceArgsDropHostPathFlags(t *testing.T) {
	got := fenceArgs([]string{
		"-test.run=^TestX$", "-test.testlogfile=/tmp/go-build1/testlog.txt", "-test.v=test2json",
		"-test.gocoverdir=/tmp/op/cover", "--test.coverprofile=c.out", "-test.timeout=10m0s", "-test.count=1",
	})
	want := []string{"-test.run=^TestX$", "-test.v=test2json", "-test.timeout=10m0s", "-test.count=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fenceArgs = %q, want %q", got, want)
	}
}

func TestShellQuoteSurvivesAShell(t *testing.T) {
	for _, s := range []string{"plain", "it's", `^Test(A|B)$`, "a b", `$HOME;rm -rf x`} {
		out, err := exec.Command("bash", "-c", "printf %s "+shellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Fatalf("shellQuote(%q) round-trips to %q (%v)", s, out, err)
		}
	}
}

// This file only runs inside the fence: being here proves the gate let it in.
func TestGateAdmitsOnlyTheFence(t *testing.T) {
	if !inFence() && !onHostedRunner() {
		t.Fatal("a test ran outside the fence and off a hosted runner: the gate failed")
	}
}
