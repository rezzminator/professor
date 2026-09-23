package backfill

import (
	"fmt"
	"strings"
)

// Summary is what one FromTranscripts did.
type Summary struct {
	ConfigDirs      []string  // config dirs whose projects/ was walked
	NoProjects      []string  // config dirs holding no projects/
	TranscriptsRead int       // transcripts parsed and written
	Unreadable      []Problem // transcripts that could not be read, each a backfill fault
	Malformed       []Problem // malformed lines of transcripts read, each a backfill fault
	EntriesSkipped  int       // tool entries older than since
	CallsInserted   int       // calls rows that did not exist
	CallsFilled     int       // existing calls rows that gained a NULL column
	Requests        int       // distinct requests rows upserted
	Agents          int       // distinct agents rows upserted
	Retired         int       // pending requests retired: unreachable internal sub-agents (retireOrphans)
}

// String is the summary the CLI prints: one count line, then one line per
// config dir without projects/, per unreadable transcript and per malformed line.
func (s Summary) String() string {
	var out strings.Builder
	fmt.Fprintf(
		&out, "callmeter backfill: %d transcripts read in %d config dirs, %d unreadable, %d malformed lines\n",
		s.TranscriptsRead, len(s.ConfigDirs), len(s.Unreadable), len(s.Malformed),
	)
	fmt.Fprintf(
		&out,
		"calls: %d inserted, %d filled; requests: %d; agents: %d; entries older than the window: %d; retired %d\n",
		s.CallsInserted,
		s.CallsFilled,
		s.Requests,
		s.Agents,
		s.EntriesSkipped,
		s.Retired,
	)
	for _, dir := range s.NoProjects {
		fmt.Fprintf(&out, "no projects/ in config dir %s\n", dir)
	}
	for _, p := range s.Unreadable {
		fmt.Fprintf(&out, "unreadable: %s: %s\n", p.Path, p.Err)
	}
	for _, p := range s.Malformed {
		fmt.Fprintf(&out, "malformed: %s line %d: %s\n", p.Path, p.Line, p.Err)
	}
	return out.String()
}
