package harvest

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	sevenzip "github.com/bodgit/sevenzip"
	rardecode "github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"
	"golang.org/x/text/unicode/norm"
)

// Archive limits are variables, rather than compile-time constants, on
// purpose. The Python implementation exposes these as module-level policy
// knobs and its regression suite lowers them to exercise bomb handling with
// tiny fixtures. Production defaults remain the documented 1000/1 GiB/100
// MiB/100x values; callers that need per-request limits should serialize a
// temporary override or use the internal limit-aware helpers in tests.
var (
	MaxArchiveMembers    int   = 1000
	MaxArchiveTotalBytes int64 = 1 << 30
	MaxArchiveFileBytes  int64 = 100 << 20
	MaxArchiveRatio      int64 = 100
)

type Member struct {
	Name             string `json:"name"`
	CompressedSize   int64  `json:"compressed_size"`
	UncompressedSize int64  `json:"uncompressed_size"`
	IsDir            bool   `json:"is_dir"`
	IsSymlink        bool   `json:"is_symlink"`
}

func validateMemberName(name string) error {
	name = normalizedMemberName(name)
	if !utf8.ValidString(name) {
		return fmt.Errorf("member name is not UTF-8")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("Member name contains null byte: %q", name)
	}
	name = filepath.ToSlash(name)
	if name == "" {
		return fmt.Errorf("member name is empty")
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("Member name is absolute path: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("Member name contains '..': %q", name)
		}
	}
	// Python's oracle bounds Unicode code points after NFC normalization;
	// len(string) would incorrectly count UTF-8 bytes here.
	if utf8.RuneCountInString(name) > 255 {
		runes := []rune(name)
		return fmt.Errorf("Member name too long (%d > 255): %q…", len(runes), string(runes[:minInt(len(runes), 80)]))
	}
	return nil
}

func normalizedMemberName(name string) string {
	return norm.NFC.String(filepath.ToSlash(name))
}

func archiveFormat(path string) string {
	low := strings.ToLower(path)
	if strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz") || strings.HasSuffix(low, ".tar.bz2") ||
		strings.HasSuffix(low, ".tbz2") ||
		strings.HasSuffix(low, ".tar.xz") ||
		strings.HasSuffix(low, ".txz") ||
		strings.HasSuffix(low, ".tar") {
		return "tar"
	}
	if strings.HasSuffix(low, ".zip") {
		return "zip"
	}
	if strings.HasSuffix(low, ".7z") {
		return "7z"
	}
	if strings.HasSuffix(low, ".rar") {
		return "rar"
	}
	f, e := os.Open(path)
	if e != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 8)
	_, _ = io.ReadFull(f, head)
	switch {
	case head[0] == 'P' && head[1] == 'K':
		return "zip"
	case head[0] == '7' && head[1] == 'z' && head[2] == 0xbc && head[3] == 0xaf:
		return "7z"
	case strings.HasPrefix(string(head), "Rar!"):
		return "rar"
	case head[0] == 0x1f && head[1] == 0x8b:
		return "tar"
	case head[0] == 'B' && head[1] == 'Z':
		return "tar"
	case head[0] == 0xfd && head[1] == '7' && head[2] == 'z' && head[3] == 'X':
		return "tar"
	}
	return ""
}

// ListArchive performs a listing pass and enforces count, path, symlink, and
// expansion limits before any member is read.
func ListArchive(path string) ([]Member, error) {
	switch archiveFormat(path) {
	case "zip":
		return listZip(path)
	case "tar":
		return listTar(path)
	case "7z":
		return list7z(path)
	case "rar":
		return listRAR(path)
	default:
		return nil, fmt.Errorf("unsupported or unrecognized archive format: %s", path)
	}
}

