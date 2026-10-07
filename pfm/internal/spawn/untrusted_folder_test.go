package spawn

import (
	"context"
	"strings"
	"testing"
)

// untrustedCodex is fakeCodex in a folder it does not trust, as a live 0.159.0
// pane played it: the folder-trust dialog (its option key "1" trusts the
// folder and starts a thread; Escape leads on to a Read Only composer with no
// thread), and a composer that answers /rename with codexNoThread and only
// queues a prompt — the composer empties, so the prompt looks submitted.
// holdsTrust is a dialog that ignores its option key.
type untrustedCodex struct {
	*fakeCodex

	trustAsked bool
	holdsTrust bool
	noThread   bool
	noticed    bool
}

const untrustedDialog = "  Folder access\n  /work/alpha\n" +
	"  Trust this folder? Codex can read, edit, and run files here, subject to your permission settings.\n" +
	"› 1. Trust and continue\n  2. Back to Agent Command Center\n  enter continue · esc back\n"

func newUntrustedCodex() *untrustedCodex {
	return &untrustedCodex{fakeCodex: newFakeCodex(), noThread: true}
}

func (fake *untrustedCodex) Capture(ctx context.Context, socket, target string) (string, error) {
	fake.mutex.Lock()
	trustAsked, noticed := fake.trustAsked, fake.noticed
	fake.mutex.Unlock()
	if trustAsked {
		return untrustedDialog, nil
	}
	capture, err := fake.fakeCodex.Capture(ctx, socket, target)
	if err != nil || !noticed {
		return capture, err
	}
	return "■ " + codexNoThread + "\n" + capture, nil
}

func (fake *untrustedCodex) SendKey(ctx context.Context, socket, target, key string) error {
	fake.mutex.Lock()
	switch {
	case fake.trustAsked:
		fake.keys = append(fake.keys, "key:"+key)
		switch {
		case key == "Escape":
			fake.trustAsked = false
		case key == "1" && !fake.holdsTrust:
			fake.trustAsked, fake.noThread = false, false
		}
		fake.mutex.Unlock()
		return nil
	case fake.noThread && key == "Enter" && (fake.stage == "offered" || fake.composer != ""):
		fake.keys = append(fake.keys, "key:"+key)
		fake.noticed = true
		fake.stage, fake.composer = "composer", ""
		fake.mutex.Unlock()
		return nil
	}
	fake.mutex.Unlock()
	return fake.fakeCodex.SendKey(ctx, socket, target, key)
}

func requireUntrustedRefusal(t *testing.T, result Result, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("Run() reported a thread-less Codex as started: %#v", result)
	}
	for _, want := range []string{"no active thread", "untrusted", "/work/alpha", codexNoThread} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestCodexWithoutAThreadFailsAtRename(t *testing.T) {
	fake := newUntrustedCodex()
	result, err := Run(context.Background(), fake, codexRequest())
	requireUntrustedRefusal(t, result, err)
	if countKey(fake.keys, "literal:/rename") != 1 {
		t.Fatalf("a thread-less Codex was asked to rename more than once: %v", fake.keys)
	}
	for _, key := range fake.keys {
		if strings.HasPrefix(key, "paste:") {
			t.Fatalf("the first prompt was pasted into a thread-less Codex: %v", fake.keys)
		}
	}
}

func TestCodexWithoutAThreadFailsAtThePrompt(t *testing.T) {
	fake := newUntrustedCodex()
	fake.offersRename = false
	result, err := Run(context.Background(), fake, codexRequest())
	requireUntrustedRefusal(t, result, err)
}

// TestCodexFolderTrustDialogIsNeverEscaped is 0.159's "Trust this folder?"
// dialog: pfm trusts the folder by the dialog's own option key, exactly as it
// accepts the older "1. Yes, continue", then renames and prompts. Escape there
// would leave a thread-less Read Only composer.
func TestCodexFolderTrustDialogIsNeverEscaped(t *testing.T) {
	fake := newUntrustedCodex()
	fake.trustAsked = true
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v, keys=%v", err, fake.keys)
	}
	if len(fake.keys) == 0 || fake.keys[0] != "key:1" {
		t.Fatalf("trust dialog keys=%v, want its option key 1 first", fake.keys)
	}
	if countKey(fake.keys, "key:Escape") != 0 {
		t.Fatalf("the folder-trust dialog was escaped: %v", fake.keys)
	}
	if !result.Named || !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v, keys=%v", result, fake.keys)
	}
}

// TestCodexFolderTrustDialogThatHoldsFailsLoudly is a dialog that ignores its
// option key: nothing is typed into it, nothing escapes it, and the spawn
// fails naming the untrusted folder instead of reporting a chat started.
func TestCodexFolderTrustDialogThatHoldsFailsLoudly(t *testing.T) {
	fake := newUntrustedCodex()
	fake.trustAsked, fake.holdsTrust = true, true
	result, err := Run(context.Background(), fake, codexRequest())
	requireUntrustedRefusal(t, result, err)
	for _, key := range fake.keys {
		if key != "key:1" {
			t.Fatalf("a held trust dialog got more than its option key: %v", fake.keys)
		}
	}
	if count := countKey(fake.keys, "key:1"); count == 0 || count > startupEscapes {
		t.Fatalf("option key presses = %d, want 1..%d: %v", count, startupEscapes, fake.keys)
	}
}

func TestCodexWithAThreadStillStarts(t *testing.T) {
	fake := newUntrustedCodex()
	fake.noThread = false
	result, err := Run(context.Background(), fake, codexRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Named || !result.Prompted || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestCodexFolderUntrustedReadsOnlyTheNotice(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		want    bool
	}{
		{"the notice", "codex\n■ " + codexNoThread + "\n› \n" + fakeStatusLine, true},
		{"the trust dialog", untrustedDialog, true},
		{"an idle composer", "codex\n› Ask Codex to do anything\n" + fakeStatusLine, false},
		{"a prompt quoting the notice", "codex\n› " + codexNoThread + "\n" + fakeStatusLine, false},
		{"a transcript naming the notice", "• Codex answers " + codexNoThread + " there\n› \n" + fakeStatusLine, false},
		{"the title without its choice", "  Trust this folder? is what it asked\n› \n" + fakeStatusLine, false},
	}
	for _, testCase := range cases {
		if got := codexFolderUntrusted(testCase.capture); got != testCase.want {
			t.Errorf("%s: codexFolderUntrusted = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestCodexFolderTrustKeyReadsTheDialogsOwnOption(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		want    string
	}{
		{"the dialog", untrustedDialog, "1"},
		{"the row renumbered", strings.Replace(untrustedDialog, "1. Trust", "2. Trust", 1), "2"},
		{"the row not selected", strings.Replace(untrustedDialog, "› 1. Trust", "  1. Trust", 1), "1"},
		{"the row without the title", "› 1. Trust and continue\n  2. Back to Agent Command Center\n", ""},
		{"the title without the row", "  Trust this folder? is what it asked\n› \n" + fakeStatusLine, ""},
		{"the older trust dialog", codexTrustQuestion + "\n› " + codexTrustYes + "\n", ""},
	}
	for _, testCase := range cases {
		if got := codexFolderTrustKey(testCase.capture); got != testCase.want {
			t.Errorf("%s: codexFolderTrustKey = %q, want %q", testCase.name, got, testCase.want)
		}
		if key := startupOverlayKey(testCase.capture); testCase.want != "" && key != testCase.want {
			t.Errorf("%s: startupOverlayKey = %q, want %q", testCase.name, key, testCase.want)
		}
	}
}
