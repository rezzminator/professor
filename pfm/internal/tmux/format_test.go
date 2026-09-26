package tmux

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFormatSplitAcceptsBothTmuxSeparatorSpellingsAtEveryCallSiteArity(t *testing.T) {
	for _, separator := range []struct {
		name  string
		value string
	}{
		{name: "escaped_backslash_037", value: Escaped},
		{name: "raw_0x1f", value: Separator},
	} {
		for _, count := range []int{10, 6, 5, 4, 3} {
			separator := separator
			count := count
			t.Run(fmt.Sprintf("%s/%d_fields", separator.name, count), func(t *testing.T) {
				want := make([]string, count)
				for i := range want {
					want[i] = fmt.Sprintf("field-%d", i+1)
				}

				line := strings.Join(want, separator.value)
				if got := FormatSplit(line, count); !reflect.DeepEqual(got, want) {
					t.Fatalf("FormatSplit(%q, %d) = %#v, want %#v", line, count, got, want)
				}
			})
		}
	}
}

func TestFormatSplitTreatsEscapedSpellingInsideFieldContentAsASeparator(t *testing.T) {
	line := "title" + Escaped + "with-content" + Escaped + "socket"
	want := []string{"title", "with-content", "socket"}

	if got := FormatSplit(line, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("FormatSplit(%q, 3) = %#v, want %#v", line, got, want)
	}
}

func TestFormatSplitDegenerateRecordsAndSplitCapAreStable(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		count int
		want  []string
	}{
		{name: "empty_line_is_one_empty_field", line: "", count: 3, want: []string{""}},
		{name: "single_field_line_stays_single", line: "only", count: 3, want: []string{"only"}},
		{
			name:  "more_separators_than_count_remain_in_the_final_field",
			line:  strings.Join([]string{"one", "two", "three", "four"}, Separator),
			count: 3,
			want:  []string{"one", "two", "three" + Separator + "four"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatSplit(tt.line, tt.count); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FormatSplit(%q, %d) = %#v, want %#v", tt.line, tt.count, got, tt.want)
			}
		})
	}
}

func TestFormatJoinRoundTripsThroughFormatSplit(t *testing.T) {
	want := []string{"#{session_name}", "#{pane_id}", "#{pane_current_path}", "#{pane_pid}"}
	line := FormatJoin(want...)

	if got := FormatSplit(line, len(want)); !reflect.DeepEqual(got, want) {
		t.Fatalf("FormatSplit(FormatJoin(fields...), %d) = %#v, want %#v", len(want), got, want)
	}
}
