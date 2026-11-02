package gather

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func TestParseDarwinCPUTime(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  uint64
	}{
		{value: "00:01.25", want: 125},
		{value: "103:20.39", want: 620039},
		{value: "1:02:03.04", want: 372304},
		{value: "2-01:02:03.04", want: 17652304},
	} {
		got, err := parseDarwinCPUTime(testCase.value)
		if err != nil || got != testCase.want {
			t.Errorf("parseDarwinCPUTime(%q) = %d, %v; want %d", testCase.value, got, err, testCase.want)
		}
	}
}

func TestParseDarwinSwapXSWUsage(t *testing.T) {
	total, used, err := parseDarwinSwap(xswUsage(7<<30, 2<<30, 5<<30))
	if err != nil || total != 7<<30 || used != 5<<30 {
		t.Fatalf("parseDarwinSwap() = total:%d used:%d err:%v", total, used, err)
	}
}

func TestParseDarwinVMStatSkipsOneBadCounter(t *testing.T) {
	output := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n" +
		"Pages free:                               100.\n" +
		"Pages inactive:                           xx.\n" +
		"Pages purgeable:                          7.\n"
	pageSize, pages, warnings, err := parseDarwinVMStat(output)
	if err != nil || pageSize != 16384 || len(pages) != 2 || pages["Pages free"] != 100 ||
		pages["Pages purgeable"] != 7 {
		t.Fatalf("parseDarwinVMStat() = %d %v %v err=%v", pageSize, pages, warnings, err)
	}
	want := `parse Darwin vm_stat "Pages inactive": strconv.ParseUint: parsing "xx": invalid syntax; counter skipped`
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", warnings, want)
	}
}

// One ps snapshot serves RSS, CPU time and the terminal: a row whose CPU will
// not parse keeps its place with that reading unknown, a row with no pid is
// dropped, and "??" is no terminal.
func TestParsePSSnapshotKeepsARowWithAnUnreadableCounter(t *testing.T) {
	rows, warnings := parsePSSnapshot(
		"10 1 00:01.00 2048 ttys001 /bin/good arg\n" +
			"11 10 bad 4096 ?? /bin/oddcpu\n" +
			"xx 1 00:01.00 1 ?? /bin/nopid\n" +
			"12 1 00:01.00\n",
	)
	want := map[int]psRow{
		10: {
			counter: ProcessCounter{Parent: 1, CPUTicks: 100, RSSBytes: 2048 * 1024, Command: "/bin/good arg"},
			tty:     "ttys001",
		},
		11: {counter: ProcessCounter{Parent: 10, CPUUnknown: true, RSSBytes: 4096 * 1024, Command: "/bin/oddcpu"}},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	wantWarnings := []string{
		`parse Darwin process row "11 10 bad 4096 ?? /bin/oddcpu": cpu=unexpected CPU time "bad" rss=<nil>; ` +
			`kept with that reading unknown`,
		`parse Darwin process row "xx 1 00:01.00 1 ?? /bin/nopid": pid=strconv.Atoi: parsing "xx": invalid syntax ` +
			`parent=<nil>`,
		`parse Darwin process row: short record "12 1 00:01.00"`,
	}
	if !reflect.DeepEqual(warnings, wantWarnings) {
		t.Fatalf("warnings = %q, want %q", warnings, wantWarnings)
	}
}

// A snapshot whose rows all fail to parse is an error naming why, never an
// empty process table; one good row is a table.
func TestSnapshotTableRefusesASnapshotWithNoParseableRow(t *testing.T) {
	rows, err := snapshotTable("12 1 00:01.00\n")
	if want := `ps snapshot held no parseable process row: parse Darwin process row: short record "12 1 00:01.00"`; rows != nil ||
		err == nil ||
		err.Error() != want {
		t.Fatalf("snapshotTable() = %#v, %v; want nil, %q", rows, err, want)
	}
	rows, err = snapshotTable("12 1 00:01.00\n10 1 00:01.00 2048 ?? /bin/good\n")
	want := map[int]psRow{
		10: {counter: ProcessCounter{Parent: 1, CPUTicks: 100, RSSBytes: 2048 * 1024, Command: "/bin/good"}},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("snapshotTable() = %#v, %v; want %#v", rows, err, want)
	}
}

// Available memory is the free, inactive, speculative and purgeable pages,
// capped at the total; an unreadable swap record is SwapErr beside a memory
// reading that stands.
func TestDarwinMemoryComposesTheCounters(t *testing.T) {
	vmStat := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n" +
		"Pages free:      100.\nPages inactive:  50.\nPages speculative: 25.\nPages purgeable: 25.\nPages wired down: 9.\n"
	for _, tc := range []struct {
		name    string
		total   uint64
		swap    []byte
		swapErr error
		want    Memory
		wantErr string
	}{
		{
			name: "every counter", total: 16 << 30, swap: xswUsage(4<<30, 3<<30, 1<<30),
			want: Memory{TotalKB: 16 << 20, AvailableKB: 200 * 16, SwapTotalKB: 4 << 20, SwapFreeKB: 3 << 20},
		},
		{
			name: "available capped at the total", total: 1 << 20, swap: xswUsage(0, 0, 0),
			want: Memory{TotalKB: 1 << 10, AvailableKB: 1 << 10},
		},
		{
			name: "swap unread", total: 16 << 30, swapErr: errors.New("sysctl refused"),
			want:    Memory{TotalKB: 16 << 20, AvailableKB: 200 * 16},
			wantErr: "read Darwin swap counters: sysctl refused",
		},
		{
			name: "swap record short", total: 16 << 30, swap: make([]byte, 8),
			want:    Memory{TotalKB: 16 << 20, AvailableKB: 200 * 16},
			wantErr: "parse Darwin swap counters: short xsw_usage record (8 bytes)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := darwinMemory(tc.total, vmStat, tc.swap, tc.swapErr)
			if err != nil || errorText(got.SwapErr) != tc.wantErr {
				t.Fatalf("darwinMemory() = %+v, %v; want SwapErr %q", got, err, tc.wantErr)
			}
			got.SwapErr = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("darwinMemory() = %+v, want %+v", got, tc.want)
			}
		})
	}
	if _, err := darwinMemory(16<<30, "garbage\n", nil, nil); err == nil ||
		err.Error() != `parse Darwin vm_stat page size: malformed header "garbage"` {
		t.Fatalf("darwinMemory over a malformed vm_stat = %v", err)
	}
}

// xswUsage is a vm.swapusage record: total, available, used, in bytes.
func xswUsage(total, available, used uint64) []byte {
	record := make([]byte, 32)
	binary.LittleEndian.PutUint64(record[:8], total)
	binary.LittleEndian.PutUint64(record[8:16], available)
	binary.LittleEndian.PutUint64(record[16:24], used)
	return record
}
