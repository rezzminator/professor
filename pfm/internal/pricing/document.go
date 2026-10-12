package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// RefreshReport is what the refresh that served a table did, as the document
// states it: Changed and Persisted only for a refreshed table.
type RefreshReport struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	Error        string `json:"error,omitempty"`
	Changed      *bool  `json:"changed,omitempty"`
	Persisted    *bool  `json:"persisted,omitempty"`
	PersistError string `json:"persist_error,omitempty"`
	StampError   string `json:"stamp_error,omitempty"`
}

// Document is the `pfm model-cost --json` document: the served table, where
// it came from, the refresh that served it and the override merged into it.
// A saved one is a reproducible price table for `pfm tokens --prices`.
type Document struct {
	Version    int           `json:"version"`
	FetchedAt  string        `json:"fetched_at"`
	CheckedAt  string        `json:"checked_at,omitempty"`
	Sources    []Source      `json:"sources"`
	ServedFrom string        `json:"served_from"`
	File       string        `json:"file,omitempty"`
	FileError  string        `json:"file_error,omitempty"`
	Refresh    RefreshReport `json:"refresh"`
	Override   *Override     `json:"override"`
	Model      string        `json:"model,omitempty"`
	Rows       []Row         `json:"rows"`
}

// EncodeDocument writes doc as pfm model-cost --json prints it: two-space
// indented, newline-terminated, no HTML escaping.
func EncodeDocument(w io.Writer, doc Document) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("encode price document: %w", err)
	}
	return nil
}

// DecodeDocument reads and validates one saved document; name labels every
// error. Its rows are served rows: each carries the source pfm set.
func DecodeDocument(content []byte, name string) (Document, error) {
	var doc Document
	if err := strictUnmarshal(content, &doc); err != nil {
		return Document{}, fmt.Errorf("price document %s: %w", name, err)
	}
	version := doc.Version
	if err := checkVersion(&version); err != nil {
		return Document{}, fmt.Errorf("price document %s: %w", name, err)
	}
	rows := make([]Row, len(doc.Rows))
	for i, row := range doc.Rows {
		if row.Source != SourcePublished && row.Source != SourceOverride {
			return Document{}, fmt.Errorf("price document %s: %s: source %q is neither %s nor %s",
				name, row.Key, row.Source, SourcePublished, SourceOverride)
		}
		row.Source = ""
		rows[i] = row
	}
	if err := validate(
		Table{Version: doc.Version, FetchedAt: doc.FetchedAt, Sources: doc.Sources, Rows: rows},
	); err != nil {
		return Document{}, fmt.Errorf("price document %s: %w", name, err)
	}
	if doc.CheckedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, doc.CheckedAt); err != nil {
			return Document{}, fmt.Errorf(
				"price document %s: checked_at %q is not an RFC 3339 time",
				name,
				doc.CheckedAt,
			)
		}
	}
	if doc.Override != nil && (doc.Override.Path == "" || doc.Override.Rows < 0) {
		return Document{}, fmt.Errorf("price document %s: override needs a path and a row count", name)
	}
	return doc, nil
}

// Table is the document's rows as a price table, sources kept.
func (doc Document) Table() Table {
	return Table{Version: doc.Version, FetchedAt: doc.FetchedAt, Sources: doc.Sources, Rows: doc.Rows}
}
