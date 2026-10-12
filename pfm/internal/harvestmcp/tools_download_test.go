package harvestmcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestRemoteDownloadIsAResourceLinkServedOnlyThroughMCP: a downloaded zip is a
// resource_link on the remote server, resources/read returns its bytes, an
// unknown id is ResourceNotFound (-32602 invalid params, SEP-2164), and a blob
// over the cap is a named error.
func TestRemoteDownloadIsAResourceLinkServedOnlyThroughMCP(t *testing.T) {
	service := newTestService(t, Runtime{Remote: true, MaxResourceBytes: 1 << 20})
	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	writeTestZip(t, zipPath, map[string]string{"a.txt": "hi"})
	body, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	id := hex.EncodeToString(sum[:])
	item := service.downloadItem(context.Background(), "https://example.test/data/bundle.zip",
		harvest.Result{Source: "https://example.test/data/bundle.zip", Kind: "zip", Path: zipPath, Method: "direct"})
	if item.Path != "" || item.Resource == nil || item.Resource.URI != downloadURIPrefix+id ||
		item.Resource.Type != "resource_link" || item.Resource.Size != int64(len(body)) ||
		item.Resource.MIMEType != "application/zip" || item.SHA256 != id {
		t.Fatalf("remote download item = %+v resource=%+v", item, item.Resource)
	}
	session := connectHarvesterInProcess(t, service)
	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: item.Resource.URI})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Contents) != 1 || !bytes.Equal(read.Contents[0].Blob, body) {
		t.Fatalf("resources/read did not return the downloaded bytes")
	}
	_, err = session.ReadResource(
		context.Background(),
		&mcp.ReadResourceParams{URI: downloadURIPrefix + strings.Repeat("0", 64)},
	)
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) || wire.Code != jsonrpc.CodeInvalidParams || wire.Message != "Resource not found" {
		t.Fatalf("unknown id error = %v, want ResourceNotFound (-32602) with message Resource not found", err)
	}
	service.runtime.MaxResourceBytes = 8
	if _, err := session.ReadResource(
		context.Background(),
		&mcp.ReadResourceParams{URI: item.Resource.URI},
	); err == nil ||
		!strings.Contains(err.Error(), "harvest.maxResourceBytes") {
		t.Fatalf("over-cap read error = %v, want the named limit", err)
	}
	if over := service.downloadItem(context.Background(), "https://example.test/b.zip",
		harvest.Result{Kind: "zip", Path: zipPath}); !strings.Contains(over.Note, "harvest.maxResourceBytes") {
		t.Fatalf("over-cap download item note = %q, want the limit stated up front", over.Note)
	}
}

// TestDownloadFileAnswersAnItemBlockPerURL: each URL answers its own block,
// numbered over the call; a misplaced item names where it belongs.
func TestDownloadFileAnswersAnItemBlockPerURL(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	_, text := callText(t, session, toolDownloadFile, map[string]any{
		"urls": []string{"/tmp/x.zip", "10.1038/nature14539"},
	})
	if !strings.HasPrefix(
		text,
		"=== [1/2] /tmp/x.zip\nerror: this is a local path; harvester_download_file takes URLs — "+
			"read a local document with `harvester_read` (files).\n\n=== [2/2] 10.1038/nature14539\nerror: ",
	) ||
		!strings.Contains(text, "; read it with `harvester_read` (publications), or download the URL of its file.") {
		t.Fatalf("download_file(local path, DOI) = %q", text)
	}
}

// TestDownloadFileItemNamesItsRungVia: the rung that served a file is the
// item's via, never its method.
func TestDownloadFileItemNamesItsRungVia(t *testing.T) {
	service := newTestService(t, Runtime{})
	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	writeTestZip(t, zipPath, map[string]string{"a.txt": "hi"})
	item := service.downloadItem(context.Background(), "https://example.test/bundle.zip",
		harvest.Result{Kind: "zip", Path: zipPath, Method: "direct"})
	if text := renderDownload(
		1,
		1,
		item,
	); !strings.HasPrefix(
		text,
		"=== [1/1] https://example.test/bundle.zip\nkind: zip / ",
	) ||
		!strings.Contains(text, " / via: direct\npath: ") {
		t.Fatalf("download_file text = %q, want its header, via and path", text)
	}
}

// TestDownloadFileAllFailedIsAnError: a call whose every item failed answers
// isError true, so a caller gating on isError never reads a failed batch as a
// result; one item that downloaded keeps the batch a result.
func TestDownloadFileAllFailedIsAnError(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	result := callRaw(t, session, toolDownloadFile, map[string]any{
		"urls": []string{"/tmp/x.zip", "10.1038/nature14539"},
	})
	if !result.IsError || !strings.Contains(allText(result), "harvester_read") {
		t.Fatalf("download_file with every item failed: isError = %v, content %s", result.IsError, allText(result))
	}
	failed := DownloadItem{Source: "https://example.test/gone.zip", Error: "HTTP 404"}
	served := DownloadItem{Source: "https://example.test/bundle.zip", Kind: "zip", Path: "/tmp/bundle.zip"}
	if mixed := downloadResult([]DownloadItem{failed, served}); mixed.IsError {
		t.Fatalf("download_file with one item served: isError = true, content %s", allText(mixed))
	}
	if all := downloadResult([]DownloadItem{failed, failed}); !all.IsError {
		t.Fatalf("download_file with every item failed: isError = false, content %s", allText(all))
	}
}
