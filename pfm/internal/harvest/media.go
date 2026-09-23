package harvest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FetchImage downloads one image without invoking the document converter.
// Images are immutable cache content and therefore do not expire.
func (h *Harvester) FetchImage(ctx context.Context, source string, refresh ...bool) Result {
	if isLocalSource(source) {
		path := source
		if strings.HasPrefix(strings.ToLower(path), "file://") {
			decoded, err := fileURLPath(path)
			if err != nil {
				return Result{Source: source, Error: err.Error()}
			}
			path = decoded
		}
		if reason := DenyLocalPath(path, h.options.LocalRoots); reason != "" {
			return Result{Source: source, Error: reason}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return Result{Source: source, Error: err.Error()}
		}
		if len(body) > maxImageBytes {
			return Result{Source: source, Error: "image exceeds 10 MiB limit"}
		}
		return h.storeBinary(source, classifyKind(path, "", body), localLabel, body, refreshValue(refresh))
	}
	if err := validateFetchURL(source, false); err != nil {
		return Result{Source: source, Error: err.Error()}
	}
	if !refreshValue(refresh) {
		if path, kind := h.binaryCachePath(source); path != "" {
			if body, err := os.ReadFile(path); err == nil {
				return Result{
					Source:      source,
					Kind:        kind,
					Path:        path,
					Method:      cacheLabel,
					CacheStatus: cacheStatusHit,
					Bytes:       int64(len(body)),
				}
			}
		}
	}
	got, err := h.retrieveWith(ctx, retrieveRequest{
		target:   source,
		want:     WantFile,
		policy:   PolicyFile,
		options:  FetchOptions{Refresh: refreshValue(refresh)},
		accept:   isImageKind,
		maxBytes: maxImageBytes,
	})
	if err != nil {
		return fileFailure(source, "image", got, err)
	}
	return got.Result
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

func refreshValue(v []bool) bool { return len(v) > 0 && v[0] }

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
	base := filepath.Join(h.options.CacheDir, CacheKey(source, kind))
	return strings.TrimSuffix(base, filepath.Ext(base)) + ext
}

func (h *Harvester) storeBinary(source, kind, method string, body []byte, refresh bool) (result Result) {
	path := h.binaryPath(source, kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Result{Source: source, Error: err.Error()}
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), ".harvest-bin-*")
	if e != nil {
		return Result{Source: source, Error: e.Error()}
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) && result.Error == "" {
			result = Result{
				Source: source,
				Kind:   kind,
				Error:  fmt.Sprintf("remove binary cache temp %s: %v", tmpName, err),
			}
		}
	}()
	if _, e = tmp.Write(body); e == nil {
		e = tmp.Chmod(0o600)
	}
	if closeErr := tmp.Close(); e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(tmpName, path)
	}
	if e != nil {
		return Result{Source: source, Kind: kind, Error: fmt.Sprintf("cache binary: %v", e)}
	}
	status := cacheStatusMiss
	if refresh {
		status = cacheStatusRefresh
	}
	return Result{Source: source, Kind: kind, Path: path, Method: method, CacheStatus: status, Bytes: int64(len(body))}
}

func (h *Harvester) fetchArchiveBytes(ctx context.Context, source string, refresh bool) (string, Result) {
	if isLocalSource(source) {
		path := source
		if strings.HasPrefix(strings.ToLower(path), "file://") {
			decoded, err := fileURLPath(path)
			if err != nil {
				return "", Result{Source: source, Error: err.Error()}
			}
			path = decoded
		}
		if reason := DenyLocalPath(path, h.options.LocalRoots); reason != "" {
			return "", Result{Source: source, Error: reason}
		}
		return path, Result{Source: source, Path: path}
	}
	if err := validateFetchURL(source, false); err != nil {
		return "", Result{Source: source, Error: err.Error()}
	}
	if !refresh {
		if path, kind := h.binaryCachePath(source); path != "" {
			return path, Result{Source: source, Kind: kind, Path: path, Method: cacheLabel, CacheStatus: cacheStatusHit}
		}
	}
	got, err := h.retrieveWith(ctx, retrieveRequest{
		target:  source,
		want:    WantFile,
		policy:  PolicyFile,
		options: FetchOptions{Refresh: refresh},
		accept: func(kind string) bool {
			return kind == kindZIP || kind == kindTAR || kind == kind7Z || kind == kindRAR
		},
	})
	if err != nil {
		return "", fileFailure(source, "archive", got, err)
	}
	return got.Result.Path, got.Result
}
