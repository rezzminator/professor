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
		return h.storeBinary(source, classifyKind(path, "", body), "local", body, refreshValue(refresh))
	}
	if err := assertFetchable(source, false); err != nil {
		return Result{Source: source, Error: err.Error()}
	}
	if !refreshValue(refresh) {
		if path, kind := h.binaryCachePath(source); path != "" {
			if body, err := os.ReadFile(path); err == nil {
				return Result{
					Source:      source,
					Kind:        kind,
					Path:        path,
					Method:      "cache",
					CacheStatus: "hit",
					Bytes:       int64(len(body)),
				}
			}
		}
	}
	for _, rung := range []struct {
		name   string
		client *http.Client
		ua     string
	}{
		{"direct", h.binaryDirectOrClient(), h.userAgent}, {"chrome-impersonation", h.binaryChromeOrChrome(), chromeUA},
	} {
		body, status, contentType, err := getBody(ctx, rung.client, source, rung.ua, maxImageBytes+1)
		if err != nil || status >= 400 || len(body) > maxImageBytes {
			continue
		}
		kind := classifyKind(source, contentType, body)
		if !isImageKind(kind) {
			continue
		}
		return h.storeBinary(source, kind, rung.name, body, refreshValue(refresh))
	}
	return Result{Source: source, Error: "image could not be downloaded"}
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
	case "jpg", "png", "gif", "webp", "bmp", "tiff", "svg", "image":
		return true
	}
	return false
}

func refreshValue(v []bool) bool { return len(v) > 0 && v[0] }

func (h *Harvester) binaryCachePath(source string) (string, string) {
	for _, kind := range []string{"jpg", "png", "gif", "webp", "bmp", "tiff", "svg", "image", "zip", "tar", "7z", "rar"} {
		path := filepath.Join(h.options.CacheDir, CacheKey(source, kind))
		ext := filepath.Ext(path)
		bin := strings.TrimSuffix(path, ext)
		for _, candidateExt := range []string{".jpg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".svg", ".zip", ".tar", ".7z", ".rar"} {
			candidate := bin + candidateExt
			if _, err := os.Stat(candidate); err == nil {
				return candidate, kind
			}
		}
	}
	return "", ""
}

func (h *Harvester) storeBinary(source, kind, method string, body []byte, refresh bool) (result Result) {
	ext := filepath.Ext(strings.Split(strings.Split(source, "?")[0], "#")[0])
	if ext == "" || len(ext) > 5 {
		ext = map[string]string{"jpg": ".jpg", "png": ".png", "gif": ".gif", "webp": ".webp", "bmp": ".bmp", "tiff": ".tiff", "svg": ".svg", "image": ".png", "zip": ".zip", "tar": ".tar", "7z": ".7z", "rar": ".rar"}[kind]
	}
	if ext == "" {
		ext = ".bin"
	}
	base := filepath.Join(h.options.CacheDir, CacheKey(source, kind))
	path := strings.TrimSuffix(base, filepath.Ext(base)) + ext
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
	status := "miss"
	if refresh {
		status = "refresh"
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
	if err := assertFetchable(source, false); err != nil {
		return "", Result{Source: source, Error: err.Error()}
	}
	if !refresh {
		if path, kind := h.binaryCachePath(source); path != "" {
			return path, Result{Source: source, Kind: kind, Path: path, Method: "cache", CacheStatus: "hit"}
		}
	}
	for _, rung := range []struct {
		name   string
		client *http.Client
		ua     string
	}{
		{"direct", h.binaryDirectOrClient(), h.userAgent}, {"chrome-impersonation", h.binaryChromeOrChrome(), chromeUA},
	} {
		body, status, contentType, err := getBody(ctx, rung.client, source, rung.ua, h.options.MaxBytes)
		if err != nil || status >= 400 {
			continue
		}
		kind := classifyKind(source, contentType, body)
		if kind != "zip" && kind != "tar" && kind != "7z" && kind != "rar" {
			continue
		}
		result := h.storeBinary(source, kind, rung.name, body, refresh)
		if result.Error == "" {
			return result.Path, result
		}
	}
	return "", Result{Source: source, Error: "archive could not be downloaded"}
}
