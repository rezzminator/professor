package obs

import (
	"log/slog"
	"testing"
)

func TestTestRecorderCapturesFieldsAndRestoresTheProcessLogger(t *testing.T) {
	before := processScope()
	ctx, recorder := Test(t)
	if processScope() == before {
		t.Fatal("obs.Test did not install its recorder as the process logger")
	}
	Logger(ctx).Warn("probe", FieldChat, "cc-1")
	records := recorder.Records()
	if len(records) != 1 || records[0].Message != "probe" || records[0].Level != slog.LevelWarn.String() {
		t.Fatalf("records = %+v, want one WARN probe", records)
	}
	if chat, found := records[0].Field(FieldChat); !found || chat != "cc-1" {
		t.Fatalf("chat field = %v (found %t)", chat, found)
	}
	if _, found := records[0].Field("nothing-logged-this"); found {
		t.Fatal("Field reported a key the record never carried")
	}
}

func TestRecorderReportsAnUndecodableLineAsEvidence(t *testing.T) {
	recorder := &Recorder{}
	if _, err := recorder.Write([]byte("{not json}\n")); err != nil {
		t.Fatal(err)
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Message == "" {
		t.Fatal("an undecodable line was dropped instead of reported")
	}
}
