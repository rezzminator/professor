package pricing

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/config"
)

//go:embed prices.json
var shippedJSON []byte

// shippedSource names the embedded table in an error.
const shippedSource = "embedded prices.json"

// Shipped is the embedded table. It decodes and validates on every call, so a
// caller owns the rows it gets.
func Shipped() (Table, error) {
	rows, err := decodeTable(shippedJSON, shippedSource, SourceShipped)
	if err != nil {
		return Table{}, err
	}
	if err := checkPatterns(rows, shippedSource); err != nil {
		return Table{}, err
	}
	return Table{Rows: rows}, nil
}

// WithOverride is the shipped table with the override file at overridePath merged in.
// A missing file is no override; any other read fault, and any invalid file, is
// an error naming the path.
func WithOverride(overridePath string) (Table, error) {
	content, err := os.ReadFile(overridePath)
	if errors.Is(err, fs.ErrNotExist) {
		return Shipped()
	}
	source := overridePath
	if abs, absErr := filepath.Abs(overridePath); absErr == nil {
		source = abs
	}
	if err != nil {
		return Table{}, fmt.Errorf("prices %s: %w", source, err)
	}
	overrides, err := decodeTable(content, source, SourceOverride)
	if err != nil {
		return Table{}, err
	}
	table, err := Shipped()
	if err != nil {
		return Table{}, err
	}
	table.Rows = merge(table.Rows, overrides)
	if err := checkPatterns(table.Rows, source); err != nil {
		return Table{}, err
	}
	table.Override = &Override{Path: source, Rows: len(overrides)}
	return table, nil
}

// merge replaces the row of a shipped key in place and appends a new key after
// the shipped rows, in override-file order.
func merge(shipped, overrides []Row) []Row {
	position := make(map[string]int, len(shipped))
	for i := range shipped {
		position[shipped[i].Key] = i
	}
	for i := range overrides {
		key := overrides[i].Key
		if at, ok := position[key]; ok {
			shipped[at] = overrides[i]
			continue
		}
		position[key] = len(shipped)
		shipped = append(shipped, overrides[i])
	}
	return shipped
}

// Effective is the table of the machine: the override beside the given
// pfm.config.json merged into the shipped one. With no config path it is the
// shipped table alone.
func Effective(pfmConfigPath string) (Table, error) {
	if pfmConfigPath == "" {
		return Shipped()
	}
	return WithOverride(config.PricesPath(pfmConfigPath))
}
