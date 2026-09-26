package installer

import (
	"path/filepath"
	"strings"
	"testing"
)

// A standalone harvester entry in the Claude user registry (~/.claude.json)
// is the legacy state the cutover exists to find; reading only the Codex side
// or a project .mcp.json would report this machine migrated when it is not.
func TestInspectHarvesterClientCutoverFlagsAStandaloneEntryInTheClaudeUserRegistry(t *testing.T) {
	home := t.TempDir()
	writeFixture(
		t,
		filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"harvester":{"type":"stdio","command":"uv","args":["run","harvester"]}}}`,
	)
	registries := []string{filepath.Join(home, ".claude.json")}
	for _, report := range InspectHarvesterClientCutover(home, 8377, registries, nil) {
		if report.State == MCPClientLegacyStandalone {
			return
		}
	}
	t.Fatal("the Claude user registry's standalone harvester was reported migrated")
}

// TestInspectHarvesterClientCutoverRefusesANilRegistryList pins issue #24
// finding 5's part D: a nil registries argument is a programming error, not
// "use the historical $HOME/.claude.json default" — the caller must resolve
// the actual roster (installer.ClaudeUserRegistries). Silently falling back
// to one hardcoded path is exactly how doctor and the writer disagreed with
// no indication why; refusing loudly with one MCPClientUnreadable report
// naming the missing list is the fix.
func TestInspectHarvesterClientCutoverRefusesANilRegistryList(t *testing.T) {
	home := t.TempDir()
	reports := InspectHarvesterClientCutover(home, 8377, nil, nil)
	if len(reports) != 1 {
		t.Fatalf("reports=%#v, want exactly one unreadable report naming the missing list", reports)
	}
	if reports[0].State != MCPClientUnreadable {
		t.Fatalf("reports[0].State=%q, want %q", reports[0].State, MCPClientUnreadable)
	}
	if reports[0].Error == nil || reports[0].Error.Error() != "no Claude registries supplied" {
		t.Fatalf("reports[0].Error=%v, want an error naming the missing registry list", reports[0].Error)
	}
}

// TestOpenCodeUnownedEntriesNamesWhatInstallWillNotReplace pins the
// distinction doctor's remediation rests on: install records every OpenCode
// registration it writes in its ownership ledger and preserves any entry that
// ledger does not match (writeMCPOpenCodeJSON's "preserve conflicting manual
// OpenCode MCP client"), so a user-written `harvester` entry is named while a
// pfm-written one with a stale port is not — it is the one `pfm install --yes`
// still rewrites.
func TestOpenCodeUnownedEntriesNamesWhatInstallWillNotReplace(t *testing.T) {
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	writeFixture(
		t,
		path,
		`{"mcp":{"chat":{"type":"local","command":["`+filepath.Join(home, ".local", "bin", "pfm")+
			`","mcp","chat","serve"],"enabled":true},`+
			`"harvester":{"type":"local","command":["uv","--directory","/srv/harvester","run","harvester"]}}}`,
	)
	writeFixture(t, filepath.Join(managedRootForHome(home), mcpOwnershipName),
		`{"opencodeRegistrations":{"`+path+`":{"chat":{"type":"local","command":["`+
			filepath.Join(home, ".local", "bin", "pfm")+`","mcp","chat","serve"],"enabled":true}}}}`)

	unowned, err := OpenCodeUnownedEntries(home, path, chatName, mcpServerHarvester)
	if err != nil {
		t.Fatalf("OpenCodeUnownedEntries() error = %v", err)
	}
	if len(unowned) != 1 || unowned[0] != mcpServerHarvester {
		t.Fatalf("unowned=%q, want only the user-written harvester entry", unowned)
	}

	// No ledger at all: every present entry is one install did not write.
	bare := t.TempDir()
	barePath := OpenCodeConfigPath(bare)
	writeFixture(t, barePath, `{"mcp":{"harvester":{"type":"remote","url":"https://operator.invalid"}}}`)
	unowned, err = OpenCodeUnownedEntries(bare, barePath, chatName, mcpServerHarvester)
	if err != nil || len(unowned) != 1 || unowned[0] != mcpServerHarvester {
		t.Fatalf("unowned=%q, err=%v; want the harvester entry named with no ledger present", unowned, err)
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
// install writes, pfm's own legacy `chat` / `harvester` shapes are legacy-pfm,
// anything else is foreign, and a malformed config is unreadable, never absent.
func TestInspectOpenCodeServersClassifiesProfessorAndPFMLegacyEntries(t *testing.T) {
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	bin := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(
		t,
		path,
		`{"mcp":{"professor":{"type":"local","command":["`+bin+`","mcp","serve","--stdio"],"enabled":true},`+
			`"chat":{"type":"local","command":["`+bin+`","mcp","chat","serve"],"enabled":true},`+
			`"harvester":{"type":"remote","url":"http://127.0.0.1:8456/mcp/harvester","enabled":true}}}`,
	)
	reports := InspectOpenCodeServers(path, home, 8456, professorName, chatName, mcpServerHarvester)
	if len(reports) != 3 || reports[0].State != MCPClientPFM || reports[1].State != MCPClientLegacyPFM ||
		reports[2].State != MCPClientLegacyPFM {
		t.Fatalf("reports=%#v, want professor pfm, chat and harvester legacy-pfm", reports)
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

// TestInspectClaudeServersClassifiesProfessorAndPFMLegacyEntries pins the one
// Claude/Codex classifier: `professor` is pfm only in the exact stdio shape
// install writes (`{"type":"stdio","command":"<bin>","args":["mcp","serve",
// "--stdio"]}`, nothing more, nothing less), never over HTTP; pfm's legacy
// `chat` / `harvester` shapes (the loopback URL, the retired 64-hex bearer,
// the stdio chat) are legacy-pfm; a `uv`/`harvest…` harvester stays
// legacy-standalone.
func TestInspectClaudeServersClassifiesProfessorAndPFMLegacyEntries(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	bin := filepath.Join(home, ".local", "bin", "pfm")
	bearer := "Bearer " + strings.Repeat("ab", 32)
	for _, testCase := range []struct {
		name, key, registration, want string
	}{
		{"professor stdio", professorName, `{"type":"stdio","command":"` + bin + `","args":["mcp","serve","--stdio"]}`, MCPClientPFM},
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
		{"harvester http legacy", mcpServerHarvester, `{"type":"http","url":"http://127.0.0.1:8456/mcp/harvester"}`, MCPClientLegacyPFM},
		{"harvester bearer legacy", mcpServerHarvester, `{"type":"http","url":"http://127.0.0.1:8456/mcp/harvester","headers":{"Authorization":"` + bearer + `"}}`, MCPClientLegacyPFM},
		{"harvester foreign bearer", mcpServerHarvester, `{"type":"http","url":"http://127.0.0.1:8456/mcp/harvester","headers":{"Authorization":"Bearer retired"}}`, MCPClientForeignRegistration},
		{"harvester wrong port", mcpServerHarvester, `{"type":"http","url":"http://127.0.0.1:9999/mcp/harvester"}`, MCPClientForeignRegistration},
		{"harvester standalone", mcpServerHarvester, `{"type":"stdio","command":"uv","args":["run","harvester"]}`, MCPClientLegacyStandalone},
		{"harvester foreign", mcpServerHarvester, `{"type":"http","url":"https://foreign.invalid/mcp"}`, MCPClientForeignRegistration},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			writeFixture(t, path, `{"mcpServers":{"`+testCase.key+`":`+testCase.registration+`}}`)
			reports := InspectClaudeServers(path, home, 8456, testCase.key)
			if len(reports) != 1 || reports[0].State != testCase.want {
				t.Fatalf("reports=%#v, want state %q", reports, testCase.want)
			}
		})
	}
}

// TestInspectHarvesterClientCutoverTellsPFMLegacyFromForeignInCodex pins the
// Codex half: the single-line loopback url table is pfm's legacy entry (the
// one install removes), while the same url with a headers table is not a
// shape install removes and so stays foreign.
func TestInspectHarvesterClientCutoverTellsPFMLegacyFromForeignInCodex(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, ".codex")
	config := filepath.Join(codex, "config.toml")
	writeFixture(t, config, "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:8456/mcp/harvester\"\n")
	reports := InspectHarvesterClientCutover(home, 8456, []string{}, []string{codex})
	if len(reports) != 2 || reports[0].State != MCPClientLegacyPFM {
		t.Fatalf("reports=%#v, want the Codex url table legacy-pfm", reports)
	}
	writeFixture(t, config, "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:8456/mcp/harvester\"\n"+
		"[mcp_servers.harvester.headers]\nAuthorization = \"Bearer "+strings.Repeat("ab", 32)+"\"\n")
	reports = InspectHarvesterClientCutover(home, 8456, []string{}, []string{codex})
	if reports[0].State != MCPClientForeignRegistration {
		t.Fatalf("reports=%#v, want the headed Codex table foreign-registration", reports)
	}
}

// TestOpenCodeUnownedEntriesClaimsAnInterruptedInstallsPendingEntry pins the
// interrupted install: the ledger's opencodePending already holds pfm's
// professor for the path (writeMCPOpenCodeJSON records it before the write),
// so the entry is install's own — the one it adopts on the next run — and
// doctor must not call it user-owned.
func TestOpenCodeUnownedEntriesClaimsAnInterruptedInstallsPendingEntry(t *testing.T) {
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
