package harvest

import (
	"errors"
	"strings"
	"testing"
)

func TestTitleGuessResultEchoesGivenValue(t *testing.T) {
	got := titleGuessResult("title:Some Book", "Some Book")
	if got.Source != "title:Some Book" {
		t.Fatalf("Source = %q, want the original source", got.Source)
	}
	if !strings.Contains(got.Error, `"Some Book"`) || !strings.Contains(got.Error, "search_literature") {
		t.Fatalf("Error = %q, want it to echo the value and name search_literature", got.Error)
	}
}

func TestNoteRungOutcomeOnErrorNamesItsKind(t *testing.T) {
	err := errors.New("connection refused")
	gotKind, gotErr := noteRungOutcome(err, nil, "")
	if gotErr != err {
		t.Fatalf("err = %v, want %v", gotErr, err)
	}
	if gotKind != errorKind(err) {
		t.Fatalf("kind = %q, want %q", gotKind, errorKind(err))
	}
}

func TestNoteRungOutcomeOnAnswerKeepsPriorErr(t *testing.T) {
	priorErr := errors.New("earlier failure")
	gotKind, gotErr := noteRungOutcome(nil, priorErr, "connect")
	if gotErr != priorErr || gotKind != "connect" {
		t.Fatalf("err/kind = %v/%q, want the prior values preserved on a reader's answer", gotErr, gotKind)
	}
}

func TestConvertOutageNoteAppendsOnlyWhenNothingMoreSpecificClaimedIt(t *testing.T) {
	base := "base message"
	msg, kind := convertOutageNote(base, "", true, false, false, false)
	if msg == base {
		t.Fatal("convertOutageNote did not append a tool-outage note")
	}
	if kind != errorKindConvert {
		t.Fatalf("kind = %q, want %q", kind, errorKindConvert)
	}

	// A more specific diagnostic (emptyPDFConvert/wrongPDF/appShellFailure)
	// must win: the outage note must not double up on or mask it.
	for _, tc := range []struct {
		name                                       string
		emptyPDFConvert, wrongPDF, appShellFailure bool
	}{
		{"emptyPDFConvert", true, false, false},
		{"wrongPDF", false, true, false},
		{"appShellFailure", false, false, true},
	} {
		msg, kind := convertOutageNote(base, "existing", true, tc.emptyPDFConvert, tc.wrongPDF, tc.appShellFailure)
		if msg != base {
			t.Fatalf("%s: convertOutageNote appended despite a more specific diagnostic: %q", tc.name, msg)
		}
		if kind != "existing" {
			t.Fatalf("%s: kind = %q, want the existing kind untouched", tc.name, kind)
		}
	}

	// Not an outage at all: no change.
	msg, kind = convertOutageNote(base, "kind", false, false, false, false)
	if msg != base || kind != "kind" {
		t.Fatalf("convertOutageNote changed message/kind when outage=false: %q/%q", msg, kind)
	}
}
