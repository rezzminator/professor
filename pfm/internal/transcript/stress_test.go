package transcript

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
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

	output := FlattenPromptText(json.RawMessage(input))
	if len(output) != size || output[0] != 'x' || output[len(output)-1] != 'x' {
		t.Fatalf("10 MiB string output has length %d or corrupt edges", len(output))
	}
	t.Logf("10 MiB JSON string flattened")
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

	output := FlattenPromptText(json.RawMessage(input.String()))
	if got, want := len(output), elements*2-1; got != want {
		t.Fatalf("100k text block output length = %d, want %d", got, want)
	}
	t.Logf("100k-element content array flattened")
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

	output := FlattenPromptText(json.RawMessage(input))
	if output != "" {
		t.Fatalf("deep non-text nesting output = %q, want empty", output)
	}
	t.Logf("%d-level nesting rejected safely", depth)
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

	for _, size := range []int{1 << 20, 4 << 20} {
		output := FlattenPromptText(json.RawMessage(quotedASCII(size)))
		if len(output) != size {
			t.Fatalf("%d-byte string flattened to %d bytes", size, len(output))
		}
	}
	t.Logf("1 MiB and 4 MiB strings flattened whole")
}

// BenchmarkFlattenPromptTextScaling is the cost of one flatten at 1 MiB and
// 4 MiB: the pair a linear-scaling reading compares.
func BenchmarkFlattenPromptTextScaling(b *testing.B) {
	for _, size := range []int{1 << 20, 4 << 20} {
		input := json.RawMessage(quotedASCII(size))
		b.Run(strconv.Itoa(size>>20)+"MiB", func(b *testing.B) {
			for b.Loop() {
				_ = FlattenPromptText(input)
			}
		})
	}
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