func listZip(path string) ([]Member, error) {
	f, e := zip.OpenReader(path)
	if e != nil {
		return nil, fmt.Errorf("open zip: %w", e)
	}
	defer f.Close()
	out := make([]Member, 0, len(f.File))
	var total int64
	for _, entry := range f.File {
		name := normalizedMemberName(entry.Name)
		if e := validateMemberName(name); e != nil {
			return nil, e
		}
		symlink := entry.Mode()&os.ModeSymlink != 0
		m := Member{
			Name:             name,
			CompressedSize:   int64(entry.CompressedSize64),
			UncompressedSize: int64(entry.UncompressedSize64),
			IsDir:            entry.FileInfo().IsDir(),
			IsSymlink:        symlink,
		}
		if m.CompressedSize > 0 && float64(m.UncompressedSize)/float64(m.CompressedSize) > float64(MaxArchiveRatio) {
			ratio := float64(m.UncompressedSize) / float64(m.CompressedSize)
			return nil, fmt.Errorf(
				"Member %q: compression ratio %.1fx exceeds %dx (zip-bomb?)",
				m.Name,
				ratio,
				MaxArchiveRatio,
			)
		}
		if !m.IsDir {
			total += m.UncompressedSize
		}
		out = append(out, m)
	}
	if len(out) > MaxArchiveMembers {
		return nil, fmt.Errorf("Archive has %d members (limit %d)", len(out), MaxArchiveMembers)
	}
	if total > MaxArchiveTotalBytes {
		return nil, fmt.Errorf("Archive total uncompressed size %d bytes exceeds limit %d", total, MaxArchiveTotalBytes)
	}
	return out, nil
}

func openTar(path string) (io.Reader, func() error, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	var head [2]byte
	_, _ = io.ReadFull(f, head[:])
	_, _ = f.Seek(0, io.SeekStart)
	if head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, nil, err
		}
		return gz, func() error { _ = gz.Close(); return f.Close() }, nil
	}
	if head[0] == 'B' && head[1] == 'Z' {
		bz := bzip2.NewReader(f)
		return bz, f.Close, nil
	}
	if head[0] == 0xfd && head[1] == '7' {
		xzr, err := xz.NewReader(f)
		if err != nil {
			_ = f.Close()
			return nil, nil, fmt.Errorf("open xz: %w", err)
		}
		return xzr, f.Close, nil
	}
	return f, f.Close, nil
}

func listTar(path string) ([]Member, error) {
	r, closeFn, e := openTar(path)
	if e != nil {
		return nil, e
	}
	defer closeFn()
	tr := tar.NewReader(r)
	out := []Member{}
	var total int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("read tar: %w", e)
		}
		name := normalizedMemberName(h.Name)
		if e := validateMemberName(name); e != nil {
			return nil, e
		}
		m := Member{
			Name:             name,
			UncompressedSize: h.Size,
			IsDir:            h.FileInfo().IsDir(),
			IsSymlink:        h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink,
		}
		if !m.IsDir {
			if m.UncompressedSize > MaxArchiveTotalBytes-total {
				return nil, fmt.Errorf("Archive total uncompressed size exceeds limit %d", MaxArchiveTotalBytes)
			}
			total += m.UncompressedSize
		}
		out = append(out, m)
	}
	if len(out) > MaxArchiveMembers {
		return nil, fmt.Errorf("Archive has %d members (limit %d)", len(out), MaxArchiveMembers)
	}
	if total > MaxArchiveTotalBytes {
		return nil, fmt.Errorf("Archive total uncompressed size %d bytes exceeds limit %d", total, MaxArchiveTotalBytes)
	}
	return out, nil
}

func list7z(path string) ([]Member, error) {
	r, err := sevenzip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open 7z: %w", err)
	}
	defer r.Close()
	out := make([]Member, 0, len(r.File))
	var total int64
	for _, entry := range r.File {
		name := normalizedMemberName(entry.Name)
		if err := validateMemberName(name); err != nil {
			return nil, err
		}
		if entry.UncompressedSize > uint64(^uint64(0)>>1) {
			return nil, fmt.Errorf("member %q size exceeds platform limit", entry.Name)
		}
		m := Member{
			Name:             name,
			UncompressedSize: int64(entry.UncompressedSize),
			IsDir:            entry.FileInfo().IsDir(),
			IsSymlink:        entry.Mode()&fs.ModeSymlink != 0,
		}
		if !m.IsDir {
			if m.UncompressedSize > MaxArchiveTotalBytes-total {
				return nil, fmt.Errorf("archive exceeds total uncompressed limit")
			}
			total += m.UncompressedSize
		}
		out = append(out, m)
	}
	if len(out) > MaxArchiveMembers {
		return nil, fmt.Errorf("Archive has %d members (limit %d)", len(out), MaxArchiveMembers)
	}
	return out, nil
}

func read7z(path, name string) ([]byte, error) {
	name = normalizedMemberName(name)
	if err := validateMemberName(name); err != nil {
		return nil, err
	}
	r, err := sevenzip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open 7z: %w", err)
	}
	defer r.Close()
	for _, entry := range r.File {
		if normalizedMemberName(entry.Name) != name {
			continue
		}
		if entry.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to read symlink member %q", name)
		}
		if entry.FileInfo().IsDir() {
			return nil, fmt.Errorf("member is a directory")
		}
		if entry.UncompressedSize > uint64(MaxArchiveFileBytes) {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("open 7z member %q: %w", name, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(rc, MaxArchiveFileBytes+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close 7z member %q: %w", name, closeErr)
		}
		if int64(len(body)) > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		return body, nil
	}
	return nil, fmt.Errorf("member not found: %s", name)
}

