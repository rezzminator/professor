package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// OpenStateFile opens only a regular file of the effective user in a
// protected directory of the effective user, through no symlink and without
// blocking: every other thing at the path is a named error, never a hang. A
// foreign owner needs a second user, so lockNativeReload's safety test
// (reload TestLockNativeReloadSafety) is its home.
func TestOpenStateFile(t *testing.T) {
	const name = "state.lock"
	for _, test := range []struct {
		name    string
		flag    int
		prepare func(t *testing.T, dir string) string
		want    string
	}{
		{"a missing file is created", os.O_CREATE | os.O_RDWR, protected, ""},
		{"a missing file without O_CREATE is absent", os.O_RDONLY, protected, "no such file or directory"},
		{"a FIFO is no regular file", os.O_CREATE | os.O_RDWR, func(t *testing.T, dir string) string {
			dir = protected(t, dir)
			if err := syscall.Mkfifo(filepath.Join(dir, name), 0o600); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "test file is not a regular file"},
		{"a directory is refused", os.O_CREATE | os.O_RDWR, func(t *testing.T, dir string) string {
			dir = protected(t, dir)
			if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "open test file:"},
		{"a symlinked file is refused", os.O_CREATE | os.O_RDWR, func(t *testing.T, dir string) string {
			dir = protected(t, dir)
			if err := os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "open test file:"},
		{"a symlinked directory is refused", os.O_CREATE | os.O_RDWR, func(t *testing.T, dir string) string {
			link := filepath.Join(dir, "link")
			if err := os.Symlink(protected(t, filepath.Join(dir, "real")), link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "open test file directory:"},
		{"a directory others can write is refused", os.O_CREATE | os.O_RDWR, func(t *testing.T, dir string) string {
			dir = protected(t, dir)
			if err := os.Chmod(dir, 0o770); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "test file directory is not a protected directory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := test.prepare(t, t.TempDir())
			file, err := OpenStateFile(dir, name, test.flag, "test file")
			if test.want != "" {
				if err == nil {
					_ = file.Close()
					t.Fatalf("OpenStateFile = a file, want an error naming %q", test.want)
				}
				if !strings.Contains(err.Error(), test.want) {
					t.Fatalf("OpenStateFile error = %v, want it naming %q", err, test.want)
				}
				if test.flag == os.O_RDONLY && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("OpenStateFile error = %v, want os.ErrNotExist", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}()
			info, err := os.Lstat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				t.Fatalf("created %s mode %v, want a regular file 0600", name, info.Mode())
			}
		})
	}
}

// A creating open that loses a same-moment create to another process answers
// "no such file" once and is asked again; an open that creates nothing, a
// directory that stays gone and any other error end where they stand.
func TestOpenCreating(t *testing.T) {
	for _, test := range []struct {
		name      string
		flag      int
		answers   []error
		wantCalls int
		wantErr   error
	}{
		{"a first answer is kept", os.O_CREATE | os.O_RDWR, []error{nil}, 1, nil},
		{"a lost create is asked again", os.O_CREATE | os.O_RDWR, []error{syscall.ENOENT, syscall.ENOENT, nil}, 3, nil},
		{
			"a directory that stays gone ends absent", os.O_CREATE | os.O_RDWR,
			[]error{syscall.ENOENT, syscall.ENOENT, syscall.ENOENT, nil},
			3, syscall.ENOENT,
		},
		{"an open that creates nothing is asked once", os.O_RDONLY, []error{syscall.ENOENT, nil}, 1, syscall.ENOENT},
		{"another error is not asked again", os.O_CREATE | os.O_RDWR, []error{syscall.ELOOP, nil}, 1, syscall.ELOOP},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			fd, err := openCreating(test.flag, func() (int, error) {
				answer := test.answers[calls]
				calls++
				if answer != nil {
					return -1, answer
				}
				return 7, nil
			})
			wantFD := 7
			if test.wantErr != nil {
				wantFD = -1
			}
			if calls != test.wantCalls || err != test.wantErr || fd != wantFD {
				t.Fatalf("openCreating = fd %d, err %v after %d call(s), want fd %d, err %v after %d",
					fd, err, calls, wantFD, test.wantErr, test.wantCalls)
			}
		})
	}
}

// protected makes dir (created when missing) a directory only its owner can write.
func protected(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}
