package compose

import "testing"

func TestDefaultEligibleSuppressesOnlyAnUnusedResumableClaudeRow(t *testing.T) {
	tests := []struct {
		name string
		row  Row
		want bool
	}{
		{
			name: "zero prompt transcript",
			row:  Row{Kind: ResumeClaude, Size: 4096, PromptCount: 0},
			want: false,
		},
		{
			name: "answered transcript",
			row:  Row{Kind: ResumeClaude, Size: 4096, PromptCount: 3},
			want: true,
		},
		{
			name: "live claude seat before its first prompt",
			row:  Row{Kind: LiveClaude, Name: "GATEWAY", Socket: "cc-1-2-3"},
			want: true,
		},
		{
			name: "live background claude seat",
			row:  Row{Kind: LiveClaude, BG: true, Size: 4096, PromptCount: 3},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := defaultEligible(test.row); got != test.want {
				t.Fatalf("defaultEligible(%+v) = %t, want %t", test.row, got, test.want)
			}
		})
	}
}
