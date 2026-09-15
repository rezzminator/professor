package harvest

// This file is the Go regression oracle for the RETIRED Python harvester's
// test_safe_archive.py and the archive cases in test_dispatch_hardening.py
// (harvester/ was deleted after full migration to pfm; read those suites at
// their last tracked revision, `git show 4edf9c5^:harvester/tests/...`).

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func writeLegacyZip(t *testing.T, path string, members map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeLegacySymlinkZip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	link := &zip.FileHeader{Name: "link.txt", Method: zip.Store}
	link.SetMode(os.ModeSymlink | 0o755)
	w, err := zw.CreateHeader(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "/etc/passwd"); err != nil {
		t.Fatal(err)
	}
	safe, err := zw.Create("safe.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(safe, "safe content"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type legacyTarMember struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func legacyTarBytes(t *testing.T, members ...legacyTarMember) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, member := range members {
		h := &tar.Header{
			Name:     member.name,
			Mode:     0o600,
			Typeflag: member.typeflag,
			Linkname: member.linkname,
			Size:     int64(len(member.body)),
		}
		if member.typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg && len(member.body) > 0 {
			if _, err := tw.Write(member.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeLegacyTar(t *testing.T, path string, members ...legacyTarMember) {
	t.Helper()
	if err := os.WriteFile(path, legacyTarBytes(t, members...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyGzipTar(t *testing.T, path string, members ...legacyTarMember) {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, member := range members {
		h := &tar.Header{
			Name:     member.name,
			Mode:     0o600,
			Typeflag: member.typeflag,
			Linkname: member.linkname,
			Size:     int64(len(member.body)),
		}
		if member.typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg && len(member.body) > 0 {
			if _, err := tw.Write(member.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyXZTar(t *testing.T, path string, members ...legacyTarMember) {
	t.Helper()
	var out bytes.Buffer
	xzw, err := xz.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xzw)
	for _, member := range members {
		h := &tar.Header{
			Name:     member.name,
			Mode:     0o600,
			Typeflag: member.typeflag,
			Linkname: member.linkname,
			Size:     int64(len(member.body)),
		}
		if member.typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg && len(member.body) > 0 {
			if _, err := tw.Write(member.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xzw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// This is a tiny, valid tar.bz2 fixture. Go's standard library has a bzip2
// reader but deliberately no writer, so keeping the fixture inline avoids a
// test-time dependency on a host command.
const legacyTarBzip2Fixture = "QlpoOTFBWSZTWVwea0EAAHJ7gMqAEABAAXOAAIB+Zt5QCAggAFQ0p6TQDQbUAHo1BJKMhoAAAB91AiQgochCKOXyjQZ6BDAxlxU4gyH2xBCLiBx9zOlHmNLm6XxN2eqUfLJdcLJs6U0bYiID8XckU4UJBcHmtBA="

// file_and_empty.7z from github.com/bodgit/sevenzip's testdata. It contains
// one non-empty file and one empty file and needs no external 7z executable.
const legacySevenZipFixture = "N3q8ryccAAQwP4SyFQAAAAAAAAA4AAAAAAAAAA+CMddIdXV1dWdlIGZpbGUgY29udGVudHMBBAYAAQkVAAcLAQABAQAMFQAABQIOAUAPAYARGQBsAGEAcgBnAGUAAABlAG0AcAB0AHkAAAAAAA=="

func writeLegacyBase64Fixture(t *testing.T, path, encoded string) {
	t.Helper()
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// rawLegacyZip writes only ZIP metadata. It is used for announced-size and
// ratio caps, where allocating the announced payload would defeat the purpose
// of the regression test.
func writeLegacyBinary(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}

func rawLegacyZip(t *testing.T, path, name string, compressed, uncompressed uint32) {
	t.Helper()
	var b bytes.Buffer
	nameBytes := []byte(name)
	crc := crc32.ChecksumIEEE(nil)
	writeLegacyBinary(t, &b, uint32(0x04034b50))
	writeLegacyBinary(t, &b, uint16(20))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(zip.Store))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, crc)
	writeLegacyBinary(t, &b, compressed)
	writeLegacyBinary(t, &b, uncompressed)
	writeLegacyBinary(t, &b, uint16(len(nameBytes)))
	writeLegacyBinary(t, &b, uint16(0))
	if _, err := b.Write(nameBytes); err != nil {
		t.Fatal(err)
	}
	centralOffset := uint32(b.Len())
	writeLegacyBinary(t, &b, uint32(0x02014b50))
	writeLegacyBinary(t, &b, uint16(20))
	writeLegacyBinary(t, &b, uint16(20))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(zip.Store))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, crc)
	writeLegacyBinary(t, &b, compressed)
	writeLegacyBinary(t, &b, uncompressed)
	writeLegacyBinary(t, &b, uint16(len(nameBytes)))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint32(0))
	writeLegacyBinary(t, &b, uint32(0))
	if _, err := b.Write(nameBytes); err != nil {
		t.Fatal(err)
	}
	centralSize := uint32(b.Len()) - centralOffset
	writeLegacyBinary(t, &b, uint32(0x06054b50))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(0))
	writeLegacyBinary(t, &b, uint16(1))
	writeLegacyBinary(t, &b, uint16(1))
	writeLegacyBinary(t, &b, centralSize)
	writeLegacyBinary(t, &b, centralOffset)
	writeLegacyBinary(t, &b, uint16(0))
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func legacyErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err, want)
	}
}

func TestLegacyParityNormalZipListingAndMemberReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "normal.zip")
	writeLegacyZip(
		t,
		path,
		map[string][]byte{"hello.txt": []byte("hello world"), "sub/foo.txt": []byte("foo content"), "empty.dat": nil},
	)
	members, err := ListArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, member := range members {
		names[member.Name] = true
	}
	for _, name := range []string{"hello.txt", "sub/foo.txt", "empty.dat"} {
		if !names[name] {
			t.Fatalf("listing omitted %q: %#v", name, members)
		}
	}
	for name, want := range map[string]string{"hello.txt": "hello world", "sub/foo.txt": "foo content"} {
		got, err := ReadArchiveMember(path, name)
		if err != nil || string(got) != want {
			t.Fatalf("ReadArchiveMember(%q) = %q, err=%v; want %q", name, got, err, want)
		}
	}
	member := members[0]
	if member.Name == "empty.dat" {
		member = members[1]
	}
	if member.UncompressedSize == 0 || member.IsDir || member.IsSymlink {
		t.Fatalf("member metadata = %#v, want non-empty regular file", member)
	}
	legacyErrorContains(t, func() error { _, err := ReadArchiveMember(path, "ghost.txt"); return err }(), "not found")
}

func TestLegacyParityZipTraversalAndAbsoluteNames(t *testing.T) {
	traversal := filepath.Join(t.TempDir(), "traversal.zip")
	writeLegacyZip(t, traversal, map[string][]byte{"../evil.txt": []byte("owned")})
	_, err := ListArchive(traversal)
	legacyErrorContains(t, err, "..")
	_, err = ReadArchiveMember(traversal, "../evil.txt")
	legacyErrorContains(t, err, "..")
	nested := filepath.Join(t.TempDir(), "nested.zip")
	writeLegacyZip(t, nested, map[string][]byte{"a/../../b.txt": []byte("bad")})
	_, err = ListArchive(nested)
	legacyErrorContains(t, err, "..")
	ok := filepath.Join(t.TempDir(), "ok.zip")
	writeLegacyZip(t, ok, map[string][]byte{"safe.txt": []byte("ok")})
	_, err = ReadArchiveMember(ok, "/etc/passwd")
	legacyErrorContains(t, err, "absolute")
}

func TestLegacyParityZipSymlinkFlagAndRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "symlink.zip")
	writeLegacySymlinkZip(t, path)
	members, err := ListArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	var link, safe Member
	for _, member := range members {
		switch member.Name {
		case "link.txt":
			link = member
		case "safe.txt":
			safe = member
		}
	}
	if !link.IsSymlink || safe.IsSymlink {
		t.Fatalf("symlink flags = link:%#v safe:%#v", link, safe)
	}
	_, err = ReadArchiveMember(path, "link.txt")
	legacyErrorContains(t, err, "symlink")
	body, err := ReadArchiveMember(path, "safe.txt")
	if err != nil || string(body) != "safe content" {
		t.Fatalf("safe member = %q, err=%v", body, err)
	}
}

func TestLegacyParityTarSymlinkHardlinkTraversalAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.tar.gz")
	writeLegacyGzipTar(t, path,
		legacyTarMember{name: "normal.txt", body: []byte("normal content")},
		legacyTarMember{name: "link.txt", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		legacyTarMember{name: "hardlink.txt", typeflag: tar.TypeLink, linkname: "normal.txt"},
	)
	members, err := ListArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{}
	for _, member := range members {
		flags[member.Name] = member.IsSymlink
	}
	if !flags["link.txt"] || !flags["hardlink.txt"] {
		t.Fatalf("link flags = %#v", flags)
	}
	for _, name := range []string{"link.txt", "hardlink.txt"} {
		_, err := ReadArchiveMember(path, name)
		legacyErrorContains(t, err, "symlink")
	}
	dotdot := filepath.Join(t.TempDir(), "dotdot.tar.gz")
	writeLegacyGzipTar(t, dotdot, legacyTarMember{name: "../evil.txt", body: []byte("pwned")})
	_, err = ListArchive(dotdot)
	legacyErrorContains(t, err, "..")
}

func TestLegacyParityTarCompressionFormats(t *testing.T) {
	member := legacyTarMember{name: "hello.txt", body: []byte("compressed member")}
	gzipPath := filepath.Join(t.TempDir(), "sample.tar.gz")
	writeLegacyGzipTar(t, gzipPath, member)
	xzPath := filepath.Join(t.TempDir(), "sample.tar.xz")
	writeLegacyXZTar(t, xzPath, member)
	bzipPath := filepath.Join(t.TempDir(), "sample.tar.bz2")
	bzipBody, err := base64.StdEncoding.DecodeString(legacyTarBzip2Fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bzipPath, bzipBody, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, want string }{
		{gzipPath, "compressed member"},
		{xzPath, "compressed member"},
		{bzipPath, "compressed bzip2"},
	} {
		members, err := ListArchive(tc.path)
		if err != nil || len(members) != 1 {
			t.Fatalf("ListArchive(%q) = %#v, err=%v", tc.path, members, err)
		}
		body, err := ReadArchiveMember(tc.path, members[0].Name)
		if err != nil || string(body) != tc.want {
			t.Fatalf("ReadArchiveMember(%q) = %q, err=%v; want %q", tc.path, body, err, tc.want)
		}
	}
	// Magic detection must work when a compressed tar has no useful suffix.
	magicPath := filepath.Join(t.TempDir(), "payload.bin")
	gzipBytes, err := os.ReadFile(gzipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(magicPath, gzipBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListArchive(magicPath); err != nil {
		t.Fatalf("magic-detected gzip tar: %v", err)
	}
}

func TestLegacyParityRARFormatDispatchesToRARReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.rar")
	if err := os.WriteFile(path, []byte("Rar!\x1a\x07\x00not a complete archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ListArchive(path)
	legacyErrorContains(t, err, "open RAR")
}

func TestLegacyParitySevenZipListingReadsAndMissingMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.7z")
	writeLegacyBase64Fixture(t, path, legacySevenZipFixture)
	members, err := ListArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) < 2 {
		t.Fatalf("7z members = %#v, want file and empty entries", members)
	}
	read := 0
	for _, member := range members {
		if member.IsDir {
			continue
		}
		body, err := ReadArchiveMember(path, member.Name)
		if err != nil {
			t.Fatalf("read 7z member %q: %v", member.Name, err)
		}
		read++
		if member.UncompressedSize > 0 && len(body) == 0 {
			t.Fatalf("non-empty 7z member %q returned no bytes", member.Name)
		}
	}
	if read < 2 {
		t.Fatalf("read %d 7z members, want at least 2", read)
	}
	_, err = ReadArchiveMember(path, "ghost.txt")
	legacyErrorContains(t, err, "not found")
}

func TestLegacyParityArchiveCaps(t *testing.T) {
	tooMany := filepath.Join(t.TempDir(), "many.zip")
	f, err := os.Create(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for i := 0; i <= MaxArchiveMembers; i++ {
		w, err := zw.Create(fmt.Sprintf("file%d.txt", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = ListArchive(tooMany)
	legacyErrorContains(t, err, "members")

	tooManyTar := filepath.Join(t.TempDir(), "many.tar")
	entries := make([]legacyTarMember, 0, MaxArchiveMembers+1)
	for i := 0; i <= MaxArchiveMembers; i++ {
		entries = append(entries, legacyTarMember{name: fmt.Sprintf("f%d.txt", i), body: []byte("x")})
	}
	writeLegacyTar(t, tooManyTar, entries...)
	_, err = ListArchive(tooManyTar)
	legacyErrorContains(t, err, "members")

	total := filepath.Join(t.TempDir(), "total.zip")
	rawLegacyZip(t, total, "total.bin", uint32(MaxArchiveTotalBytes+1), uint32(MaxArchiveTotalBytes+1))
	_, err = ListArchive(total)
	legacyErrorContains(t, err, "total uncompressed")

	file := filepath.Join(t.TempDir(), "file.zip")
	rawLegacyZip(t, file, "file.bin", 1, uint32(MaxArchiveFileBytes+1))
	_, err = ReadArchiveMember(file, "file.bin")
	legacyErrorContains(t, err, "file limit")

	ratio := filepath.Join(t.TempDir(), "ratio.zip")
	rawLegacyZip(t, ratio, "bomb.bin", 1, uint32(MaxArchiveRatio+1))
	_, err = ListArchive(ratio)
	legacyErrorContains(t, err, "ratio")
}

func TestLegacyParityNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name, want string
	}{
		{"file\x00.txt", "null byte"},
		{"/etc/passwd", "absolute"},
		{"../outside.txt", ".."},
		{"a/b/../../etc/passwd", ".."},
		{strings.Repeat("a", 256), "too long"},
	} {
		legacyErrorContains(t, validateMemberName(tc.name), tc.want)
	}
	if err := validateMemberName(strings.Repeat("a", 255)); err != nil {
		t.Fatalf("255-codepoint name rejected: %v", err)
	}
	if got := normalizedMemberName("cafe\u0301"); got != "café" {
		t.Fatalf("NFC normalization = %q", got)
	}
	zipPath := filepath.Join(t.TempDir(), "nul.zip")
	writeLegacyZip(t, zipPath, map[string][]byte{"safe.txt": []byte("ok")})
	_, err := ReadArchiveMember(zipPath, "safe\x00.txt")
	legacyErrorContains(t, err, "null byte")
	_, err = ReadArchiveMember(zipPath, "/etc/passwd")
	legacyErrorContains(t, err, "absolute")
	tarPath := filepath.Join(t.TempDir(), "nul.tar")
	writeLegacyTar(t, tarPath, legacyTarMember{name: "safe.txt", body: []byte("ok")})
	_, err = ReadArchiveMember(tarPath, "safe\x00.txt")
	legacyErrorContains(t, err, "null byte")
}

func legacyArchiveClient(status int, contentType string, body []byte) *http.Client {
	return &http.Client{Transport: legacyRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}, nil
	})}
}

type legacyRoundTrip func(*http.Request) (*http.Response, error)

func (f legacyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type legacyArchiveConverter struct{}

func (legacyArchiveConverter) Convert(_ context.Context, _, _ string, body []byte) (string, error) {
	return string(body), nil
}

func walkLegacyExt(t *testing.T, root, ext string) []string {
	t.Helper()
	var found []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ext) {
			found = append(found, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestLegacyParityArchiveLocalAndRemoteSources(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "local.zip")
	writeLegacyZip(t, zipPath, map[string][]byte{"hello.txt": []byte("local")})
	h := mustNew(
		t,
		Options{
			CacheDir:   t.TempDir(),
			LocalRoots: []string{filepath.Dir(zipPath)},
			Converter:  legacyArchiveConverter{},
		},
	)
	for _, source := range []string{zipPath, "file://" + zipPath} {
		got, err := h.Archive(context.Background(), source, "hello.txt")
		if err != nil || got.Error != "" || got.Kind != "archive_member" || got.Content == "" {
			t.Fatalf("local Archive(%q) = %#v, err=%v", source, got, err)
		}
	}
	remotePath := "https://archive.example/data.zip"
	body := func() []byte {
		var out bytes.Buffer
		zw := zip.NewWriter(&out)
		w, err := zw.Create("hello.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("remote")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}()
	remoteClient := legacyArchiveClient(http.StatusOK, "application/zip", body)
	h = mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    remoteClient,
			Chrome:    legacyArchiveClient(http.StatusOK, "application/zip", body),
			Converter: legacyArchiveConverter{},
		},
	)
	listing, err := h.Archive(context.Background(), remotePath, "")
	if err != nil || listing.Error != "" || len(listing.Members) != 1 || listing.Method != "archive:listing" {
		t.Fatalf("remote archive listing = %#v, err=%v", listing, err)
	}
	member, err := h.Archive(context.Background(), remotePath, "hello.txt")
	if err != nil || member.Error != "" || member.Kind != "archive_member" {
		t.Fatalf("remote archive member = %#v, err=%v", member, err)
	}
}

func TestLegacyParityArchive4xxBodyNeverCached(t *testing.T) {
	cacheDir := t.TempDir()
	body := []byte("<html><body>Forbidden</body></html>")
	h := mustNew(
		t,
		Options{
			CacheDir: cacheDir,
			Client:   legacyArchiveClient(http.StatusForbidden, "text/html", body),
			Chrome:   legacyArchiveClient(http.StatusForbidden, "text/html", body),
		},
	)
	got, err := h.Archive(context.Background(), "https://gone.example/data.zip", "")
	if err == nil || got.Error == "" {
		t.Fatalf("Archive 403 = %#v, err=%v; want explicit failure", got, err)
	}
	if files := walkLegacyExt(t, cacheDir, ".zip"); len(files) != 0 {
		t.Fatalf("4xx HTML cached as archive: %v", files)
	}
}

func TestLegacyParityBinaryArchiveIsNotCachedAsText(t *testing.T) {
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	w, err := zw.Create("book.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bytes.Repeat([]byte("x"), 2000)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	h := mustNew(
		t,
		Options{
			CacheDir: cacheDir,
			Client:   legacyArchiveClient(http.StatusOK, "application/epub+zip", zipBytes.Bytes()),
			Chrome:   legacyArchiveClient(http.StatusOK, "application/epub+zip", zipBytes.Bytes()),
		},
	)
	// epub is not an archive API input in the Go seam; fetchURL is the closest
	// boundary to the OA-candidate path and must never turn ZIP bytes into .md.
	got := h.fetchURL(context.Background(), "https://web.archive.org/book.epub", FetchOptions{})
	if got.Error == "" {
		t.Fatalf("binary candidate = %#v; want rejection without converter", got)
	}
	if files := walkLegacyExt(t, cacheDir, ".md"); len(files) != 0 {
		t.Fatalf("binary archive cached as text: %v", files)
	}
}
