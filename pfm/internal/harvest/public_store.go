package harvest

// This file is the protocol boundary for results that leave the harvester
// process.  The transport and cache deliberately retain their provenance for
// diagnostics; this layer turns that private receipt into a separate,
// provenance-free artifact before an adapter can return it.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (h *Harvester) cacheRoot() (string, error) {
	if h == nil || strings.TrimSpace(h.options.CacheDir) == "" {
		return "", errors.New("harvester cache directory is unavailable")
	}
	root, err := filepath.Abs(filepath.Clean(h.options.CacheDir))
	if err != nil {
		return "", errors.New("harvester cache directory is unavailable")
	}
	return root, nil
}

func (h *Harvester) publicRoot() (string, error) {
	root, err := h.cacheRoot()
	if err != nil {
		return "", err
	}
	return publicNamespace(root, false)
}

// publicNamespace permits the configured cache root itself to be a symlink,
// but never permits the public namespace to be one. Otherwise public/ could
// silently point at .private/ and turn private cache files into public files.
func publicNamespace(root string, create bool) (string, error) {
	path := filepath.Join(root, "public")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return canonicalPublicPath(path)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return "", err
		}
		info, err = os.Lstat(path)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("public directory is not a safe directory")
	}
	if create {
		if err := os.Chmod(path, 0o700); err != nil {
			return "", err
		}
	}
	return canonicalPublicPath(path)
}

func ensureNamespaceDir(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return os.ErrNotExist
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("private namespace is not a safe directory")
	}
	if create {
		return os.Chmod(path, 0o700)
	}
	return nil
}

func (h *Harvester) publicArtifactPath(source, kind, oldPath, ext string) (string, error) {
	root, err := h.cacheRoot()
	if err != nil {
		return "", err
	}
	canonical := oldPath
	if oldPath != "" {
		if resolved, err := canonicalPublicPath(oldPath); err == nil {
			canonical = resolved
		}
	}
	key := sha256.Sum256([]byte("harvester-public\x00" + source + "\x00" + kind + "\x00" + canonical))
	return filepath.Join(root, "public", hex.EncodeToString(key[:])+ext), nil
}

func (h *Harvester) writePublicMarkdown(path, body string, fetchedAt ...string) error {
	stamp := time.Now().UTC().Format(time.RFC3339)
	if len(fetchedAt) > 0 {
		candidate := strings.TrimSpace(fetchedAt[0])
		if _, err := time.Parse(time.RFC3339, candidate); err == nil {
			stamp = candidate
		}
	}
	meta := "---\nfetched_at: " + stamp + "\ntoken_count: " + fmt.Sprint(
		estimateTokens(body),
	) + "\nsource: harvester\n---\n\n"
	return h.writePublicFile(path, []byte(meta+body))
}

func (h *Harvester) writePublicFile(path string, data []byte) error {
	root, err := h.cacheRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	publicRoot := filepath.Join(root, "public")
	if _, err := publicNamespace(root, true); err != nil {
		return fmt.Errorf("create public directory: %w", err)
	}
	if symlinked, symlinkErr := symlinkBelow(path, publicRoot); symlinkErr != nil || symlinked {
		return errors.New("public artifact path is not a safe namespace")
	}
	canonicalPublic, err := canonicalPublicPath(publicRoot)
	if err != nil {
		return errors.New("public directory cannot be resolved")
	}
	canonicalPath, err := canonicalPublicPath(path)
	if err != nil || !isPathInside(canonicalPath, canonicalPublic) {
		return errors.New("public artifact path escaped its directory")
	}
	return h.writeAtomic(path, data, 0o600)
}

