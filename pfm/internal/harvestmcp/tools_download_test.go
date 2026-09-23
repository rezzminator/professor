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
// unknown id is ResourceNotFound, and a blob over the cap is a named error.
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
	if !errors.As(err, &wire) || wire.Code != mcp.CodeResourceNotFound {
		t.Fatalf("unknown id error = %v, want ResourceNotFound", err)
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

// TestDownloadRoundTripsItsTypedOutput: a real call through the SDK returns
// structuredContent that validates; a local path is a named wrong-tool error.
func TestDownloadRoundTripsItsTypedOutput(t *testing.T) {
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{}))
	var out DownloadOutput
	callStructured(t, session, toolDownload, map[string]any{"sources": []string{"/tmp/x.zip"}}, &out)
	if len(out.Items) != 1 || !strings.Contains(out.Items[0].Error, "`parseLocalDocuments`") {
		t.Fatalf("download(local path) = %+v", out.Items)
	}
}
