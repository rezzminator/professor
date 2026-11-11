package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// HostMemory reads a jail's meminfo into kilobytes; a MemAvailable it cannot
// read is named in AvailableErr, never a 0 reading, while the other counters
// stay; an absent meminfo is the read's fs.ErrNotExist.
func TestHostMemoryReadsAJailMeminfo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		meminfo string
		want    func(path string) Memory
	}{
		{
			name: "every counter",
			meminfo: "MemTotal:       16384000 kB\nMemAvailable:    8192000 kB\n" +
				"SwapTotal:       2097148 kB\nSwapFree:        2097148 kB\n",
			want: func(string) Memory {
				return Memory{TotalKB: 16384000, AvailableKB: 8192000, SwapTotalKB: 2097148, SwapFreeKB: 2097148}
			},
		},
		{
			name:    "no MemAvailable line",
			meminfo: "MemTotal: 16384000 kB\n",
			want: func(path string) Memory {
				return Memory{TotalKB: 16384000, AvailableErr: fmt.Errorf("no MemAvailable line in %s", path)}
			},
		},
		{
			name:    "MemAvailable without a value",
			meminfo: "MemTotal: 16384000 kB\nMemAvailable:\n",
			want: func(path string) Memory {
				return Memory{TotalKB: 16384000, AvailableErr: fmt.Errorf("parse MemAvailable line %q in %s", "MemAvailable:", path)}
			},
		},
		{
			name:    "MemAvailable not a number",
			meminfo: "MemTotal: x kB\nMemAvailable: y kB\n",
			want: func(path string) Memory {
				return Memory{AvailableErr: fmt.Errorf(
					"parse MemAvailable value in %s: %w", path, errors.New(`strconv.ParseUint: parsing "y": invalid syntax`),
				)}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "meminfo")
			writeProcFixture(t, path, tc.meminfo)
			got, err := HostMemory(root)
			want := tc.want(path)
			if err != nil || !reflect.DeepEqual(errorText(got.AvailableErr), errorText(want.AvailableErr)) {
				t.Fatalf("HostMemory(%s) = %+v, %v; want %+v", root, got, err, want)
			}
			got.AvailableErr, want.AvailableErr = nil, nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("HostMemory(%s) = %+v; want %+v (fixture %s)", root, got, want, path)
			}
		})
	}
	root := t.TempDir()
	if got, err := HostMemory(root); !errors.Is(err, fs.ErrNotExist) || !reflect.DeepEqual(got, Memory{}) {
		t.Fatalf("HostMemory(%s) without meminfo = %+v, %v; want fs.ErrNotExist", root, got, err)
	}
	if err := os.Mkdir(filepath.Join(root, "meminfo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := HostMemory(root); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("HostMemory(%s) over a directory = %v; want a read failure, not absence", root, err)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
