package chat

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestStatusReportsADeadChatAsAStatusNotAnError pins the verb's contract: a
// resumable chat with no server behind it comes back dead, with its facts,
// and no error — the caller reads Alive(), never an error, for liveness.
func TestStatusReportsADeadChatAsAStatusNotAnError(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "c3333333-3333-4333-8333-333333333333"
	seedClaudeChat(t, root, id, assistantSaid("tests are green"))
	status, err := Status(context.Background(), nil, StatusRequest{Target: id}, io.Discard)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != headless.StateDead || status.Alive() || status.SessionID != id {
		t.Fatalf("Status() = %+v, want %s dead", status, id)
	}
	if !strings.Contains(status.Last, "tests are green") {
		t.Fatalf("Status().Last = %q, want the newest record", status.Last)
	}
	if status.Summary != "" || status.Ask != "" {
		t.Fatalf("Status() ran a runner nobody asked for: %+v", status)
	}
}

func TestInspectSeatReportsDurableLossUntilNextPrompt(t *testing.T) {
	testjail.Fleet(t)
	runtime, db, seat := nativeChatSeat(t, "/fixture/unreachable.sock", "lost")
	for _, note := range []string{"your last message was not answered — re-send it", ""} {
		before := seat
		seat.LostNote = note
		if err := db.UpdateSeat(context.Background(), before, seat); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSeat(
			context.Background(),
			&runtime,
			headless.Chat{
				ID:      seat.SessionID,
				SeatID:  seat.SeatID,
				Backend: "herdr",
				Engine:  pfmengine.Claude,
				Name:    seat.Name,
			},
			time.Now(),
			nil,
		)
		if err != nil || status.LostNote != note {
			t.Fatalf("status=%+v err=%v want lost_note=%q", status, err, note)
		}
	}
}
