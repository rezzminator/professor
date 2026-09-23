package harvestpy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// downloadTestRunner scripts a worker that reads one request line, hands it to
// requests, and answers with reply.
func downloadTestRunner(t *testing.T, reply map[string]any, requests chan<- map[string]any) *deps.FakeRunner {
	t.Helper()
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	runner := &deps.FakeRunner{}
	runner.ScriptInteractive([]string{"fake-browser"}, deps.InteractiveScript{
		Pid:    7002,
		Stdin:  requestWriter,
		Stdout: responseReader,
	})
	go func() {
		line, err := bufio.NewReader(requestReader).ReadString('\n')
		if err != nil {
			close(requests)
			return
		}
		var request map[string]any
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			close(requests)
			return
		}
		requests <- request
		encoded, _ := json.Marshal(reply)
		_, _ = fmt.Fprintln(responseWriter, string(encoded))
		_ = responseWriter.Close()
	}()
	t.Cleanup(func() {
		_ = requestReader.Close()
		_ = requestWriter.Close()
		_ = responseReader.Close()
		_ = responseWriter.Close()
	})
	return runner
}

// TestBrowserDownloadRequestAndReply pins the download op on the wire: the
// path Go names, the cap and the Go-owned dial reach the worker; a finished
// download decodes; a named failure decodes to its reason, status and head.
func TestBrowserDownloadRequestAndReply(t *testing.T) {
	request := BrowserDownloadRequest{
		BrowserFetchRequest: BrowserFetchRequest{
			URL:               "https://publisher.example.test/paper.pdf",
			Proxy:             "http://127.0.0.1:4100",
			Headless:          true,
			HostResolverRules: "MAP publisher.example.test 203.0.113.7",
			TimeoutMS:         45000,
		},
		Path:     "/cache/.browser-download-1",
		MaxBytes: 1024,
	}
	requests := make(chan map[string]any, 1)
	worker := NewBrowserWorker(Runtime{
		Python: "fake-browser",
		Script: "script",
		Runner: downloadTestRunner(t, map[string]any{
			"ok": true, "bytes": 9, "content_type": "application/pdf",
			"final_url": "https://publisher.example.test/paper.pdf", "status": 200, "via": "download",
		}, requests),
	})
	got, err := worker.Download(context.Background(), request, func(string) error { return nil })
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	sent := <-requests
	for key, want := range map[string]any{
		"op": "download", "path": request.Path, "max_bytes": float64(1024),
		"proxy": request.Proxy, "host_resolver_rules": request.HostResolverRules, "headless": true,
	} {
		if sent[key] != want {
			t.Fatalf("request %s = %v, want %v (request %v)", key, sent[key], want, sent)
		}
	}
	if got.Bytes != 9 || got.ContentType != "application/pdf" || got.Status != 200 || got.Via != "download" ||
		got.FinalURL != "https://publisher.example.test/paper.pdf" {
		t.Fatalf("download reply misdecoded: %+v", got)
	}

	failing := NewBrowserWorker(Runtime{
		Python: "fake-browser",
		Script: "script",
		Runner: downloadTestRunner(t, map[string]any{
			"ok": false, "reason": "no-download", "status": 403, "head": "<html>Just a moment...</html>",
			"error": "the navigation showed a page and no download started",
		}, make(chan map[string]any, 1)),
	})
	_, err = failing.Download(context.Background(), request, func(string) error { return nil })
	var named *BrowserDownloadFailure
	if !errors.As(err, &named) || named.Reason != "no-download" || named.Status != 403 ||
		named.Head != "<html>Just a moment...</html>" {
		t.Fatalf("named failure misdecoded: %#v (%v)", named, err)
	}
}

// TestBrowserDownloadPythonSeam runs the download seam's pure cases with NO
// browser and NO patchright: the capped copy is byte-exact under the cap, a
// named too-large failure over it with nothing left behind, and a request
// without the Go-owned proxy launches nothing.
func TestBrowserDownloadPythonSeam(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("named gap: python3 is unavailable on this host; the browser download seam test did not run")
	}
	command := exec.Command(python, filepath.Join("assets", "browser", "browser_download_test.py"))
	command.Dir = assetDirForTest()
	command.Env = append(os.Environ(), "BROWSER_LIVE=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser download seam failed: %v\n%s", err, output)
	}
}
