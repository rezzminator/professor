package mockengine

import (
	"context"
	"io"
	"reflect"
	"testing"
	"time"
)

// TestReadKeysStopsWhenTheContextEnds is L2-F33: readKeys' comment promised
// "until stdin closes or ctx ends", but the loop consulted ctx only while
// HANDING OVER an event — a read that decoded to no events at all (a CSI
// sequence, a partial rune) went straight back into stdin.Read, and the pump
// outlived its context until the writer happened to close.
func TestReadKeysStopsWhenTheContextEnds(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	keys := readKeys(ctx, reader)

	if _, err := writer.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-keys:
		if event.kind != keyRune || event.r != 'a' {
			t.Fatalf("first event = %+v, want rune 'a'", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readKeys never delivered the first key")
	}

	cancel()
	// A CSI sequence: it decodes to NO events, so nothing sends and nothing
	// would ever have checked ctx on the old loop.
	if _, err := writer.Write([]byte("\x1b[A")); err != nil {
		t.Fatal(err)
	}
	select {
	case event, open := <-keys:
		if open {
			t.Fatalf("readKeys kept pumping after its context ended: %+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readKeys did not stop after its context ended")
	}
}

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
