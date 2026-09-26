package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func examplePath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	for dir := filepath.Dir(source); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "example.pfm.config.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatal("example.pfm.config.json missing from repository")
	return ""
}

func flattenExample(prefix string, value any, leaves map[string]any) {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 {
		leaves[prefix] = value
		return
	}
	for key, child := range object {
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		flattenExample(name, child, leaves)
	}
}

func TestExampleConfigMatchesKeys(t *testing.T) {
	content, err := os.ReadFile(examplePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(content, &object); err != nil {
		t.Fatal(err)
	}
	actual := map[string]any{}
	flattenExample("", object, actual)
	want := map[string]any{}
	for _, entry := range Keys() {
		encoded, err := json.Marshal(entry.Default)
		if err != nil {
			t.Fatal(err)
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		want[entry.Key] = decoded
	}
	var missing, extra, wrong []string
	for key, expected := range want {
		got, found := actual[key]
		if !found {
			missing = append(missing, key)
		} else if !reflect.DeepEqual(got, expected) {
			wrong = append(wrong, key)
		}
	}
	for key := range actual {
		if _, found := want[key]; !found {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(wrong)
	if len(missing)+len(extra)+len(wrong) != 0 {
		t.Fatalf("example parity: missing=%v extra=%v wrong-default=%v", missing, extra, wrong)
	}
}

func TestExampleConfigHasOnlyHomeRelativePathsAndNoRoster(t *testing.T) {
	content, err := os.ReadFile(examplePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), `"accounts"`) || strings.Contains(string(content), `"homes"`) {
		t.Fatal("example seeds an account roster")
	}
	var object map[string]any
	if err := json.Unmarshal(content, &object); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	flattenExample("", object, values)
	for key, value := range values {
		if strings.Contains(strings.ToLower(key), "token") && value != nil && value != "" {
			t.Fatalf("example contains a credential at %s", key)
		}
		if path, ok := value.(string); ok && (strings.HasPrefix(path, "/") || strings.Contains(path, "/home/")) {
			t.Fatalf("example has an absolute path at %s", key)
		}
	}
}
