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

// TestInspectOpenCodeServersClassifiesProfessorAndPFMLegacyEntries pins the
// OpenCode classifier: `professor` is pfm only in the exact local stdio shape
// install writes, pfm's own legacy `chat` / `external` shapes are legacy-pfm,
// anything else is foreign, and a malformed config is unreadable, never absent.
func TestInspectOpenCodeServersClassifiesProfessorAndPFMLegacyEntries(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	bin := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(
		t,
		path,
		`{"mcp":{"professor":{"type":"local","command":["`+bin+`","mcp","serve","--stdio"],"enabled":true},`+
			`"chat":{"type":"local","command":["`+bin+`","mcp","chat","serve"],"enabled":true},`+
			`"external":{"type":"remote","url":"http://127.0.0.1:8456/mcp/external","enabled":true}}}`,
	)
	reports := InspectOpenCodeServers(path, home, 8456, professorName, chatName)
	if len(reports) != 2 || reports[0].State != MCPClientLegacyPFM || reports[1].State != MCPClientLegacyPFM {
		t.Fatalf("reports=%#v, want professor pfm, chat and external legacy-pfm", reports)
	}
	writeFixture(t, path, `{"mcp":{"chat":{"type":"remote","url":"http://127.0.0.1:8456/mcp/chat","enabled":true}}}`)
	reports = InspectOpenCodeServers(path, home, 8456, professorName, chatName)
	if reports[0].State != MCPClientAbsent || reports[1].State != MCPClientLegacyPFM {
		t.Fatalf("reports=%#v, want professor absent and the remote chat legacy-pfm", reports)
	}
	for _, foreign := range []string{
		`{"mcp":{"professor":{"type":"remote","url":"http://127.0.0.1:8456/mcp/professor","enabled":true}}}`,
		`{"mcp":{"professor":{"type":"local","command":["` + bin + `","mcp","serve","--stdio"]}}}`,
		`{"mcp":{"professor":{"type":"local","command":["pfm","mcp","serve","--stdio"],"enabled":true}}}`,
		`{"mcp":{"professor":{"type":"local","command":["` + bin + `","mcp","serve","--stdio"],"enabled":true,"env":{}}}}`,
	} {
		writeFixture(t, path, foreign)
		if reports := InspectOpenCodeServers(
			path,
			home,
			8456,
			professorName,
		); reports[0].State != MCPClientForeignRegistration {
			t.Fatalf("%s: report=%#v, want foreign-registration", foreign, reports[0])
		}
	}
	writeFixture(t, path, `{"mcp":{"chat":{"type":"remote","url":"https://operator.invalid","enabled":true}}}`)
	reports = InspectOpenCodeServers(path, home, 8456, chatName)
	if reports[0].State != MCPClientForeignRegistration {
		t.Fatalf("foreign report=%#v, want foreign-registration", reports[0])
	}
	writeFixture(t, path, `{`)
	reports = InspectOpenCodeServers(path, home, 8456, professorName)
	if reports[0].State != MCPClientUnreadable || reports[0].Error == nil {
		t.Fatalf("unreadable report=%#v, want error-bearing unreadable", reports[0])
	}
}

// TestInspectProjectServersClassifiesProfessorAndPFMLegacyEntries pins the one
// Claude/Codex classifier: `professor` is pfm only in the exact stdio shape
// install writes (`{"type":"stdio","command":"<bin>","args":["mcp","serve",
// "--stdio"]}`, nothing more, nothing less), never over HTTP; pfm's legacy
// Legacy chat shapes are legacy-pfm.
func TestInspectProjectServersClassifiesProfessorAndPFMLegacyEntries(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".mcp.json")
	bin := filepath.Join(home, ".local", "bin", "pfm")
	for _, testCase := range []struct {
		name, key, registration, want string
	}{
		{"professor stdio", professorName, `{"type":"stdio","command":"` + bin + `","args":["mcp","serve","--stdio"]}`, MCPClientLegacyPFM},
		{"professor type-less", professorName, `{"command":"` + bin + `","args":["mcp","serve","--stdio"]}`, MCPClientForeignRegistration},
		{"professor http", professorName, `{"type":"http","url":"http://127.0.0.1:8456/mcp/professor"}`, MCPClientForeignRegistration},
		{"professor other command", professorName, `{"type":"stdio","command":"manual","args":["mcp","serve","--stdio"]}`, MCPClientForeignRegistration},
		{"professor extra args", professorName, `{"type":"stdio","command":"` + bin + `","args":["mcp","serve","--stdio","--x"]}`, MCPClientForeignRegistration},
		{"professor bare pfm", professorName, `{"type":"stdio","command":"pfm","args":["mcp","serve","--stdio"]}`, MCPClientForeignRegistration},
		{"professor other binary path", professorName, `{"type":"stdio","command":"/opt/pfm/bin/pfm","args":["mcp","serve","--stdio"]}`, MCPClientForeignRegistration},
		{"professor extra key", professorName, `{"type":"stdio","command":"` + bin + `","args":["mcp","serve","--stdio"],"timeout":30}`, MCPClientForeignRegistration},
		{"professor upper-case type", professorName, `{"type":"STDIO","command":"` + bin + `","args":["mcp","serve","--stdio"]}`, MCPClientForeignRegistration},
		{"chat stdio legacy", chatName, `{"type":"stdio","command":"` + bin + `","args":["mcp","chat","serve"]}`, MCPClientLegacyPFM},
		{"chat http legacy", chatName, `{"type":"http","url":"http://127.0.0.1:8456/mcp/chat"}`, MCPClientLegacyPFM},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			writeFixture(t, path, `{"mcpServers":{"`+testCase.key+`":`+testCase.registration+`}}`)
			reports := inspectProjectMCPServers(path, home, 8456, testCase.key)
			if len(reports) != 1 || reports[0].State != testCase.want {
				t.Fatalf("reports=%#v, want state %q", reports, testCase.want)
			}
		})
	}
}

func TestOpenCodeUnownedEntriesClaimsAnInterruptedInstallsPendingEntry(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	professor := `{"type":"local","command":["` + filepath.Join(home, ".local", "bin", "pfm") +
		`","mcp","serve","--stdio"],"enabled":true}`
	writeFixture(t, path, `{"mcp":{"professor":`+professor+`}}`)
	writeFixture(t, filepath.Join(managedRootForHome(home), mcpOwnershipName),
		`{"opencodePending":{"`+physicalSettingsPath(path)+`":{"professor":`+professor+`}}}`)
	unowned, err := OpenCodeUnownedEntries(home, path, professorName)
	if err != nil || len(unowned) != 0 {
		t.Fatalf("unowned=%q, err=%v; want the pending professor claimed as pfm's", unowned, err)
	}

	// A pending record the entry no longer equals is not pfm's to claim.
	writeFixture(t, path, `{"mcp":{"professor":{"type":"local","command":["hand-written"]}}}`)
	unowned, err = OpenCodeUnownedEntries(home, path, professorName)
	if err != nil || len(unowned) != 1 || unowned[0] != professorName {
		t.Fatalf("unowned=%q, err=%v; want the hand-edited professor named", unowned, err)
	}
}
