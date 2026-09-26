package callmeter

import (
	"encoding/json"
	"testing"
)

func TestDeliveredBytes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "string", raw: `"hello world"`, want: 11},
		{name: "empty string", raw: `""`, want: 0},
		{
			name: "content block array, text summed",
			raw:  `[{"type":"text","text":"abc"},{"type":"text","text":"de"}]`,
			want: 5,
		},
		{
			name: "non-text block contributes nothing",
			raw:  `[{"type":"text","text":"abc"},{"type":"image","text":"ignored"}]`,
			want: 3,
		},
		{name: "empty array", raw: `[]`, want: 0},
		{name: "null", raw: `null`, want: 0},
		{name: "empty raw message", raw: ``, want: 0},
		{name: "object is unmeasurable", raw: `{"foo":"bar"}`, wantErr: true},
		{name: "number is unmeasurable", raw: `42`, wantErr: true},
		{name: "boolean is unmeasurable", raw: `true`, wantErr: true},
		{name: "malformed string errors", raw: `"unterminated`, wantErr: true},
		{name: "malformed array errors", raw: `[{"text":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DeliveredBytes(json.RawMessage(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DeliveredBytes(%q) = %d, nil; want an error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeliveredBytes(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("DeliveredBytes(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}
