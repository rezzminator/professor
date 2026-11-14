package gather

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// HostCPU sums a jail's first stat line, idle plus iowait as idle; a record
// that does not parse is named.
func TestHostCPUReadsAJailStat(t *testing.T) {
	for _, tc := range []struct {
		stat, wantErr string
		want          CPU
	}{
		{stat: "cpu 1 2 3 4 5 6 7\ncpu0 1 2 3 4 5 6 7\n", want: CPU{Total: 28, Idle: 9}},
		{stat: "cpu 1 2 3\n", wantErr: "read host CPU counters: malformed first line"},
		{stat: "cpu x 0 0 0 0\n", wantErr: `parse host CPU counter "x": strconv.ParseUint: parsing "x": invalid syntax`},
	} {
		root := t.TempDir()
		writeProcFixture(t, filepath.Join(root, "stat"), tc.stat)
		got, err := HostCPU(root)
		if errorText(err) != tc.wantErr || got != tc.want {
			t.Errorf("HostCPU over %q = %+v, %v; want %+v, %q", tc.stat, got, err, tc.want, tc.wantErr)
		}
	}
}

// HostCPUPressure reads avg10; no PSI file is the read's fs.ErrNotExist, and a
// record with no avg10 reads 0.
func TestHostCPUPressureReadsAvg10(t *testing.T) {
	for _, tc := range []struct {
		pressure, wantErr string
		want              float64
	}{
		{pressure: "some avg10=7.25 avg60=0.00 avg300=0.00 total=1\n", want: 7.25},
		{pressure: "some avg60=0.00\n", want: 0},
		{pressure: "some avg10=zz avg60=0.00\n", wantErr: `parse CPU pressure avg10: strconv.ParseFloat: parsing "zz": invalid syntax`},
	} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "pressure"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeProcFixture(t, filepath.Join(root, "pressure", "cpu"), tc.pressure)
		got, err := HostCPUPressure(root)
		if errorText(err) != tc.wantErr || got != tc.want {
			t.Errorf("HostCPUPressure over %q = %v, %v; want %v, %q", tc.pressure, got, err, tc.want, tc.wantErr)
		}
	}
	root := t.TempDir()
	if _, err := HostCPUPressure(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("HostCPUPressure(%s) without PSI = %v; want fs.ErrNotExist", root, err)
	}
}
