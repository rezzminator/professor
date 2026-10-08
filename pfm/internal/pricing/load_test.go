package pricing

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestEmbeddedTableDecodesWithBothEngines(t *testing.T) {
	table, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	engines := map[string]int{}
	for _, row := range table.Rows {
		engines[row.Engine]++
	}
	if table.Version != Version || engines[EngineClaude] == 0 || engines[EngineCodex] == 0 || len(table.Sources) != 2 {
		t.Fatalf(
			"embedded table: version %d, rows per engine %v, %d sources",
			table.Version,
			engines,
			len(table.Sources),
		)
	}
}

// tableAt is validTable fetched at fetchedAt.
func tableAt(fetchedAt string) []byte {
	return []byte(strings.Replace(validTable, "2026-10-07T23:02:05Z", fetchedAt, 1))
}

// withEmbedded swaps the compiled-in table for the test.
func withEmbedded(t *testing.T, content []byte) {
	t.Helper()
	saved := embeddedJSON
	embeddedJSON = content
	t.Cleanup(func() { embeddedJSON = saved })
}

// cloneHome is a home whose install record names a clone holding fileContent
// at FileRel (no file when fileContent is nil). It returns the home and the
// clone file path.
func cloneHome(t *testing.T, fileContent []byte) (string, string) {
	t.Helper()
	home, clone := t.TempDir(), t.TempDir()
	file := filepath.Join(clone, filepath.FromSlash(FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if fileContent != nil {
		if err := os.WriteFile(file, fileContent, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	resolved, err := CloneFile(home)
	if err != nil {
		t.Fatalf("CloneFile: %v", err)
	}
	return home, resolved
}

func TestLoadServesTheNewerOfTheCloneFileAndTheEmbeddedCopy(t *testing.T) {
	withEmbedded(t, tableAt("2026-10-07T00:00:00Z"))
	for _, tc := range []struct {
		name, fileAt, from string
	}{
		{"newer file", "2026-10-08T00:00:00Z", FromFile},
		{"older file", "2026-10-06T00:00:00Z", FromEmbedded},
		{"tie", "2026-10-07T00:00:00Z", FromFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, file := cloneHome(t, tableAt(tc.fileAt))
			prices, err := LoadPrices(home, "")
			if err != nil {
				t.Fatalf("LoadPrices: %v", err)
			}
			if prices.From != tc.from || prices.File != file || prices.FileError != nil || prices.Override != nil {
				t.Fatalf("LoadPrices = from %q file %q fileErr %v override %v; want from %q file %q",
					prices.From, prices.File, prices.FileError, prices.Override, tc.from, file)
			}
			published, err := LoadPublished(home)
			if err != nil || published.FileTable == nil || published.FileTable.FetchedAt != tc.fileAt ||
				!bytes.Equal(published.FileBytes, tableAt(tc.fileAt)) {
				t.Fatalf("LoadPublished clone file = %+v, err %v", published.FileTable, err)
			}
			for _, row := range prices.Rows {
				if row.Source != SourcePublished {
					t.Fatalf("row %s source %q, want published", row.Key, row.Source)
				}
			}
		})
	}
}

func TestLoadWithoutACloneRecordServesTheEmbeddedCopy(t *testing.T) {
	withEmbedded(t, tableAt("2026-10-07T00:00:00Z"))
	prices, err := LoadPrices(t.TempDir(), "")
	if err != nil {
		t.Fatalf("LoadPrices: %v", err)
	}
	if prices.From != FromEmbedded || prices.File != "" || prices.FileError != nil || len(prices.Rows) != 4 {
		t.Fatalf("LoadPrices = from %q file %q fileErr %v rows %d; want embedded, no file, no error, 4 rows",
			prices.From, prices.File, prices.FileError, len(prices.Rows))
	}
}

func TestLoadReportsAnUnusableCloneRecordOrFile(t *testing.T) {
	withEmbedded(t, tableAt("2026-10-07T00:00:00Z"))
	invalidHome, invalidFile := cloneHome(t, []byte(`{"version":1,"rows":[]}`))
	missingHome, missingFile := cloneHome(t, nil)
	staleHome := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(paths.SourceRepoPath(staleHome)), 0o700); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.WriteFile(paths.SourceRepoPath(staleHome), []byte(gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, home, want string }{
		{"invalid file", invalidHome, invalidFile},
		{"missing file", missingHome, missingFile},
		{"clone gone", staleHome, gone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prices, err := LoadPrices(tc.home, "")
			if err != nil {
				t.Fatalf("LoadPrices: %v", err)
			}
			if prices.From != FromEmbedded || prices.FileError == nil ||
				!strings.Contains(prices.FileError.Error(), tc.want) {
				t.Fatalf(
					"LoadPrices = from %q fileErr %v; want embedded and an error naming %s",
					prices.From,
					prices.FileError,
					tc.want,
				)
			}
		})
	}
}

// configWithOverride is a pfm.config.json path whose sibling pfm.prices.json
// holds body (none when body is empty).
func configWithOverride(t *testing.T, body string) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), config.FileName)
	if body != "" {
		if err := os.WriteFile(config.PricesPath(configPath), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return configPath
}

func TestServeMergesTheOverrideByKey(t *testing.T) {
	published, err := DecodeTable([]byte(validTable), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	configPath := configWithOverride(t, `{"version":2,"rows":[
		{"key":"gpt-5.5","engine":"codex","in":1,"out":2},
		{"key":"claude-negotiated-1","engine":"claude","in":3,"out":4}]}`)
	prices, err := ServeTable(published, Origin{From: FromEmbedded}, configPath)
	if err != nil {
		t.Fatalf("ServeTable: %v", err)
	}
	if prices.Override == nil || prices.Override.Path != config.PricesPath(configPath) || prices.Override.Rows != 2 {
		t.Fatalf("override = %+v, want %s with 2 rows", prices.Override, config.PricesPath(configPath))
	}
	got := map[string]Row{}
	order := []string{}
	for _, row := range prices.Rows {
		got[row.Key] = row
		order = append(order, row.Key)
	}
	if fmt.Sprint(order) != "[claude-haiku-5-5 claude-opus-5-5 gpt-5.5 gpt-5.5-pro claude-negotiated-1]" {
		t.Fatalf("row order = %v: an override replaces in place and appends a new key", order)
	}
	if row := got["gpt-5.5"]; row.Source != SourceOverride || *row.In != 1 || row.Long != nil {
		t.Fatalf("overridden row = %+v", row)
	}
	if got["claude-opus-5-5"].Source != SourcePublished || got["claude-negotiated-1"].Source != SourceOverride {
		t.Fatalf("sources = %q / %q", got["claude-opus-5-5"].Source, got["claude-negotiated-1"].Source)
	}
	if published.Rows[2].Source != "" || *published.Rows[2].In != 5 {
		t.Fatal("ServeTable must not mutate the published table it was given")
	}
	none, err := ServeTable(published, Origin{From: FromEmbedded}, configWithOverride(t, ""))
	if err != nil || none.Override != nil {
		t.Fatalf("no override file: override %+v, err %v; want none", none.Override, err)
	}
}

func TestServeRejectsAnInvalidOverrideNamingIt(t *testing.T) {
	published, err := DecodeTable([]byte(validTable), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"version 1":        `{"version":1,"rows":[]}`,
		"unknown field":    `{"version":2,"rows":[],"fetched_at":"x"}`,
		"bad row":          `{"version":2,"rows":[{"key":"gpt-x","engine":"codex","in":1}]}`,
		"unresolvable key": `{"version":2,"rows":[{"key":"claude-opus-4.1","engine":"claude","in":1,"out":2}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := configWithOverride(t, body)
			_, err := ServeTable(published, Origin{From: FromEmbedded}, configPath)
			if err == nil || !strings.Contains(err.Error(), config.PricesPath(configPath)) {
				t.Fatalf("ServeTable error = %v, want one naming %s", err, config.PricesPath(configPath))
			}
		})
	}
}

func TestWriteFileReplacesThePathWithCanonicalBytesMode0644(t *testing.T) {
	table, err := DecodeTable([]byte(validTable), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := WriteFile(path, table)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	want, err := Encode(table)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, want) || !bytes.Equal(onDisk, want) || info.Mode().Perm() != 0o644 {
		t.Fatalf("WriteFile: returned==canonical %v, disk==canonical %v, mode %v; want both and 0644",
			bytes.Equal(content, want), bytes.Equal(onDisk, want), info.Mode().Perm())
	}
}

func TestStampRecordsTheCheckAndTheContentItConfirmed(t *testing.T) {
	home := t.TempDir()
	if stamp, err := ReadStamp(home); err != nil || !stamp.At.IsZero() {
		t.Fatalf("missing stamp = %+v, %v; want zero, nil", stamp, err)
	}
	at := time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)
	if err := WriteStamp(home, at, []byte("content")); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}
	stamp, err := ReadStamp(home)
	if err != nil || !stamp.At.Equal(at) || stamp.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("content"))) {
		t.Fatalf("ReadStamp = %+v, %v", stamp, err)
	}
	if !stamp.Matches([]byte("content")) || stamp.Matches([]byte("changed")) || stamp.Matches(nil) {
		t.Fatal("Matches must hold only for the confirmed content")
	}
	if err := os.WriteFile(paths.PricesStampPath(home), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadStamp(home); err == nil || !strings.Contains(err.Error(), paths.PricesStampPath(home)) {
		t.Fatalf("malformed stamp error = %v, want one naming the stamp", err)
	}
}
