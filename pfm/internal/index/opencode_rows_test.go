package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rowIDs is each row's id, in order.
func rowIDs(rows []OpenCodeRow) string {
	ids := make([]string, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	return strings.Join(ids, ",")
}

func TestReadOpenCodeSessionRowsReadsOneSessionInStoreOrder(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	got, err := ReadOpenCodeSessionRows(context.Background(), root, "ses_root")
	if err != nil {
		t.Fatalf("ReadOpenCodeSessionRows: %v", err)
	}
	if got.ID != "ses_root" || got.Title != "Ramsey attack" || got.Directory != "/work/nuts/03-ramsey" ||
		got.ParentID != "" || got.TimeCreatedMS != 10 || got.TimeUpdatedMS != 20 {
		t.Errorf("session = %+v", got)
	}
	if ids := rowIDs(got.Messages); ids != "m1,a1,m2,a2" {
		t.Errorf("messages = %s, want m1,a1,m2,a2 (time_created, then id)", ids)
	}
	if ids := rowIDs(got.Parts); ids != "i1,i2" {
		t.Errorf("parts = %s, want i1,i2", ids)
	}
	if len(got.Parts) == 0 {
		t.Fatal("no part read")
	}
	if part := got.Parts[0]; part.MessageID != "m1" || part.TimeCreatedMS != 100 ||
		part.Data != `{"type":"text","text":"prove the bound"}` {
		t.Errorf("first part = %+v", part)
	}
	child, err := ReadOpenCodeSessionRows(context.Background(), root, "ses_child")
	if err != nil || child.ParentID != "ses_root" || rowIDs(child.Messages) != "m3" {
		t.Errorf("child = %+v, err %v", child, err)
	}
}

func TestReadOpenCodeSessionRowsNamesWhatIsMissing(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	_, err := ReadOpenCodeSessionRows(context.Background(), root, "ses_nowhere")
	if !errors.Is(err, ErrNoOpenCodeSession) || !strings.Contains(err.Error(), "ses_nowhere") {
		t.Errorf("unknown session: err %v, want ErrNoOpenCodeSession naming it", err)
	}
	empty := t.TempDir()
	_, err = ReadOpenCodeSessionRows(context.Background(), empty, "ses_root")
	if !errors.Is(err, ErrNoOpenCodeStore) || !strings.Contains(err.Error(), OpenCodeStorePath(empty)) {
		t.Errorf("missing store: err %v, want ErrNoOpenCodeStore naming %s", err, OpenCodeStorePath(empty))
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "opencode.db"), []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ReadOpenCodeSessionRows(context.Background(), broken, "ses_root")
	if err == nil || errors.Is(err, ErrNoOpenCodeStore) {
		t.Errorf("unreadable store: err %v, want a read error that is not ErrNoOpenCodeStore", err)
	}
}

func TestReadOpenCodeUsageListsEverySessionAndTheWindowsMessages(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	cases := []struct {
		since int64
		want  string
	}{
		{15, "ses_child[m3] ses_gone[] ses_root[m1,a1,m2,a2]"},
		// a message created before the window is not the window's spend
		{105, "ses_child[m3] ses_gone[] ses_root[a1,m2,a2]"},
		{250, "ses_child[] ses_gone[] ses_root[]"},
	}
	for _, c := range cases {
		sessions, err := ReadOpenCodeUsage(context.Background(), root, c.since)
		if err != nil {
			t.Fatalf("since %d: %v", c.since, err)
		}
		var got []string
		for _, session := range sessions {
			got = append(got, session.ID+"["+rowIDs(session.Messages)+"]")
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("since %d: %s, want %s", c.since, strings.Join(got, " "), c.want)
		}
	}
	if _, err := ReadOpenCodeUsage(context.Background(), t.TempDir(), 0); !errors.Is(err, ErrNoOpenCodeStore) {
		t.Errorf("missing store: err %v, want ErrNoOpenCodeStore", err)
	}
}

func TestDecodeOpenCodeMessageReadsBothRolesAndRefusesOtherForms(t *testing.T) {
	assistant, err := DecodeOpenCodeMessage(`{"role":"assistant","agent":"build","mode":"build",` +
		`"providerID":"openai","modelID":"gpt-x","variant":"high","finish":"stop","cost":0.5,` +
		`"tokens":{"total":20,"input":4,"output":3,"reasoning":2,"cache":{"read":10,"write":1}},` +
		`"error":{"name":"APIError","data":{"message":"boom"}}}`)
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	if assistant.Role != "assistant" || assistant.Agent != "build" || assistant.ProviderID != "openai" ||
		assistant.ModelID != "gpt-x" || assistant.Variant != "high" || assistant.Finish != "stop" ||
		assistant.Cost == nil || *assistant.Cost != 0.5 || assistant.Tokens == nil ||
		assistant.Tokens.Input != 4 || assistant.Tokens.Output != 3 || assistant.Tokens.Reasoning != 2 ||
		assistant.Tokens.Cache.Read != 10 || assistant.Tokens.Cache.Write != 1 ||
		assistant.Tokens.Total == nil || *assistant.Tokens.Total != 20 || len(assistant.Error) == 0 {
		t.Errorf("assistant = %+v", assistant)
	}
	user, err := DecodeOpenCodeMessage(`{"role":"user","agent":"plan",` +
		`"model":{"providerID":"anthropic","modelID":"claude-x","variant":"max"},"summary":{"diffs":[]}}`)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if user.Role != "user" || user.Model == nil || user.Model.ProviderID != "anthropic" ||
		user.Model.ModelID != "claude-x" || user.Model.Variant != "max" || user.Tokens != nil {
		t.Errorf("user = %+v", user)
	}
	for _, bad := range []string{
		`{"role":"assistant","tokens":{"input":"4"}}`,
		`{"role":3}`,
		`not json`,
		`[]`,
	} {
		if _, err := DecodeOpenCodeMessage(bad); err == nil {
			t.Errorf("DecodeOpenCodeMessage(%s) read a form the producer never writes", bad)
		}
	}
}
