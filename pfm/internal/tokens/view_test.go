package tokens

import (
	"reflect"
	"strings"
	"testing"
)

func TestRound2(t *testing.T) {
	if got := round2All([]float64{1.2345, 0, 7}); !reflect.DeepEqual(got, []float64{1.23, 0, 7}) {
		t.Errorf("round2All = %v", got)
	}
}

func TestBucketizeKeepsThePeakAndSumsTheMoney(t *testing.T) {
	row := func(ctx, money float64) []float64 {
		values := []float64{ctx}
		for range findingCount {
			values = append(values, money)
		}
		return values
	}
	short := bucketize([][]float64{row(10, 1.4), row(5, 2.6)})
	if len(short) != 2 || short[0].([]float64)[0] != 10 || short[1].([]float64)[1] != 3 {
		t.Errorf("two calls = %v, want one bucket each, money rounded", short)
	}
	var long [][]float64
	for i := range 160 {
		long = append(long, row(float64(i), 1))
	}
	buckets := bucketize(long)
	if len(buckets) != 80 {
		t.Fatalf("160 calls fold into %d buckets, want 80", len(buckets))
	}
	if first := buckets[0].([]float64); first[0] != 1 || first[1] != 2 || len(first) != findingCount+1 {
		t.Errorf("first bucket = %v, want peak 1 and each column summed over two calls", first)
	}
	if got := bucketize(nil); len(got) != 0 {
		t.Errorf("no calls = %v, want no bucket", got)
	}
}

func TestViewTextStripsTheProjectAndCutsLongText(t *testing.T) {
	long := strings.Repeat("y", 70)
	cases := []struct{ text, want string }{
		{"/w/demo/.worktrees/f1/pfm/x.go", "pfm/x.go"},
		{"/w/other/x.go", "/w/other/x.go"},
		{"go test " + long, utf16Slice("go test "+long, 0, 63) + "…"},
		{"/w/demo/" + long + "z", "…" + utf16Tail(long+"z", 63)},
	}
	for _, c := range cases {
		if got := viewText(c.text, "/w/demo"); got != c.want {
			t.Errorf("viewText(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
