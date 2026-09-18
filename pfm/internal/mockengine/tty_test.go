package mockengine

import (
	"reflect"
	"testing"
)

func TestKeyDecoderSeparatesTextEnterEscapeAndControlKeys(t *testing.T) {
	var decoder keyDecoder
	got := decoder.feed([]byte("hi\r"))
	want := []keyEvent{{kind: keyRune, r: 'h'}, {kind: keyRune, r: 'i'}, {kind: keyEnter}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("feed(hi\\r) = %+v, want %+v", got, want)
	}
	// A CSI sequence (an arrow key) is swallowed whole, even split across reads.
	if got := decoder.feed([]byte("\x1b[")); len(got) != 0 {
		t.Fatalf("a half CSI produced %+v", got)
	}
	if got := decoder.feed([]byte("Ax")); !reflect.DeepEqual(got, []keyEvent{{kind: keyRune, r: 'x'}}) {
		t.Fatalf("CSI tail = %+v", got)
	}
	// A lone Escape at the end of a read is the key itself.
	if got := decoder.feed([]byte("\x1b")); !reflect.DeepEqual(got, []keyEvent{{kind: keyEscape}}) {
		t.Fatalf("lone ESC = %+v", got)
	}
	got = decoder.feed([]byte("\x7f\x08\x13\n"))
	want = []keyEvent{{kind: keyBackspace}, {kind: keyBackspace}, {kind: keyStash}, {kind: keyEnter}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("controls = %+v, want %+v", got, want)
	}
	// Multi-byte text arrives as runes, not bytes.
	if got := decoder.feed([]byte("❯")); !reflect.DeepEqual(got, []keyEvent{{kind: keyRune, r: '❯'}}) {
		t.Fatalf("utf-8 = %+v", got)
	}
}

func TestComposerEditsADraft(t *testing.T) {
	var draft composer
	for _, event := range []keyEvent{
		{kind: keyRune, r: 'a'}, {kind: keyRune, r: 'b'}, {kind: keyBackspace}, {kind: keyRune, r: 'c'},
	} {
		draft.apply(event)
	}
	if draft.text() != "ac" {
		t.Fatalf("draft = %q", draft.text())
	}
	draft.apply(keyEvent{kind: keyStash})
	if draft.text() != "" || draft.stashed != "ac" {
		t.Fatalf("stash left draft=%q stashed=%q", draft.text(), draft.stashed)
	}
	if line := draft.take(); line != "" {
		t.Fatalf("take on an empty draft = %q", line)
	}
}
