package installer

import (
	"path/filepath"
	"testing"
)

func TestOpenCodeUnownedEntriesNamesWhatInstallWillNotReplace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	writeFixture(
		t,
		path,
		`{"mcp":{"chat":{"type":"local","command":["`+filepath.Join(home, ".local", "bin", "pfm")+
			`","mcp","chat","serve"],"enabled":true},`+
			`"external":{"type":"local","command":["uv","--directory","/srv/external","run","external"]}}}`,
	)
	writeFixture(t, filepath.Join(managedRootForHome(home), mcpOwnershipName),
		`{"opencodeRegistrations":{"`+path+`":{"chat":{"type":"local","command":["`+
			filepath.Join(home, ".local", "bin", "pfm")+`","mcp","chat","serve"],"enabled":true}}}}`)

	unowned, err := OpenCodeUnownedEntries(home, path, chatName, "external")
	if err != nil {
		t.Fatalf("OpenCodeUnownedEntries() error = %v", err)
	}
	if len(unowned) != 1 || unowned[0] != "external" {
		t.Fatalf("unowned=%q, want only the user-written external entry", unowned)
	}

	// No ledger at all: every present entry is one install did not write.
	bare := t.TempDir()
	barePath := OpenCodeConfigPath(bare)
	writeFixture(t, barePath, `{"mcp":{"external":{"type":"remote","url":"https://operator.invalid"}}}`)
	unowned, err = OpenCodeUnownedEntries(bare, barePath, chatName, "external")
	if err != nil || len(unowned) != 1 || unowned[0] != "external" {
		t.Fatalf("unowned=%q, err=%v; want the external entry named with no ledger present", unowned, err)
	}

	// An absent config names nothing and is not an error.
	if unowned, err := OpenCodeUnownedEntries(bare, filepath.Join(bare, "gone.jsonc"), chatName); err != nil ||
		len(unowned) != 0 {
		t.Fatalf("absent config = %q, %v; want no entries and no error", unowned, err)
	}

	// A malformed ledger is an error, never a silent "everything is pfm's".
	writeFixture(t, filepath.Join(managedRootForHome(bare), mcpOwnershipName), `{`)
	if _, err := OpenCodeUnownedEntries(bare, barePath, chatName); err == nil {
		t.Fatal("OpenCodeUnownedEntries() over a malformed ownership ledger = nil error, want the decode failure")
	}
}

// TestInspectOpenCodeServersClassifiesFamilyEntries pins the OpenCode
// classifier: a family entry is pfm only in the exact local stdio shape install
// writes, anything else is foreign, and a malformed config is unreadable, never
// absent.
func TestInspectOpenCodeServersClassifiesFamilyEntries(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	bin := filepath.Join(home, ".local", "bin", "pfm")
	args := `"mcp","serve","--stdio","--family","chat"`
	for _, testCase := range []struct{ name, config, want string }{
		{"install shape", `{"mcp":{"chat-professor":{"type":"local","command":["` + bin + `",` + args + `],"enabled":true}}}`, MCPClientPFM},
		{"absent", `{"mcp":{"other":{"type":"remote","url":"https://operator.invalid"}}}`, MCPClientAbsent},
		{"remote", `{"mcp":{"chat-professor":{"type":"remote","url":"http://127.0.0.1:8456/mcp/chat","enabled":true}}}`, MCPClientForeignRegistration},
		{"not enabled", `{"mcp":{"chat-professor":{"type":"local","command":["` + bin + `",` + args + `]}}}`, MCPClientForeignRegistration},
		{"bare pfm", `{"mcp":{"chat-professor":{"type":"local","command":["pfm",` + args + `],"enabled":true}}}`, MCPClientForeignRegistration},
		{"no family", `{"mcp":{"chat-professor":{"type":"local","command":["` + bin + `","mcp","serve","--stdio"],"enabled":true}}}`, MCPClientForeignRegistration},
		{"extra key", `{"mcp":{"chat-professor":{"type":"local","command":["` + bin + `",` + args + `],"enabled":true,"env":{}}}}`, MCPClientForeignRegistration},
		{"malformed", `{`, MCPClientUnreadable},
	} {
		writeFixture(t, path, testCase.config)
		report := InspectOpenCodeServers(path, home, "chat-professor")[0]
		if report.State != testCase.want || (report.Error != nil) != (testCase.want == MCPClientUnreadable) {
			t.Fatalf(
				"%s: report=%#v, want state %q (error only when unreadable); config %s",
				testCase.name,
				report,
				testCase.want,
				path,
			)
		}
	}
}

func TestOpenCodeUnownedEntriesClaimsAnInterruptedInstallsPendingEntry(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	chat := `{"type":"local","command":["` + filepath.Join(home, ".local", "bin", "pfm") +
		`","mcp","serve","--stdio","--family","chat"],"enabled":true}`
	writeFixture(t, path, `{"mcp":{"chat-professor":`+chat+`}}`)
	writeFixture(t, filepath.Join(managedRootForHome(home), mcpOwnershipName),
		`{"opencodePending":{"`+physicalSettingsPath(path)+`":{"chat-professor":`+chat+`}}}`)
	unowned, err := OpenCodeUnownedEntries(home, path, "chat-professor")
	if err != nil || len(unowned) != 0 {
		t.Fatalf("unowned=%q, err=%v; want the pending chat-professor claimed as pfm's", unowned, err)
	}

	// A pending record the entry no longer equals is not pfm's to claim.
	writeFixture(t, path, `{"mcp":{"chat-professor":{"type":"local","command":["hand-written"]}}}`)
	unowned, err = OpenCodeUnownedEntries(home, path, "chat-professor")
	if err != nil || len(unowned) != 1 || unowned[0] != "chat-professor" {
		t.Fatalf("unowned=%q, err=%v; want the hand-edited chat-professor named", unowned, err)
	}
}