func listRAR(path string) ([]Member, error) {
	entries, err := rardecode.List(path)
	if err != nil {
		return nil, fmt.Errorf("open RAR: %w", err)
	}
	out := make([]Member, 0, len(entries))
	var total int64
	for _, entry := range entries {
		name := normalizedMemberName(entry.Name)
		if err := validateMemberName(name); err != nil {
			return nil, err
		}
		if entry.UnPackedSize < 0 {
			return nil, fmt.Errorf("Member %q has invalid size", entry.Name)
		}
		m := Member{
			Name:             name,
			CompressedSize:   entry.PackedSize,
			UncompressedSize: entry.UnPackedSize,
			IsDir:            entry.IsDir,
			IsSymlink:        entry.Mode()&fs.ModeSymlink != 0,
		}
		if !m.IsDir {
			if m.UncompressedSize > MaxArchiveTotalBytes-total {
				return nil, fmt.Errorf("Archive total uncompressed size exceeds limit %d", MaxArchiveTotalBytes)
			}
			total += m.UncompressedSize
		}
		out = append(out, m)
	}
	if len(out) > MaxArchiveMembers {
		return nil, fmt.Errorf("Archive has %d members (limit %d)", len(out), MaxArchiveMembers)
	}
	for _, m := range out {
		if m.CompressedSize > 0 && float64(m.UncompressedSize)/float64(m.CompressedSize) > float64(MaxArchiveRatio) {
			ratio := float64(m.UncompressedSize) / float64(m.CompressedSize)
			return nil, fmt.Errorf(
				"Member %q: compression ratio %.1fx exceeds %dx (zip-bomb?)",
				m.Name,
				ratio,
				MaxArchiveRatio,
			)
		}
	}
	return out, nil
}

func readRAR(path, name string) ([]byte, error) {
	name = normalizedMemberName(name)
	if err := validateMemberName(name); err != nil {
		return nil, err
	}
	entries, err := rardecode.List(path)
	if err != nil {
		return nil, fmt.Errorf("open RAR: %w", err)
	}
	for _, entry := range entries {
		if normalizedMemberName(entry.Name) != name {
			continue
		}
		if entry.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to read symlink member %q", name)
		}
		if entry.IsDir {
			return nil, fmt.Errorf("member is a directory")
		}
		if entry.UnPackedSize > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("open RAR member %q: %w", name, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(rc, MaxArchiveFileBytes+1))
		closeErr := rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read RAR member %q: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close RAR member %q: %w", name, closeErr)
		}
		if int64(len(body)) > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		return body, nil
	}
	return nil, fmt.Errorf("member not found: %s", name)
}

func ReadArchiveMember(path, name string) ([]byte, error) {
	if e := validateMemberName(name); e != nil {
		return nil, e
	}
	switch archiveFormat(path) {
	case "zip":
		return readZip(path, name)
	case "tar":
		return readTar(path, name)
	case "7z":
		return read7z(path, name)
	case "rar":
		return readRAR(path, name)
	default:
		return nil, fmt.Errorf("unsupported or unrecognized archive format: %s", path)
	}
}

func readZip(path, name string) ([]byte, error) {
	f, e := zip.OpenReader(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	name = normalizedMemberName(name)
	if e := validateMemberName(name); e != nil {
		return nil, e
	}
	for _, entry := range f.File {
		if normalizedMemberName(entry.Name) != name {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to read symlink member %q", name)
		}
		if entry.FileInfo().IsDir() {
			return nil, fmt.Errorf("member is a directory")
		}
		if int64(entry.UncompressedSize64) > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		if entry.CompressedSize64 > 0 &&
			float64(entry.UncompressedSize64)/float64(entry.CompressedSize64) > float64(MaxArchiveRatio) {
			return nil, fmt.Errorf("compression ratio exceeds limit")
		}
		r, e := entry.Open()
		if e != nil {
			return nil, e
		}
		defer r.Close()
		body, e := io.ReadAll(io.LimitReader(r, MaxArchiveFileBytes+1))
		if e != nil {
			return nil, e
		}
		if int64(len(body)) > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		return body, nil
	}
	return nil, fmt.Errorf("member not found: %s", name)
}

func readTar(path, name string) ([]byte, error) {
	r, closeFn, e := openTar(path)
	if e != nil {
		return nil, e
	}
	defer closeFn()
	name = normalizedMemberName(name)
	if e := validateMemberName(name); e != nil {
		return nil, e
	}
	tr := tar.NewReader(r)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if normalizedMemberName(h.Name) != name {
			continue
		}
		if h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink {
			return nil, fmt.Errorf("refusing to read symlink/hardlink member %q", name)
		}
		if h.FileInfo().IsDir() {
			return nil, fmt.Errorf("member is a directory")
		}
		if h.Size > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		body, e := io.ReadAll(io.LimitReader(tr, MaxArchiveFileBytes+1))
		if e != nil {
			return nil, e
		}
		if int64(len(body)) > MaxArchiveFileBytes {
			return nil, fmt.Errorf("member exceeds file limit")
		}
		return body, nil
	}
	return nil, fmt.Errorf("member not found: %s", name)
}

