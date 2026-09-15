package artifact

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var nonnegativeIntegerPattern = regexp.MustCompile(`^(0|[1-9]\d*)$`)

var censusKeys = []string{
	"window-meta-count",
	"agent-meta-count",
	"paired-transcript-count",
	"selected-paired-transcript-count",
	"omitted-paired-transcript-count",
	"coverage-gap-count",
	"excluded-other-agent-or-invalid-count",
	"invalid-meta-count",
}

func ParseCensus(text string) (Census, error) {
	values := make(map[string]int, len(censusKeys))
	known := make(map[string]struct{}, len(censusKeys))
	for _, key := range censusKeys {
		known[key] = struct{}{}
	}
	var problems []string
	for offset, row := range linesOf(text) {
		fields := strings.Split(row, "\t")
		if len(fields) != 2 || !nonnegativeIntegerPattern.MatchString(fieldAt(fields, 1)) {
			problems = append(problems, fmt.Sprintf("line %d: invalid census row", offset+1))
			continue
		}
		value, err := strconv.Atoi(fields[1])
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: invalid census row", offset+1))
			continue
		}
		key := fields[0]
		if _, ok := known[key]; !ok {
			problems = append(problems, fmt.Sprintf("line %d: unknown census key %s", offset+1, key))
		} else if _, exists := values[key]; exists {
			problems = append(problems, fmt.Sprintf("line %d: duplicate census key %s", offset+1, key))
		} else {
			values[key] = value
		}
	}
	for _, key := range censusKeys {
		if _, ok := values[key]; !ok {
			problems = append(problems, "missing census key: "+key)
		}
	}
	if len(problems) != 0 {
		return Census{}, parseFailure(problems...)
	}
	return Census{
		WindowMetaCount:                  values["window-meta-count"],
		AgentMetaCount:                   values["agent-meta-count"],
		PairedTranscriptCount:            values["paired-transcript-count"],
		SelectedPairedTranscriptCount:    values["selected-paired-transcript-count"],
		OmittedPairedTranscriptCount:     values["omitted-paired-transcript-count"],
		CoverageGapCount:                 values["coverage-gap-count"],
		ExcludedOtherAgentOrInvalidCount: values["excluded-other-agent-or-invalid-count"],
		InvalidMetaCount:                 values["invalid-meta-count"],
	}, nil
}

func RenderCensus(census Census) string {
	values := []int{
		census.WindowMetaCount,
		census.AgentMetaCount,
		census.PairedTranscriptCount,
		census.SelectedPairedTranscriptCount,
		census.OmittedPairedTranscriptCount,
		census.CoverageGapCount,
		census.ExcludedOtherAgentOrInvalidCount,
		census.InvalidMetaCount,
	}
	var rendered strings.Builder
	for index, key := range censusKeys {
		fmt.Fprintf(&rendered, "%s\t%d\n", key, values[index])
	}
	return rendered.String()
}

func fieldAt(fields []string, index int) string {
	if index < 0 || index >= len(fields) {
		return ""
	}
	return fields[index]
}
