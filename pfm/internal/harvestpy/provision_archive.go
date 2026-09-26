package harvestpy

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractNamedBinary(path, name, destination string) (returnErr error) {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := input.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close binary archive %s: %w", path, err))
		}
	}()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer func() {
		if err := gzipReader.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close binary gzip stream %s: %w", path, err))
		}
	}()
	archive := tar.NewReader(gzipReader)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(header.Name) != name || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if err := copyLimited(destination, archive, header.Size); err != nil {
			return err
		}
		return os.Chmod(destination, 0o700)
	}
	return fmt.Errorf("binary %q not found in archive", name)
}

func extractPython(path, destination string) (pythonPath string, returnErr error) {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", fmt.Errorf("create Python extraction root: %w", err)
	}
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := input.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Python archive %s: %w", path, err))
		}
	}()
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := gzipReader.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Python gzip stream %s: %w", path, err))
		}
	}()
	archive := tar.NewReader(gzipReader)
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return "", err
		}
		destinationPath := filepath.Join(destination, filepath.FromSlash(name))
		if header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeXHeader ||
			header.Typeflag == tar.TypeGNULongName ||
			header.Typeflag == tar.TypeGNULongLink {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(destinationPath, header.FileInfo().Mode().Perm()); err != nil {
				return "", fmt.Errorf("create Python directory %s: %w", name, err)
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > 512<<20 || total > 2<<30-header.Size {
				return "", fmt.Errorf("python archive is too large at %d bytes", total+header.Size)
			}
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return "", fmt.Errorf("create Python parent directory: %w", err)
			}
			if err := copyLimited(destinationPath, archive, header.Size); err != nil {
				return "", fmt.Errorf("extract Python file %s: %w", name, err)
			}
			total += header.Size
			if err := os.Chmod(destinationPath, header.FileInfo().Mode().Perm()); err != nil {
				return "", fmt.Errorf("preserve Python file mode %s: %w", name, err)
			}
		case tar.TypeSymlink:
			target, err := safeSymlinkTarget(name, header.Linkname)
			if err != nil {
				return "", fmt.Errorf("unsafe Python symlink %s: %w", name, err)
			}
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return "", err
			}
			if err := os.Symlink(target, destinationPath); err != nil {
				return "", fmt.Errorf("extract Python symlink %s: %w", name, err)
			}
		case tar.TypeLink:
			target, err := safeArchiveName(header.Linkname)
			if err != nil {
				return "", fmt.Errorf("unsafe Python hardlink %s: %w", name, err)
			}
			if err := ensureArchiveParentSafe(destination, destinationPath); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
				return "", err
			}
			if err := os.Link(filepath.Join(destination, filepath.FromSlash(target)), destinationPath); err != nil {
				return "", fmt.Errorf("extract Python hardlink %s: %w", name, err)
			}
		default:
			return "", fmt.Errorf("unsupported Python archive entry %s (type %d)", name, header.Typeflag)
		}
	}
	for _, relative := range []string{"python/bin/python3", "python/bin/python"} {
		candidate := filepath.Join(destination, filepath.FromSlash(relative))
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("python executable not found in extracted archive")
}

func safeArchiveName(name string) (string, error) {
	name = filepath.ToSlash(name)
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean != strings.TrimSuffix(name, "/") || strings.HasPrefix(clean, "../") ||
		strings.Contains(clean, "/../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return strings.TrimSuffix(clean, "/"), nil
}

func safeSymlinkTarget(entry, linkname string) (string, error) {
	linkname = filepath.ToSlash(linkname)
	if linkname == "" || strings.HasPrefix(linkname, "/") || strings.ContainsRune(linkname, 0) {
		return "", fmt.Errorf("unsafe symlink target %q", linkname)
	}
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(entry), linkname)))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("symlink target escapes archive root: %q", linkname)
	}
	return filepath.ToSlash(filepath.Clean(linkname)), nil
}

func ensureArchiveParentSafe(root, path string) error {
	relative, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive path escapes extraction root: %s", path)
	}
	current := root
	if info, err := os.Lstat(current); err != nil || !info.IsDir() {
		if err != nil {
			return fmt.Errorf("inspect extraction root: %w", err)
		}
		return fmt.Errorf("extraction root is not a directory: %s", root)
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect archive parent %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive parent is a symlink: %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("archive parent is not a directory: %s", current)
		}
	}
	return nil
}

func copyLimited(destination string, source io.Reader, size int64) error {
	if size < 0 || size > 512<<20 {
		return fmt.Errorf("archive file size %d exceeds limit", size)
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	written, err := io.Copy(output, io.LimitReader(source, size))
	if err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if written != size {
		_ = output.Close()
		_ = os.Remove(destination)
		return fmt.Errorf("archive entry ended at %d bytes, want %d", written, size)
	}
	return output.Close()
}
