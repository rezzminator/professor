package compose

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestGoldenRowScenarios(t *testing.T) {
	var rendered bytes.Buffer
	scenarios := []struct {
		name  string
		input Input
	}{
		{name: "default", input: fixtureInput(DefaultView)},
		{name: "all", input: fixtureInput(AllView)},
		{name: "killed", input: fixtureInput(KilledView)},
		{name: "split-and-multiserver", input: splitFixtureInput()},
		{name: "caps-overflow", input: capsFixtureInput()},
	}
	for _, scenario := range scenarios {
		renderScenario(&rendered, scenario.name, Compose(scenario.input))
	}
	rendered.Truncate(rendered.Len() - 1)

	goldenPath := filepath.Join("..", "..", "testdata", "golden", "compose.tsv")
	if os.Getenv("PFM_UPDATE_GOLDENS") == "1" {
		if err := os.WriteFile(goldenPath, rendered.Bytes(), 0o644); err != nil {
			t.Fatalf("regenerate compose golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read compose golden: %v\nactual:\n%s", err, rendered.String())
	}
	if !bytes.Equal(rendered.Bytes(), want) {
		t.Fatalf(
			"compose golden mismatch (%s)\nwant:\n%s\nactual:\n%s",
			firstDifference(want, rendered.Bytes()),
			want,
			rendered.String(),
		)
	}
}

func firstDifference(want, got []byte) string {
	limit := len(want)
	if len(got) < limit {
		limit = len(got)
	}
	for index := 0; index < limit; index++ {
		if want[index] != got[index] {
			return fmt.Sprintf(
				"byte %d: want %#x, got %#x; lengths %d/%d",
				index,
				want[index],
				got[index],
				len(want),
				len(got),
			)
		}
	}
	return fmt.Sprintf("lengths %d/%d", len(want), len(got))
}

func renderScenario(output *bytes.Buffer, name string, result Output) {
	fmt.Fprintf(output, "SCENARIO\t%s\n", name)
	fmt.Fprintf(
		output,
		"META\tkilled=%d\tempty=%d\tprojects=%s\n",
		result.KilledCount,
		result.SuppressedCount,
		strings.Join(result.ProjectOrder, ","),
	)
	projects := make([]string, 0, len(result.ProjectDirs))
	for project := range result.ProjectDirs {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	for _, project := range projects {
		fmt.Fprintf(output, "DIR\t%s\t%s\n", project, result.ProjectDirs[project])
	}
	for index := range result.Rows {
		row := result.Rows[index]
		fmt.Fprintf(
			output,
			"ROW\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\n",
			row.Kind,
			row.ID,
			row.Name,
			row.Project,
			row.CWD,
			row.Socket,
			row.PaneID,
			row.Size,
			row.PromptCount,
			row.ActivityNS,
			row.AgeNS,
			strconv.Itoa(row.Account),
			renderInts(row.Accounts),
			renderBool(row.Killed),
			renderBool(row.BG),
			renderBool(row.C1H),
			renderBool(row.Attached),
			boolInt(row.Here),
			row.ServerCount,
			row.SplitCount,
		)
	}
	output.WriteByte('\n')
}

func renderInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func renderBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
