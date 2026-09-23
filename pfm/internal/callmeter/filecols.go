package callmeter

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// toolRead is the Read tool's name: its calls carry a line range.
const toolRead = "Read"

// FileColumnsFromInput sets, from a file tool's input, file_path — made
// absolute against cwd, left relative when there is none — and, for a Read,
// the range it asked for: offset (1 when absent or below 1) and limit. The
// file tools are Read, Write, Edit, MultiEdit (file_path) and NotebookEdit
// (notebook_path); any other tool is left untouched. file_bytes is never set:
// the hook stats the file, a transcript holds no size. An input that cannot be
// decoded, or a file tool's input naming no path, is the error, and the
// columns stay NULL; the caller turns it into its own fault.
func FileColumnsFromInput(c *Call, tool string, input json.RawMessage, cwd string) error {
	pathField := "file_path"
	switch tool {
	case toolRead, "Write", "Edit", "MultiEdit":
	case "NotebookEdit":
		pathField = "notebook_path"
	default:
		return nil
	}
	var fields struct {
		FilePath     string   `json:"file_path"`
		NotebookPath string   `json:"notebook_path"`
		Offset       *float64 `json:"offset"`
		Limit        *float64 `json:"limit"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return fmt.Errorf("decode %s input (%d bytes): %w", tool, len(input), err)
	}
	path := fields.FilePath
	if tool == "NotebookEdit" {
		path = fields.NotebookPath
	}
	if path == "" {
		return fmt.Errorf("%s input has no %s", tool, pathField)
	}
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	c.FilePath = Ptr(path)
	if tool != toolRead {
		return nil
	}
	c.ReadStart = Ptr(int64(1))
	if fields.Offset != nil && *fields.Offset >= 1 {
		c.ReadStart = Ptr(int64(*fields.Offset))
	}
	c.ReadLines = wholeOr(fields.Limit, nil)
	return nil
}

// FileColumnsFromResult sets, from a file tool's result object, the range a
// Read actually read — each of startLine, numLines and totalLines present wins
// over the input's request — and a Write or Edit's prior size: originalFile's
// length, or 0 for a file the Write created (type "create", originalFile
// null). Any other tool is left untouched; a result that cannot be decoded is
// the error.
func FileColumnsFromResult(c *Call, tool string, result json.RawMessage) error {
	switch tool {
	case toolRead, "Write", "Edit":
	default:
		return nil
	}
	var fields struct {
		Type         string  `json:"type"`
		OriginalFile *string `json:"originalFile"`
		File         *struct {
			StartLine  *float64 `json:"startLine"`
			NumLines   *float64 `json:"numLines"`
			TotalLines *float64 `json:"totalLines"`
		} `json:"file"`
	}
	if err := json.Unmarshal(result, &fields); err != nil {
		return fmt.Errorf("decode %s result (%d bytes): %w", tool, len(result), err)
	}
	if tool == toolRead {
		if fields.File != nil {
			c.ReadStart = wholeOr(fields.File.StartLine, c.ReadStart)
			c.ReadLines = wholeOr(fields.File.NumLines, c.ReadLines)
			c.ReadTotalLines = wholeOr(fields.File.TotalLines, c.ReadTotalLines)
		}
		return nil
	}
	switch {
	case fields.OriginalFile != nil:
		c.FileBytesBefore = Ptr(int64(len(*fields.OriginalFile)))
	case fields.Type == "create":
		c.FileBytesBefore = Ptr(int64(0))
	}
	return nil
}

// wholeOr is value truncated to a whole number, or fallback when it is absent.
func wholeOr(value *float64, fallback *int64) *int64 {
	if value == nil {
		return fallback
	}
	return Ptr(int64(*value))
}