// Archive routes a source through local or URL fetching, then lists or reads
// one member without ever extracting an archive to the filesystem.
func (h *Harvester) Archive(ctx context.Context, source, member string) (Result, error) {
	if member != "" {
		source = source + "::" + member
	}
	if strings.Contains(source, "::") {
		parts := strings.SplitN(source, "::", 2)
		path, download := h.fetchArchiveBytes(ctx, parts[0], false)
		if download.Error != "" {
			return download, errors.New(download.Error)
		}
		data, e := ReadArchiveMember(path, parts[1])
		if e != nil {
			return Result{Source: source, Error: e.Error()}, e
		}
		kind := classifyKind(parts[1], "", data)
		if isImageKind(kind) {
			stored := h.storeBinary(source, kind, "archive:member:image", data, false)
			if stored.Error != "" {
				return stored, errors.New(stored.Error)
			}
			content := fmt.Sprintf(
				"![%s](%s)\n\n*Image extracted from the archive at `%s` — read it directly.*\n",
				parts[1],
				stored.Path,
				stored.Path,
			)
			stored.Source = source
			stored.Kind = "archive_member"
			stored.Content = content
			stored.Chars = len(content)
			stored.ContentChars = len(content)
			stored.Tokens = estimateTokens(content)
			return stored, nil
		}
		converted, convErr := h.convert(ctx, kind, source, data)
		if convErr != nil {
			// A nil converter is useful for archive browsing tests and raw text
			// members. Preserve bytes verbatim; document conversion itself remains
			// injected and is still required for PDFs/Office/HTML extraction.
			if h.options.Converter == nil && (kind == "txt" || kind == "html") {
				converted = string(data)
			} else {
				return Result{Source: source, Kind: "archive_member", Error: convErr.Error()}, convErr
			}
		}
		chars := contentChars(converted)
		return Result{
			Source:       source,
			Kind:         "archive_member",
			Content:      converted,
			Bytes:        int64(len(data)),
			Chars:        chars,
			ContentChars: chars,
			Tokens:       estimateTokens(converted),
		}, nil
	}
	return h.archiveList(ctx, source)
}

func (h *Harvester) archiveList(ctx context.Context, source string) (Result, error) {
	path, download := h.fetchArchiveBytes(ctx, source, false)
	if download.Error != "" {
		return download, errors.New(download.Error)
	}
	members, e := ListArchive(path)
	if e != nil {
		return Result{Source: source, Error: e.Error()}, e
	}
	content := formatArchiveListing(source, members)
	chars := contentChars(content)
	return Result{
		Source:       source,
		Kind:         "archive",
		Content:      content,
		Members:      members,
		Path:         download.Path,
		Method:       "archive:listing",
		CacheStatus:  download.CacheStatus,
		Chars:        chars,
		ContentChars: chars,
		Tokens:       estimateTokens(content),
	}, nil
}

func formatArchiveListing(source string, members []Member) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"# Archive: %s\n\n%d member(s). Fetch one with `archive(source=%q, member=\"<name>\")`.\n\n",
		source,
		len(members),
		source,
	)
	b.WriteString("| name | size (bytes) | type |\n| --- | ---: | --- |\n")
	for _, member := range members {
		name := strings.ReplaceAll(member.Name, "|", "\\|")
		kind := "file"
		if member.IsDir {
			kind = "dir"
		} else if member.IsSymlink {
			kind = "symlink"
		}
		fmt.Fprintf(&b, "| %s | %d | %s |\n", name, member.UncompressedSize, kind)
	}
	return b.String()
}
