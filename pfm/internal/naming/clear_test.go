package naming

import "testing"

func TestClearSuccessorName(t *testing.T) {
	const id = "22222222-2222-4222-8222-222222222222"
	for _, test := range []struct {
		original, id, want string
		occupied           []string
		bad                bool
	}{
		{original: "Desk", id: id, want: "Desk #2"},
		{original: "Desk #2", id: id, want: "Desk #3"},
		{original: "Desk #3", id: id, want: "Desk #2"},
		{original: "Desk #2", id: id, want: "Desk #4", occupied: []string{"Desk #2", "Desk #3", "Other #4"}},
		{original: "Desk", id: id, want: "Desk #3", occupied: []string{"Desk #2", "Desk #4"}},
		{original: "Desk #draft", id: id, want: "Desk #draft #2"},
		{original: "Desk #1", id: id, want: "Desk #2"},
		{original: "", id: id},
		{original: "  ", id: id},
		{original: "Desk", id: "../bad", bad: true},
		{original: "Desk\nnext", id: id, bad: true},
	} {
		got, err := ClearSuccessorName(test.original, test.id, test.occupied...)
		if got != test.want || (err != nil) != test.bad {
			t.Errorf("ClearSuccessorName(%q,%q,%v)=%q,%v want %q error=%v",
				test.original, test.id, test.occupied, got, err, test.want, test.bad)
		}
	}
	pending := ClearName{Inherited: "Desk", Name: "Fresh"}
	for _, test := range []struct{ current, want string }{{"Desk", "Fresh"}, {"", "Fresh"}, {"Manual", "Manual"}, {"Fresh", "Fresh"}} {
		if got := pending.Title(test.current); got != test.want {
			t.Errorf("Title(%q)=%q want %q", test.current, got, test.want)
		}
	}
	pending.Confirmed = true
	if got := pending.Title("Desk"); got != "Desk" {
		t.Fatalf("confirmed name overrode deliberate rename: %q", got)
	}
}

func TestClearNameValidatesAllocatedNumberNotCurrentAvailability(t *testing.T) {
	const id = "22222222-2222-4222-8222-222222222222"
	for _, test := range []struct {
		original, chosen string
		bad              bool
	}{
		{"Desk", "Desk #2", false},
		{"Desk #2", "Desk #3", false},
		{"Desk", "Desk #99", false},
		{"Desk", "Other #2", true},
		{"Desk", "Desk #1", true},
		{"Desk #2", "Desk #2", true},
		{"Desk", "Desk #02", true},
		{"", " #2", true},
	} {
		if err := (ClearName{Inherited: test.original, Name: test.chosen}).Validate(id); (err != nil) != test.bad {
			t.Errorf("Validate(%q,%q)=%v want error=%v", test.original, test.chosen, err, test.bad)
		}
	}
}

func TestClearObservedTitleStopsOverride(t *testing.T) {
	for _, current := range []string{"Fresh", "Manual"} {
		decision := ClearName{Inherited: "Desk", Name: "Fresh"}.ObserveTitle(current)
		if !decision.Confirmed || decision.Title("Desk") != "Desk" {
			t.Fatalf("observed %q did not protect later user rename: %+v", current, decision)
		}
	}
	for _, current := range []string{"", "Desk"} {
		if decision := (ClearName{Inherited: "Desk", Name: "Fresh"}).ObserveTitle(current); decision.Confirmed {
			t.Fatalf("inherited %q confirmed output: %+v", current, decision)
		}
	}
}
