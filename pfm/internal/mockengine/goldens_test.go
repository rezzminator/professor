package mockengine

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenProvenanceMarksEveryKey(t *testing.T) {
	for _, engine := range []string{engineClaude, engineCodex, engineOpenCode} {
		t.Run(engine, func(t *testing.T) {
			directory := filepath.Join("testdata", "hooks", engine)
			path := filepath.Join(directory, "SOURCES.tsv")
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}()
			reader := csv.NewReader(file)
			reader.Comma = '\t'
			reader.FieldsPerRecord = -1
			rows, err := reader.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			source := map[string]string{}
			for _, row := range rows[1:] {
				if len(row) < 4 || (row[2] != "observed" && row[2] != "unverified") {
					t.Fatalf("%s: invalid provenance row=%v", path, row)
				}
				if strings.Contains(row[3], "/home/") || strings.Contains(row[3], "/Users/") {
					t.Fatalf("%s: machine-absolute source %q", path, row[3])
				}
				source[row[0]+"/"+row[1]] = row[2]
			}
			goldens, err := filepath.Glob(filepath.Join(directory, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			if len(goldens) == 0 {
				t.Fatalf("%s: no hook goldens", directory)
			}
			for _, golden := range goldens {
				raw, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				var value map[string]any
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				for key := range value {
					if source[filepath.Base(golden)+"/"+key] == "" {
						t.Fatalf("%s: key %s has no observed/unverified provenance in %s", golden, key, path)
					}
				}
			}
		})
	}
}
