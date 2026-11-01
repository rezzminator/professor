package obs

import (
	"fmt"
	"testing"
)

func TestWarnLinesRecordsEveryLineAsAWarning(t *testing.T) {
	ctx, recorder := Test(t)
	writer := WarnLines(ctx, "statusline.warn")
	if _, err := fmt.Fprint(writer, "first cause\n\nsecond cause\n"); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2: %s", len(records), recorder.Raw())
	}
	for i, want := range []string{"first cause", "second cause"} {
		got, _ := records[i].Field(FieldErr)
		if records[i].Level != "WARN" || records[i].Message != "statusline.warn" || got != want {
			t.Fatalf("record %d = %+v, want WARN statusline.warn %q", i, records[i], want)
		}
	}
}
