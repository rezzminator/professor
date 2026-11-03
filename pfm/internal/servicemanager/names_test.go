package servicemanager

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestUnitNamesFrom(t *testing.T) {
	for _, row := range []struct {
		name, prefix, unit, want string
		present                  bool
	}{
		{name: "unset", unit: "pfm-mcp.service", want: "pfm-mcp.service"},
		{name: "default", present: true, prefix: "pfm", unit: "pfm-schedule.timer", want: "pfm-schedule.timer"},
		{name: "systemd", present: true, prefix: "journey-atlas", unit: "pfm-mcp.service", want: "journey-atlas-mcp.service"},
		{name: "launchd", present: true, prefix: "journey-atlas", unit: "com.professor.pfm.mcp", want: "com.professor.journey-atlas.mcp"},
		{name: "filename", present: true, prefix: "pfm-atlas", unit: "/fixture/systemd/pfm-name-sync.path", want: "/fixture/systemd/pfm-atlas-name-sync.path"},
		{name: "target", present: true, prefix: "pfm-atlas", unit: "gui/1000/com.professor.pfm.schedule", want: "gui/1000/com.professor.pfm-atlas.schedule"},
		{name: "qualified", present: true, prefix: "pfm-atlas", unit: "pfm-atlas-mcp.service", want: "pfm-atlas-mcp.service"},
		{name: "overlapping prefix", present: true, prefix: "pfm-name", unit: "pfm-name-sync.service", want: "pfm-name-name-sync.service"},
		{name: "foreign", present: true, prefix: "journey-atlas", unit: "other.service", want: "other.service"},
	} {
		t.Run(row.name, func(t *testing.T) {
			env := &paths.MapEnv{}
			if row.present {
				env.Values = map[string]string{paths.EnvUnitPrefix: row.prefix}
			}
			names, err := UnitNamesFrom(env)
			if err != nil {
				t.Fatal(err)
			}
			if got := names.Name(row.unit); got != row.want {
				t.Fatalf("Name(%q) = %q, want %q", row.unit, got, row.want)
			}
			if got := names.Name(names.Name(row.unit)); got != row.want {
				t.Fatalf("Name(Name(%q)) = %q, want %q", row.unit, got, row.want)
			}
		})
	}
	for _, prefix := range []string{"a", "ab", strings.Repeat("a", 62), strings.Repeat("a", 63)} {
		if _, err := UnitNamesFrom(&paths.MapEnv{Values: map[string]string{paths.EnvUnitPrefix: prefix}}); err != nil {
			t.Fatalf("valid prefix %q: %v", prefix, err)
		}
	}
	for _, prefix := range []string{"", "-a", "1a", "A", "a/b", "a b", "a\n", strings.Repeat("a", 64)} {
		_, err := UnitNamesFrom(&paths.MapEnv{Values: map[string]string{paths.EnvUnitPrefix: prefix}})
		want := "PFM_UNIT_PREFIX must be 1..63 lowercase letters, digits or hyphens, starting with a letter (got " + "\"" + strings.ReplaceAll(
			prefix,
			"\n",
			"\\n",
		) + "\")"
		if err == nil || err.Error() != want {
			t.Fatalf("invalid prefix %q error = %v, want %q", prefix, err, want)
		}
	}
}

func TestUnitNamesRenderAndMatch(t *testing.T) {
	names, err := UnitNamesFrom(&paths.MapEnv{Values: map[string]string{paths.EnvUnitPrefix: "pfm-atlas"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names.Render(
		"Unit=pfm-name-sync.service\n<string>com.professor.pfm.theme</string>\nEnvironment=\"XPC_SERVICE_NAME=com.professor.pfm.theme\"\nExecStart=/fixture/pfm-mcp.service/pfm theme sync\n",
	),
		"Unit=pfm-atlas-name-sync.service\n<string>com.professor.pfm-atlas.theme</string>\nEnvironment=\"XPC_SERVICE_NAME=com.professor.pfm-atlas.theme\"\nExecStart=/fixture/pfm-mcp.service/pfm theme sync\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	for _, row := range []struct {
		name string
		want bool
	}{{"pfm-atlas-mcp.service", true}, {"com.professor.pfm-atlas.mcp.plist", true}, {"pfm-mcp.service", false}, {"pfm-beacon-mcp.service", false}} {
		if got := names.Matches(row.name); got != row.want {
			t.Fatalf("Matches(%q) = %t, want %t", row.name, got, row.want)
		}
	}
}
