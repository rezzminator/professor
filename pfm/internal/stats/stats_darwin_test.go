//go:build darwin

package stats

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestSamplerSampleResourcesUsesDarwinHostCounters(t *testing.T) {
	sampler := &Sampler{}
	first, err := sampler.SampleResources(nil)
	if err != nil {
		t.Fatalf("first Darwin resource sample: %v", err)
	}
	if first.Header.MemoryBytes == 0 {
		t.Fatalf("first Darwin resource sample has no host memory: %#v", first.Header)
	}
	// Darwin's host total is summed from ps per-process lifetime CPU, so an
	// interval in which more CPU exits than accrues carries no honest delta and
	// SampleResources deliberately leaves CPUValid false (see stats.go's own
	// comment at the deltaIdle guard). Under `go test ./...` short-lived test
	// processes churn constantly, so ONE 25ms interval is a coin flip: this test
	// passed on an idle box and failed under full-suite load while the sampler
	// behaved exactly as documented. Sampling until a valid interval appears
	// keeps the assertion strict — a sampler that never reports a delta still
	// fails — while removing the load sensitivity.
	deadline := time.Now().Add(5 * time.Second)
	var second Snapshot
	for {
		time.Sleep(25 * time.Millisecond)
		second, err = sampler.SampleResources(nil)
		if err != nil {
			t.Fatalf("later Darwin resource sample: %v", err)
		}
		if !second.Ready {
			t.Fatalf("later Darwin resource sample not ready: %#v", second)
		}
		if second.Header.CPUValid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"no Darwin resource sample reported a CPU delta within 5s: %#v",
				second.Header,
			)
		}
	}
}

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
	record := make([]byte, 32)
	binary.LittleEndian.PutUint64(record[:8], 7<<30)
	binary.LittleEndian.PutUint64(record[8:16], 2<<30)
	binary.LittleEndian.PutUint64(record[16:24], 5<<30)
	total, used, err := parseDarwinSwap(record)
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

func TestParseDarwinProcessRowsKeepsARowWithAnUnreadableCounter(t *testing.T) {
	processes, busy, warnings := parseDarwinProcessRows(
		"10 1 00:01.00 2048 /bin/good\n11 10 bad 4096 /bin/oddcpu\nxx 1 00:01.00 1 /bin/nopid\n",
	)
	if len(processes) != 2 || processes[10].jiffies != 100 || processes[11].jiffies != 0 ||
		processes[11].parent != 10 || processes[11].rss != 4096*1024 || busy != 100 {
		t.Fatalf("processes = %#v busy=%d", processes, busy)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %q, want one for the kept row and one for the dropped row", warnings)
	}
}
