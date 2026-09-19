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
	if !strings.Contains(got.Error, `"Some Book"`) || !strings.Contains(got.Error, "findWorks") {
		t.Fatalf("Error = %q, want it to echo the value and name findWorks", got.Error)
	}
}

func TestNoteRungOutcomeOnErrorOverwritesKindButKeepsLastKnownStatus(t *testing.T) {
	err := errors.New("connection refused")
	gotKind, gotStatus, gotErr := noteRungOutcome(err, 0, nil, "", 403)
	if gotErr != err {
		t.Fatalf("err = %v, want %v", gotErr, err)
	}
	if gotKind != errorKind(err) {
		t.Fatalf("kind = %q, want %q", gotKind, errorKind(err))
	}
	if gotStatus != 403 {
		t.Fatalf("status = %d, want the prior status preserved (getBody reports 0 on every transport error)", gotStatus)
	}
}

func TestNoteRungOutcomeOnSuccessUpdatesStatusAndKeepsPriorErr(t *testing.T) {
	priorErr := errors.New("earlier failure")
	gotKind, gotStatus, gotErr := noteRungOutcome(nil, 200, priorErr, "connect", 500)
	if gotErr != priorErr || gotKind != "connect" {
		t.Fatalf("err/kind = %v/%q, want the prior values preserved on a successful rung", gotErr, gotKind)
	}
	if gotStatus != 200 {
		t.Fatalf("status = %d, want the new status", gotStatus)
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
