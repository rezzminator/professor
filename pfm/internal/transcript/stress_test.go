package transcript

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFlattenPromptTextStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the prompt-flatten stress battery")
	}

	stressLargeString(t)
	stressLargeArray(t)
	stressDeepNesting(t)
	stressInvalidUTF8(t)
	stressLinearScaling(t)
}

func stressLargeString(t *testing.T) {
	t.Helper()

	const size = 10 << 20
	input := make([]byte, size+2)
	input[0] = '"'
	for index := 1; index <= size; index++ {
		input[index] = 'x'
	}
	input[len(input)-1] = '"'

	started := time.Now()
	output := FlattenPromptText(json.RawMessage(input))
	elapsed := time.Since(started)
	if len(output) != size || output[0] != 'x' || output[len(output)-1] != 'x' {
		t.Fatalf("10 MiB string output has length %d or corrupt edges", len(output))
	}
	t.Logf("10 MiB JSON string flattened in %s", elapsed.Round(time.Millisecond))
}

func stressLargeArray(t *testing.T) {
	t.Helper()

	const elements = 100000
	var input strings.Builder
	input.Grow(elements*27 + 2)
	input.WriteByte('[')
	for index := 0; index < elements; index++ {
		if index != 0 {
			input.WriteByte(',')
		}
		input.WriteString(`{"type":"text","text":"x"}`)
	}
	input.WriteByte(']')

	started := time.Now()
	output := FlattenPromptText(json.RawMessage(input.String()))
	elapsed := time.Since(started)
	if got, want := len(output), elements*2-1; got != want {
		t.Fatalf("100k text block output length = %d, want %d", got, want)
	}
	t.Logf("100k-element content array flattened in %s", elapsed.Round(time.Millisecond))
}

func stressDeepNesting(t *testing.T) {
	t.Helper()

	const depth = 20000
	input := make([]byte, depth*2+1)
	for index := 0; index < depth; index++ {
		input[index] = '['
		input[len(input)-1-index] = ']'
	}
	input[depth] = '0'

	started := time.Now()
	output := FlattenPromptText(json.RawMessage(input))
	elapsed := time.Since(started)
	if output != "" {
		t.Fatalf("deep non-text nesting output = %q, want empty", output)
	}
	t.Logf("%d-level nesting rejected safely in %s", depth, elapsed.Round(time.Millisecond))
}

func stressInvalidUTF8(t *testing.T) {
	t.Helper()

	input := []byte{'"', 0xff, 0xfe, 'x', 0xc0, 0xaf, '"'}
	output := FlattenPromptText(json.RawMessage(input))
	if !utf8.ValidString(output) {
		t.Fatalf("invalid UTF-8 input produced invalid output: %x", output)
	}
	if output == "" {
		t.Fatal("invalid UTF-8 JSON string unexpectedly produced no replacement text")
	}
	t.Logf("invalid UTF-8 replaced safely with %d output bytes", len(output))
}

func stressLinearScaling(t *testing.T) {
	t.Helper()

	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	oneMiB := quotedASCII(1 << 20)
	fourMiB := quotedASCII(4 << 20)
	small := bestFlattenDuration(oneMiB, 3)
	large := bestFlattenDuration(fourMiB, 3)
	if strict && large > small*8+250*time.Millisecond {
		t.Fatalf(
			"flatten scaling is not roughly linear: 1 MiB=%s 4 MiB=%s",
			small,
			large,
		)
	}
	if !strict && large > 30*time.Second {
		t.Fatalf("4 MiB flatten sanity duration = %s, want <30s", large)
	}
	t.Logf(
		"linear scaling probe (best of 3): 1 MiB=%s, 4 MiB=%s, ratio=%.2fx, strict=%t",
		small.Round(time.Microsecond),
		large.Round(time.Microsecond),
		float64(large)/float64(small),
		strict,
	)
}

func quotedASCII(size int) []byte {
	input := make([]byte, size+2)
	input[0] = '"'
	for index := 1; index <= size; index++ {
		input[index] = 'x'
	}
	input[len(input)-1] = '"'
	return input
}

func bestFlattenDuration(input []byte, attempts int) time.Duration {
	best := time.Duration(1<<63 - 1)
	for range attempts {
		started := time.Now()
		_ = FlattenPromptText(json.RawMessage(input))
		best = min(best, time.Since(started))
	}
	return best
}
