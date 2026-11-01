package testjail

import (
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

// This file only runs inside the fence: being here proves the gate let it in.
func TestGateAdmitsOnlyTheFence(t *testing.T) {
	if !inFence() && !onHostedRunner() {
		t.Fatal("a test ran outside the fence and off a hosted runner: the gate failed")
	}
}
