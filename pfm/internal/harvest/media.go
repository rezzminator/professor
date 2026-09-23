package harvest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Download retrieves a source's bytes, unparsed, through Retrieve's file
// policy — the download tool's path, for a file of any kind (a PDF, a zip, an
// image, audio), capped at harvest.maxDownloadBytes. It never converts.
func (h *Harvester) Download(ctx context.Context, source string) Result {
	if err := validateFetchURL(source, false); err != nil {
		return Result{Source: source, Error: err.Error(), ErrorKind: errorKindInvalid}
	}
	got, err := h.retrieveWith(ctx, retrieveRequest{
		target:  source,
		want:    WantFile,
		policy:  PolicyFile,
		options: FetchOptions{Refresh: true},
	})
	if err != nil {
		return fileFailure(source, kindFile, got, err)
	}
	result := got.Result
	result.HTTPStatus = got.Status
	return result
}

func (h *Harvester) binaryDirectOrClient() *http.Client {
	if h.binaryDirect != nil {
		return h.binaryDirect
	}
	return h.client
}

func (h *Harvester) binaryChromeOrChrome() *http.Client {
	if h.binaryChrome != nil {
		return h.binaryChrome
	}
	return h.chrome
}

func isImageKind(kind string) bool {
	switch kind {
	case kindJPG, kindPNG, kindGIF, kindWebP, kindBMP, kindTIFF, kindSVG, kindImage:
		return true
	}
	return false
}

func (h *Harvester) binaryCachePath(source string) (string, string) {
	for _, kind := range []string{kindJPG, kindPNG, kindGIF, kindWebP, kindBMP, kindTIFF, kindSVG, kindImage, kindZIP, kindTAR, kind7Z, kindRAR} {
		path := filepath.Join(h.options.CacheDir, CacheKey(source, kind))
		ext := filepath.Ext(path)
		bin := strings.TrimSuffix(path, ext)
		for _, candidateExt := range []string{extensionJPG, extensionPNG, extensionGIF, extensionWebP, extensionBMP, extensionTIFF, extensionSVG, extensionZIP, extensionTAR, extension7Z, extensionRAR} {
			candidate := bin + candidateExt
			if _, err := os.Stat(candidate); err == nil {
				return candidate, kind
			}
		}
	}
	return "", ""
}

// binaryPath is where the binary cache keeps source's bytes of kind.
func (h *Harvester) binaryPath(source, kind string) string {
	ext := filepath.Ext(strings.Split(strings.Split(source, "?")[0], "#")[0])
	if ext == "" || len(ext) > 5 {
		ext = map[string]string{kindJPG: extensionJPG, kindPNG: extensionPNG, kindGIF: extensionGIF, kindWebP: extensionWebP, kindBMP: extensionBMP, kindTIFF: extensionTIFF, kindSVG: extensionSVG, kindImage: extensionPNG, kindZIP: extensionZIP, kindTAR: extensionTAR, kind7Z: extension7Z, kindRAR: extensionRAR}[kind]
	}
	if ext == "" {
		ext = ".bin"
	}
	root := h.options.CacheDir
	if h.cache != nil {
		root = h.cache.root // a call with caller headers keeps its files in its own partition
	}
	base := filepath.Join(root, CacheKey(source, kind))
	return strings.TrimSuffix(base, filepath.Ext(base)) + ext
}
