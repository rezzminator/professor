package picker

import (
	"bytes"
	"context"
	"testing"
)

// A picker outside Herdr, or in Herdr but not opened in home, asks Herdr nothing:
// the socket named here does not exist, so any request would report an error.
func TestHerdrSpaceAsksOnlyForAPickerOpenedInHomeInsideHerdr(t *testing.T) {
	missing := t.TempDir() + "/no-herdr.sock"
	for _, test := range []struct{ socket, pane, home string }{
		{home: t.TempDir()},
		{socket: missing, pane: "w1:p1", home: t.TempDir()},
	} {
		t.Setenv("HERDR_SOCKET_PATH", test.socket)
		t.Setenv("HERDR_PANE_ID", test.pane)
		var stderr bytes.Buffer
		if label := herdrSpace(context.Background(), test.home, &stderr); label != "" || stderr.Len() != 0 {
			t.Fatalf("%+v: label %q stderr %q, want neither", test, label, stderr.String())
		}
	}
}
