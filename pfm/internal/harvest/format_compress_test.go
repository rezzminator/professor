package harvest

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// bz2Document is "a bzip2-compressed document\n" compressed by Python's bz2
// module inside the fence (the Go stdlib reads bzip2 but cannot write it).
const bz2Document = "QlpoOTFBWSZTWa60UbkAAALZgAAQQAIQAD4j3hAgACKBhA09T1CmTEyDIwI6witGrCASzbHn5lSp+LuSKcKEhXWijcg="

// TestFormatCompressedDocumentIsUnpacked: a single compressed document is
// decompressed and routed by its inner bytes. Watched FAILING before
// format_compress.go: each was refused as an archive.
func TestFormatCompressedDocumentIsUnpacked(t *testing.T) {
	bz2, err := base64.StdEncoding.DecodeString(bz2Document)
	if err != nil {
		t.Fatal(err)
	}
	var zst bytes.Buffer
	encoder, err := zstd.NewWriter(&zst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write([]byte("a zstd-compressed document\n")); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"notes.md.gz":    formatGzip(t, []byte("a gzip-compressed document\n")),
		"notes.txt.bz2":  bz2,
		"notes.txt.zst":  zst.Bytes(),
		"config.yaml.gz": formatGzip(t, []byte("name: fleet\nsize: 3\n")),
	}
	h, root := formatLocal(t, tagStripConverter(), files)
	for name, want := range map[string]string{
		"notes.md.gz":    "a gzip-compressed document",
		"notes.txt.bz2":  "a bzip2-compressed document",
		"notes.txt.zst":  "a zstd-compressed document",
		"config.yaml.gz": "```yaml\nname: fleet",
	} {
		got := h.FetchPublic(context.Background(), filepath.Join(root, name), FetchOptions{Refresh: true})
		if got.Error != "" || !strings.Contains(got.Content, want) {
			t.Errorf("%s: want content %q, got Error=%q Content=%q", name, want, got.Error, got.Content)
		}
	}
}

// TestFormatDecompressionBombIsNamed: a compressed body that inflates past
// the cap is a named failure stating the cap, for a page and a local file.
// Watched FAILING before format_compress.go (an archive refusal, no cap).
func TestFormatDecompressionBombIsNamed(t *testing.T) {
	bomb := formatGzip(t, make([]byte, compressedDocumentCapForTest+1))
	h, root := formatLocal(t, tagStripConverter(), map[string][]byte{"bomb.txt.gz": bomb})
	got := h.FetchPublic(context.Background(), filepath.Join(root, "bomb.txt.gz"), FetchOptions{Refresh: true})
	if got.Content != "" || !strings.Contains(got.Error, "64 MiB") || got.ErrorKind != errorKindOversized {
		t.Errorf(
			"local bomb: want a named %s failure stating 64 MiB, got Error=%q ErrorKind=%q",
			errorKindOversized,
			got.Error,
			got.ErrorKind,
		)
	}
	refused, ok := pageBodyGuard("https://203.0.113.10/bomb.txt.gz", kindTAR, bomb, http.StatusOK)
	if !ok || !strings.Contains(refused.Error, "64 MiB") || !strings.Contains(refused.Error, "download") {
		t.Errorf("page bomb: want a named refusal stating 64 MiB, got ok=%v %q", ok, refused.Error)
	}
}

const compressedDocumentCapForTest = 64 << 20
