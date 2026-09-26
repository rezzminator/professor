package harvestpy

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestProvisionDefaultSmokePreservesRunner proves both default smoke passes
// use the provisioner's injected Runner for their worker start. The first
// smoke judges the staged environment before publication; the second judges
// the same runtime after publication.
func TestProvisionDefaultSmokePreservesRunner(t *testing.T) {
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)

	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{}, deps.InteractiveScript{
		Pid:    9101,
		Stdin:  noCloseWriteCloser{Writer: requestWriter},
		Stdout: noCloseReadCloser{Reader: responseReader},
	})

	responsesDone := make(chan struct{})
	go func() {
		defer close(responsesDone)
		reader := bufio.NewReader(requestReader)
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			if _, err := io.WriteString(
				responseWriter,
				`{"ok":true,"imports":{},"conversion":{"ok":true}}`+"\n",
			); err != nil {
				return
			}
		}
	}()
	defer func() {
		_ = responseWriter.Close()
		_ = requestReader.Close()
		<-responsesDone
	}()

	result, err := provisionWithTargets(context.Background(), ProvisionOptions{
		Root: root, Cache: cache, Platform: platform,
		Runner: runner,
		Run:    fakeProvisionRun(t, false),
	}, targets)
	if err != nil {
		t.Fatalf("provision with default smoke failed: %v", err)
	}
	if result.Runtime.Runner != runner {
		t.Fatal("returned runtime lost the injected runner")
	}

	starts := runner.Starts()
	if len(starts) != 2 {
		t.Fatalf("worker starts = %d, want staged and post-publish default smokes", len(starts))
	}
	for index, start := range starts {
		if len(start.Argv) != 2 || !strings.HasSuffix(start.Argv[1], "/converter.py") {
			t.Errorf("worker start %d argv = %#v, want managed converter worker", index, start.Argv)
		}
	}
}

type noCloseWriteCloser struct {
	io.Writer
}

func (noCloseWriteCloser) Close() error { return nil }

type noCloseReadCloser struct {
	io.Reader
}

func (noCloseReadCloser) Close() error { return nil }
