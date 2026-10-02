package engine

import "testing"

func TestClaudeTrustDialog(t *testing.T) {
	t.Parallel()
	const dialog = " Accessing workspace:\n\n /work/alpha\n\n" +
		" Quick safety check: Is this a project you created or one you trust?\n\n" +
		" ❯ 1. Yes, I trust this folder\n   2. No, exit\n\n Enter to confirm · Esc to cancel\n"
	tests := []struct {
		name    string
		capture string
		want    bool
	}{
		{"the dialog with its exit row", dialog, true},
		{
			"the dialog's continue-without-permissions variant",
			" Accessing workspace:\n ❯ 1. Yes, I trust this folder\n   2. No, continue without these permissions\n",
			true,
		},
		{"an idle composer", "Claude\n❯ \n  ⏵⏵ bypass permissions on", false},
		{
			"a composer whose prompt only mentions the folder",
			"Claude\n❯ please say whether you trust this folder\n",
			false,
		},
		{
			"a transcript quoting the confirm row alone",
			"● the dialog offers \"Yes, I trust this folder\" on its first row\n❯ \n",
			false,
		},
		{"an empty pane", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ClaudeTrustDialog(test.capture); got != test.want {
				t.Fatalf("ClaudeTrustDialog = %t, want %t", got, test.want)
			}
		})
	}
}
