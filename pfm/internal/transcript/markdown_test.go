package transcript

import "testing"

func TestMarkdownGivesEachRoleItsHeading(t *testing.T) {
	got := Markdown([]Entry{
		{Role: RoleUser, Text: "hello"},
		{Role: RoleAssistant, Text: "hi"},
		{Role: RoleSummary, Text: "so far"},
		{Role: RoleTool, Tool: "Bash"},
	})
	want := "## USER\n\nhello\n\n## ASSISTANT\n\nhi\n\n## [COMPACTION SUMMARY]\n\nso far\n\n> [tools: Bash]\n\n"
	if got != want {
		t.Fatalf("Markdown() = %q, want %q", got, want)
	}
}

func TestLastLinesKeepsOnlyTheTail(t *testing.T) {
	for _, testCase := range []struct {
		value string
		count int
		want  string
	}{
		{"a\nb\nc", 2, "b\nc"},
		{"a\nb", 5, "a\nb"},
	} {
		if got := LastLines(testCase.value, testCase.count); got != testCase.want {
			t.Fatalf("LastLines(%q, %d) = %q, want %q", testCase.value, testCase.count, got, testCase.want)
		}
	}
}
