package paths

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTempRootFrom(t *testing.T) {
	for _, row := range []struct {
		name   string
		values map[string]string
		want   string
	}{
		{"unset falls back to the system temp directory", nil, "/tmp"},
		{"empty falls back to the system temp directory", map[string]string{EnvTmpDir: ""}, "/tmp"},
		{"set is honoured, trailing slash dropped", map[string]string{EnvTmpDir: "/var/folders/ab/T/"}, "/var/folders/ab/T"},
		{"root stays root", map[string]string{EnvTmpDir: "/"}, "/"},
	} {
		t.Run(row.name, func(t *testing.T) {
			if got := TempRootFrom(&MapEnv{Values: row.values}); got != row.want {
				t.Fatalf("TempRootFrom(%v) = %q, want %q", row.values, got, row.want)
			}
		})
	}
}

func TestTempBaseKeepsAJailInsideItself(t *testing.T) {
	if got := TempBase("/jail/seat"); got != "/jail/seat/tmp" {
		t.Fatalf("TempBase(jail) = %q, want /jail/seat/tmp", got)
	}
	t.Setenv(EnvTmpDir, "/scratch/t")
	if got := TempBase(""); got != "/scratch/t" {
		t.Fatalf("TempBase(\"\") = %q, want TMPDIR /scratch/t", got)
	}
}

func TestTempDirDedicatesAPrivateDirectoryPerPurpose(t *testing.T) {
	root := t.TempDir()
	got, err := tempDirUnder(root, "update-doctor")
	if err != nil {
		t.Fatalf("tempDirUnder: %v", err)
	}
	want := filepath.Join(root, "pfm-"+strconv.Itoa(os.Getuid()), "update-doctor")
	if got != want {
		t.Fatalf("tempDirUnder = %q, want %q", got, want)
	}
	for _, directory := range []string{filepath.Dir(want), want} {
		info, err := os.Lstat(directory)
		if err != nil {
			t.Fatalf("stat %s: %v", directory, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %v, want a 0700 directory", directory, info.Mode())
		}
	}
	if again, err := tempDirUnder(root, "update-doctor"); err != nil || again != want {
		t.Fatalf("second tempDirUnder = %q, %v; want %q, nil", again, err, want)
	}
	run, err := mkdirTempUnder(root, "update-doctor", "run-")
	if err != nil {
		t.Fatalf("mkdirTempUnder: %v", err)
	}
	if filepath.Dir(run) != want || !strings.HasPrefix(filepath.Base(run), "run-") {
		t.Fatalf("mkdirTempUnder = %q, want run-* directly inside %q", run, want)
	}
	if _, err := mkdirTempUnder(root, "../escape", "run-"); err == nil {
		t.Fatalf("mkdirTempUnder accepted purpose ../escape, want the purpose error")
	}
}

func TestTempDirTightensALooseDirectoryOfOurs(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "pfm-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(owner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(owner, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tempDirUnder(root, "seed"); err != nil {
		t.Fatalf("tempDirUnder: %v", err)
	}
	info, err := os.Lstat(owner)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("%s mode = %v, want 0700", owner, info.Mode().Perm())
	}
}

func TestTempDirRefuses(t *testing.T) {
	for _, purpose := range []string{"", ".", "..", "a/b", "../escape"} {
		if got, err := tempDirUnder(t.TempDir(), purpose); err == nil {
			t.Errorf("tempDirUnder(%q) = %q, nil; want a refusal", purpose, got)
		}
	}
	root := t.TempDir()
	elsewhere := t.TempDir()
	owner := filepath.Join(root, "pfm-"+strconv.Itoa(os.Getuid()))
	if err := os.Symlink(elsewhere, owner); err != nil {
		t.Fatal(err)
	}
	got, err := tempDirUnder(root, "seed")
	want := "temp directory " + owner + " is not a real directory"
	if err == nil || err.Error() != want {
		t.Fatalf("tempDirUnder under a symlinked owner dir = %q, %v; want error %q", got, err, want)
	}
}

func TestTempRootsFromNamesEverySpelling(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "tmpdir")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	// macOS's other spelling of a path: /private/x for /x, /x for /private/x.
	alias := func(path string) string {
		if public, ok := strings.CutPrefix(path, "/private/"); ok {
			return "/" + public
		}
		return "/private" + path
	}
	physicalReal := PhysicalPath(realDir)
	physicalTmp := PhysicalPath("/tmp")
	var want []string
	seen := map[string]bool{}
	for _, path := range []string{
		link, alias(link), physicalReal, alias(physicalReal),
		"/tmp", "/private/tmp", physicalTmp, alias(physicalTmp),
	} {
		if !seen[path] {
			seen[path] = true
			want = append(want, path)
		}
	}
	got := TempRootsFrom(&MapEnv{Values: map[string]string{EnvTmpDir: link}})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TempRootsFrom(TMPDIR=%s) = %q, want %q", link, got, want)
	}
	unset := TempRootsFrom(&MapEnv{})
	if roots := TempRootsFrom(&MapEnv{Values: map[string]string{EnvTmpDir: "/"}}); !reflect.DeepEqual(roots, unset) {
		t.Fatalf("TempRootsFrom(TMPDIR=/) = %q, want %q (TMPDIR unset): / would make every path scratch", roots, unset)
	}
}

// TestTmuxDirUnder: the socket directory tmux -L derives from TMUX_TMPDIR,
// the system temp directory standing in for an empty one.
func TestTmuxDirUnder(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	for tmuxTmpDir, want := range map[string]string{
		"":         "/tmp/tmux-" + uid,
		"/srv/tmx": "/srv/tmx/tmux-" + uid,
	} {
		if got := TmuxDirUnder(tmuxTmpDir); got != want {
			t.Errorf("TmuxDirUnder(%q) = %q, want %q", tmuxTmpDir, got, want)
		}
	}
}