func (h *Harvester) writeAtomic(path string, data []byte, mode os.FileMode) (returnErr error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".public-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove public temp %s: %w", tmpName, err))
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (h *Harvester) ensurePrivateHandleDir(root, dir string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	private := filepath.Dir(dir)
	if err := ensureNamespaceDir(private, true); err != nil {
		return fmt.Errorf("create private namespace: %w", err)
	}
	if err := ensureNamespaceDir(dir, true); err != nil {
		return fmt.Errorf("create private handle directory: %w", err)
	}
	canonical, err := canonicalPublicPath(dir)
	canonicalRoot, rootErr := canonicalPublicPath(root)
	if err != nil || rootErr != nil || !isPathInside(canonical, canonicalRoot) {
		return errors.New("private handle directory escaped the cache")
	}
	return nil
}

func (h *Harvester) readPublicArtifact(path string) ([]byte, error) {
	canonical, err := canonicalPublicPath(path)
	if err != nil {
		return nil, err
	}
	root, err := h.cacheRoot()
	if err != nil {
		return nil, err
	}
	cacheRoot, err := canonicalPublicPath(root)
	if err != nil {
		return nil, err
	}
	lexicalPath, lexicalErr := filepath.Abs(filepath.Clean(path))
	if lexicalErr != nil {
		return nil, lexicalErr
	}
	if isPrivateMetadataPath(lexicalPath, root) {
		return nil, errors.New("internal metadata is not a document artifact")
	}
	lexicalPublicRoot := filepath.Join(root, "public")
	if isPathInside(lexicalPath, lexicalPublicRoot) {
		if symlinked, symlinkErr := symlinkBelow(lexicalPath, lexicalPublicRoot); symlinkErr != nil || symlinked {
			return nil, errors.New("public artifact path is not a safe namespace")
		}
	}
	if isPathInside(lexicalPath, root) {
		if symlinked, symlinkErr := symlinkBelow(lexicalPath, root); symlinkErr != nil || symlinked {
			return nil, errors.New("artifact path is not a safe namespace")
		}
	}
	if isPathInside(lexicalPath, root) && !isPathInside(canonical, cacheRoot) {
		return nil, errors.New("artifact path escaped the cache")
	}
	if isPathInside(canonical, cacheRoot) {
		rel, _ := filepath.Rel(cacheRoot, canonical)
		first := strings.ToLower(strings.Split(filepath.ToSlash(rel), "/")[0])
		if first == ".private" || first == "stats" || first == "auth" || first == "handles" || first == "searchcache" {
			return nil, errors.New("internal metadata is not a document artifact")
		}
	} else if reason := DenyLocalPath(canonical, h.options.LocalRoots); reason != "" {
		return nil, errors.New("artifact path is not permitted")
	}
	return readBoundedFile(canonical, h.publicLimit())
}

func (h *Harvester) publicLimit() int64 {
	if h != nil && h.options.MaxBytes > 0 {
		return h.options.MaxBytes
	}
	return publicReadLimit
}

func isPrivateMetadataPath(path, root string) bool {
	for _, name := range []string{".private", "stats", "auth", "handles", "searchcache"} {
		if isPathInside(path, filepath.Join(root, name)) {
			return true
		}
	}
	return false
}

func readBoundedFile(path string, limit int64) (data []byte, returnErr error) {
	if limit <= 0 {
		limit = publicReadLimit
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close artifact %s: %w", path, err))
		}
	}()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("artifact is not a regular file")
	} else if info.Size() > limit {
		return nil, errors.New("artifact exceeds public size limit")
	}
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("artifact exceeds public size limit")
	}
	return data, nil
}

func isPathInside(path, root string) bool {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	return insideAny(path, []string{root})
}

func symlinkBelow(path, root string) (bool, error) {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false, err
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false, errors.New("path is outside namespace")
	}
	current := root
	if rel == "." {
		return false, nil
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		if statErr != nil {
			return false, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
	}
	return false, nil
}

func canonicalPublicPath(raw string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", err
	}
	current := abs
	missing := []string{}
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if info, statErr := os.Lstat(current); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", evalErr
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", evalErr
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
