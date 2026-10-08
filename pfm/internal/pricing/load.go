package pricing

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

//go:embed prices.json
var embeddedJSON []byte

const (
	// FileRel is prices.json inside the Professor clone, slash-separated.
	FileRel = "pfm/internal/pricing/prices.json"

	// FromFile, FromEmbedded and FromFetch name where a served table came from.
	FromFile     = "file"
	FromEmbedded = "embedded"
	FromFetch    = "fetch"

	embeddedName = "embedded prices.json"
)

// Override reports the pfm.prices.json that was merged: its absolute path and
// how many rows the file holds.
type Override struct {
	Path string `json:"path"`
	Rows int    `json:"rows"`
}

// Origin is where a published table was read from. File is the clone's
// prices.json when the install record names a clone; FileError is a clone
// record or file that exists but cannot be used.
type Origin struct {
	From      string
	File      string
	FileError error
}

// Published is the newer of the clone file and the embedded copy, with the
// clone file's own table and bytes when it decoded.
type Published struct {
	Table
	Origin
	FileTable *Table
	FileBytes []byte
}

// Prices is the served table: a published table with the override merged into
// its rows, each row's Source set.
type Prices struct {
	Table
	Origin
	Override *Override
}

// Stamp is the last check that confirmed the clone file: when, and the
// sha256 of the bytes it confirmed.
type Stamp struct {
	At     time.Time
	SHA256 string
}

// Embedded is the table compiled into this binary.
func Embedded() (Table, error) {
	return DecodeTable(embeddedJSON, embeddedName)
}

// CloneFile is the clone's prices.json, from the install record.
func CloneFile(home string) (string, error) {
	repo, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(repo, filepath.FromSlash(FileRel)), nil
}

// LoadPublished serves the newer fetched_at of the clone file and the embedded
// copy, a tie to the file. It never fetches; its error is an invalid embedded
// table.
func LoadPublished(home string) (Published, error) {
	embedded, err := Embedded()
	if err != nil {
		return Published{}, err
	}
	published := Published{Table: embedded, Origin: Origin{From: FromEmbedded}}
	file, err := CloneFile(home)
	switch {
	case errors.Is(err, paths.ErrNoSourceRepoMarker):
		return published, nil
	case err != nil:
		published.FileError = fmt.Errorf("clone record: %w", err)
		return published, nil
	}
	published.File = file
	content, err := os.ReadFile(file)
	if err != nil {
		published.FileError = fmt.Errorf("read clone prices: %w", err)
		return published, nil
	}
	table, err := DecodeTable(content, file)
	if err != nil {
		published.FileError = err
		return published, nil
	}
	published.FileTable, published.FileBytes = &table, content
	if !fetchedBefore(table, embedded) {
		published.Table, published.From = table, FromFile
	}
	return published, nil
}

// fetchedBefore reports whether a was fetched strictly before b; both times
// passed validation.
func fetchedBefore(a, b Table) bool {
	at, aErr := time.Parse(time.RFC3339, a.FetchedAt)
	bt, bErr := time.Parse(time.RFC3339, b.FetchedAt)
	return aErr == nil && bErr == nil && at.Before(bt)
}

// ServeTable merges the override beside pfmConfigPath ("" for none) into a
// published table. A missing override file is none; an unreadable or invalid
// one is an error naming it.
func ServeTable(published Table, origin Origin, pfmConfigPath string) (Prices, error) {
	prices := Prices{Table: published, Origin: origin}
	prices.Rows = make([]Row, len(published.Rows))
	position := make(map[string]int, len(published.Rows))
	for i, row := range published.Rows {
		row.Source = SourcePublished
		prices.Rows[i] = row
		position[row.Key] = i
	}
	if pfmConfigPath == "" {
		return prices, nil
	}
	path := config.PricesPath(pfmConfigPath)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return prices, nil
	}
	if err != nil {
		return Prices{}, fmt.Errorf("prices override %s: %w", path, err)
	}
	overrides, err := decodeOverride(content, path)
	if err != nil {
		return Prices{}, err
	}
	for _, row := range overrides {
		row.Source = SourceOverride
		if at, ok := position[row.Key]; ok {
			prices.Rows[at] = row
			continue
		}
		position[row.Key] = len(prices.Rows)
		prices.Rows = append(prices.Rows, row)
	}
	prices.Override = &Override{Path: path, Rows: len(overrides)}
	return prices, nil
}

// LoadPrices is the machine's served table, never fetched: LoadPublished, then ServeTable.
func LoadPrices(home, pfmConfigPath string) (Prices, error) {
	published, err := LoadPublished(home)
	if err != nil {
		return Prices{}, err
	}
	return ServeTable(published.Table, published.Origin, pfmConfigPath)
}

// WriteFile replaces path with the table's canonical bytes (0644: a tracked,
// shared file) and returns them.
func WriteFile(path string, table Table) ([]byte, error) {
	content, err := Encode(table)
	if err != nil {
		return nil, err
	}
	if err := atomicfile.Write(path, content, 0o644); err != nil {
		return nil, fmt.Errorf("write prices %s: %w", path, err)
	}
	return content, nil
}

func pricesSHA256(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

// ReadStamp reads the check stamp; a missing stamp is the zero Stamp.
func ReadStamp(home string) (Stamp, error) {
	path := paths.PricesStampPath(home)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Stamp{}, nil
	}
	if err != nil {
		return Stamp{}, fmt.Errorf("read price check stamp %s: %w", path, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 || len(fields[1]) != sha256.Size*2 {
		return Stamp{}, fmt.Errorf("price check stamp %s is not \"{time} {sha256}\"", path)
	}
	at, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil {
		return Stamp{}, fmt.Errorf("price check stamp %s: %w", path, err)
	}
	return Stamp{At: at, SHA256: fields[1]}, nil
}

// WriteStamp records a check at at that confirmed the clone file content.
func WriteStamp(home string, at time.Time, content []byte) error {
	path := paths.PricesStampPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("write price check stamp %s: %w", path, err)
	}
	line := at.UTC().Format(time.RFC3339Nano) + " " + pricesSHA256(content) + "\n"
	if err := atomicfile.Write(path, []byte(line), 0o644); err != nil {
		return fmt.Errorf("write price check stamp %s: %w", path, err)
	}
	return nil
}

// Matches reports whether the stamp confirmed exactly this content.
func (s Stamp) Matches(content []byte) bool {
	return content != nil && s.SHA256 != "" && s.SHA256 == pricesSHA256(content)
}
