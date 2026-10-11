package transcript

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// readerRecords are the records a writer appends: every kind ReadMeta folds
// and Tail reads — prompts, answers with and without usage, a tool call, a
// compaction, a sidechain, a harness record, an API error, a blank line and
// one that is not JSON.
var readerRecords = map[pfmengine.ID][]string{
	pfmengine.Claude: {
		`{"type":"user","message":{"content":"run the suite %d"}}`,
		`{"type":"assistant","message":{"model":"claude-a","content":[{"type":"text","text":"done %d"}],` +
			`"usage":{"input_tokens":%d,"cache_read_input_tokens":10}}}`,
		`{"type":"assistant","message":{"model":"claude-b","content":[{"type":"tool_use","name":"Bash",` +
			`"input":{"command":"go test %d"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok %d"}]}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":%d}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"claude-side","content":"side %d"}}`,
		`{"type":"user","isMeta":true,"message":{"content":"<command-name>/clear</command-name> %d"}}`,
		`{"type":"assistant","isApiErrorMessage":true,"error":"server_error","message":{"model":"<synthetic>",` +
			`"content":[{"type":"text","text":"API Error %d"}]}}`,
		``,
		`not json %d`,
	},
	pfmengine.Codex: {
		`{"type":"turn_context","payload":{"model":"gpt-x-%d"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","n":%d}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"next %d"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text",` +
			`"text":"Codex %d"}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"n\":%d}"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d},` +
			`"model_context_window":1000}}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"answer %d"}}`,
		`{"type":"event_msg","payload":{"type":"turn_aborted","n":%d}}`,
	},
}

// readerWriter writes one transcript the way an engine and the world do:
// appends cut at any byte, a truncation, a rotation, a rewrite.
type readerWriter struct {
	t       *testing.T
	path    string
	engine  pfmengine.ID
	random  *rand.Rand
	pending []byte
	serial  int
}

func (writer *readerWriter) record() []byte {
	kinds := readerRecords[writer.engine]
	writer.serial++
	format := kinds[writer.random.Intn(len(kinds))]
	if !strings.Contains(format, "%d") {
		return []byte(format + "\n")
	}
	return []byte(fmt.Sprintf(format, writer.serial*7) + "\n")
}

// appendSome writes the next 1..3 records' worth of bytes, cut anywhere.
func (writer *readerWriter) appendSome() {
	for len(writer.pending) < 400 {
		writer.pending = append(writer.pending, writer.record()...)
	}
	cut := 1 + writer.random.Intn(len(writer.pending)/2)
	writer.appendBytes(writer.pending[:cut])
	writer.pending = writer.pending[cut:]
}

func (writer *readerWriter) appendBytes(data []byte) {
	writer.t.Helper()
	file, err := os.OpenFile(writer.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		writer.t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		writer.t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		writer.t.Fatal(err)
	}
}

func (writer *readerWriter) size() int64 {
	writer.t.Helper()
	info, err := os.Stat(writer.path)
	if err != nil {
		writer.t.Fatal(err)
	}
	return info.Size()
}

// truncate cuts the file anywhere, mid-record included, and the writer
// carries on from there.
func (writer *readerWriter) truncate() {
	writer.t.Helper()
	if err := os.Truncate(writer.path, writer.random.Int63n(writer.size()+1)); err != nil {
		writer.t.Fatal(err)
	}
	writer.pending = nil
}

// rotate puts a new file in place, its first records already written.
func (writer *readerWriter) rotate() {
	writer.t.Helper()
	next := writer.path + ".next"
	if err := os.WriteFile(next, append(writer.record(), writer.record()...), 0o600); err != nil {
		writer.t.Fatal(err)
	}
	if err := os.Rename(next, writer.path); err != nil {
		writer.t.Fatal(err)
	}
	writer.pending = nil
}

// rewrite replaces the file's bytes in place with other records, longer
// than it was: the same file, grown past the reader's fold.
func (writer *readerWriter) rewrite() {
	writer.t.Helper()
	size := writer.size()
	var content []byte
	for int64(len(content)) <= size {
		content = append(content, writer.record()...)
	}
	if err := os.WriteFile(writer.path, content, 0o600); err != nil {
		writer.t.Fatal(err)
	}
	writer.pending = nil
}

// TestReaderReadsWhatTheWholeReadsRead: through appends cut at any byte,
// truncations, rotations and in-place rewrites, every Read answers exactly
// what ReadMeta and Tail answer for the file as it stands.
func TestReaderReadsWhatTheWholeReadsRead(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		for seed := int64(1); seed <= 6; seed++ {
			t.Run(fmt.Sprintf("%s/seed %d", engine, seed), func(t *testing.T) {
				writer := &readerWriter{
					t: t, path: filepath.Join(t.TempDir(), "chat.jsonl"), engine: engine,
					random: rand.New(rand.NewSource(seed)),
				}
				writer.appendBytes(writer.record())
				reader := new(Reader)
				for step := 0; step < 300; step++ {
					change := "append"
					switch roll := writer.random.Intn(100); {
					case roll < 3:
						change = "truncate"
						writer.truncate()
					case roll < 5:
						change = "rotate"
						writer.rotate()
					case roll < 7:
						change = "rewrite"
						writer.rewrite()
					case roll < 15:
						change = "nothing"
					default:
						writer.appendSome()
					}
					assertReadsWhole(t, reader, writer.path, engine, fmt.Sprintf("step %d (%s)", step, change))
				}
			})
		}
	}
}

func assertReadsWhole(t *testing.T, reader *Reader, path string, engine pfmengine.ID, label string) {
	t.Helper()
	want, wantErr := ReadNewest(context.Background(), path, string(engine), TextCap)
	got, err := reader.Read(context.Background(), path, string(engine), TextCap)
	if (err != nil) != (wantErr != nil) {
		t.Fatalf("%s: Read error %v, whole read error %v", label, err, wantErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: Read\n  %+v\nwhole read\n  %+v", label, got, want)
	}
}

// TestReaderAnswersAsTheWholeReadsAtTheEdges: the cases a fold could get
// wrong — a missing file, an empty one, a partial record that completes, a
// file shrunk to a prefix of what was folded and grown back with other bytes
// within one clock tick — each read as the whole reads read it.
func TestReaderAnswersAsTheWholeReadsAtTheEdges(t *testing.T) {
	answer := `{"type":"assistant","message":{"model":"claude-a","content":[{"type":"text","text":"done"}],` +
		`"usage":{"input_tokens":5}}}` + "\n"
	prompt := `{"type":"user","message":{"content":"next"}}` + "\n"
	for _, test := range []struct {
		name  string
		steps []func(writer *readerWriter)
	}{
		{name: "a missing file, then one written", steps: []func(*readerWriter){
			func(*readerWriter) {},
			func(writer *readerWriter) { writer.appendBytes([]byte(prompt)) },
		}},
		{name: "an empty file", steps: []func(*readerWriter){
			func(writer *readerWriter) { writer.appendBytes(nil) },
		}},
		{name: "a partial record completed", steps: []func(*readerWriter){
			func(writer *readerWriter) { writer.appendBytes([]byte(prompt + answer[:30])) },
			func(writer *readerWriter) { writer.appendBytes([]byte(answer[30:])) },
		}},
		{name: "a final record without its newline", steps: []func(*readerWriter){
			func(writer *readerWriter) { writer.appendBytes([]byte(prompt + strings.TrimSuffix(answer, "\n"))) },
			func(writer *readerWriter) { writer.appendBytes([]byte("\n" + prompt)) },
		}},
		{name: "a CRLF transcript", steps: []func(*readerWriter){
			func(writer *readerWriter) {
				writer.appendBytes([]byte(strings.ReplaceAll(prompt+answer, "\n", "\r\n")))
			},
		}},
		{name: "shrunk to a prefix and grown back past the fold", steps: []func(*readerWriter){
			func(writer *readerWriter) { writer.appendBytes([]byte(prompt + answer)) },
			func(writer *readerWriter) {
				if err := os.Truncate(writer.path, int64(len(prompt))); err != nil {
					t.Fatal(err)
				}
				writer.appendBytes([]byte(prompt + prompt + prompt))
			},
		}},
		{name: "the same file deleted and written again", steps: []func(*readerWriter){
			func(writer *readerWriter) { writer.appendBytes([]byte(prompt + answer)) },
			func(writer *readerWriter) {
				if err := os.Remove(writer.path); err != nil {
					t.Fatal(err)
				}
			},
			func(writer *readerWriter) { writer.appendBytes([]byte(answer + prompt)) },
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &readerWriter{t: t, path: filepath.Join(t.TempDir(), "chat.jsonl"), engine: pfmengine.Claude}
			reader := new(Reader)
			for index, step := range test.steps {
				step(writer)
				assertReadsWhole(t, reader, writer.path, pfmengine.Claude, fmt.Sprintf("step %d", index))
				// Read twice: the unchanged file's answer is the stored one.
				assertReadsWhole(t, reader, writer.path, pfmengine.Claude, fmt.Sprintf("step %d again", index))
			}
		})
	}
}

// TestReaderNeverServesAFoldPastARewrite: a file rewritten in place with
// other records, longer than the fold and with a new modification time, is
// read from the top — the bytes before the fold's end no longer match.
func TestReaderNeverServesAFoldPastARewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	first := `{"type":"user","message":{"content":"first"}}` + "\n" +
		`{"type":"assistant","message":{"model":"claude-a","content":"one"}}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := new(Reader)
	assertReadsWhole(t, reader, path, pfmengine.Claude, "first")
	second := `{"type":"user","message":{"content":"other"}}` + "\n" +
		`{"type":"assistant","message":{"model":"claude-b","content":"two, longer than the first"}}` + "\n"
	if err := os.WriteFile(path, []byte(second), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Unix(0, 0).Add(48 * time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	assertReadsWhole(t, reader, path, pfmengine.Claude, "rewritten")
	got, err := reader.Read(context.Background(), path, string(pfmengine.Claude), TextCap)
	if err != nil || got.Meta.Model != "claude-b" || got.Meta.HumanPrompts != 1 {
		t.Fatalf("rewritten transcript read as %+v, %v; want claude-b with one prompt", got.Meta, err)
	}
}

// TestAReadingIsNeverDatedBeforeTheNewestRecordItRead (D7): an engine that
// ends its turn between a read's stat and its read of the bytes hands the read
// the turn's last record. The reading must date the transcript no earlier
// than that record: dated by the stat before it, a turn that ended this
// instant reads as idle since the tool call before it, and a watch announces
// IDLE before --idle-after has run.
func TestAReadingIsNeverDatedBeforeTheNewestRecordItRead(t *testing.T) {
	const working = `{"type":"user","message":{"content":"run the suite"}}` + "\n" +
		`{"type":"assistant","message":{"model":"claude-a","content":[{"type":"tool_use","name":"Bash",` +
		`"input":{"command":"go test"}}]}}` + "\n"
	const finished = `{"type":"assistant","message":{"model":"claude-a",` +
		`"content":[{"type":"text","text":"finished"}]}}` + "\n"
	for _, test := range []struct {
		name string
		read func(t *testing.T, path string, race func()) Reading
	}{
		{name: "the whole read", read: func(t *testing.T, path string, race func()) Reading {
			race()
			reading, err := ReadNewest(context.Background(), path, string(pfmengine.Claude), TextCap)
			if err != nil {
				t.Fatal(err)
			}
			return reading
		}},
		{name: "a first incremental read", read: func(t *testing.T, path string, race func()) Reading {
			race()
			reading, err := new(Reader).Read(context.Background(), path, string(pfmengine.Claude), TextCap)
			if err != nil {
				t.Fatal(err)
			}
			return reading
		}},
		{name: "a resumed incremental read", read: func(t *testing.T, path string, race func()) Reading {
			reader := new(Reader)
			if _, err := reader.Read(context.Background(), path, string(pfmengine.Claude), TextCap); err != nil {
				t.Fatal(err)
			}
			race()
			reading, err := reader.Read(context.Background(), path, string(pfmengine.Claude), TextCap)
			if err != nil {
				t.Fatal(err)
			}
			return reading
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "chat.jsonl")
			if err := os.WriteFile(path, []byte(working), 0o600); err != nil {
				t.Fatal(err)
			}
			writer := &readerWriter{t: t, path: path, engine: pfmengine.Claude}
			// The tool call has run for 150 seconds when the turn ends.
			race := func() {
				quiet := time.Now().Add(-150 * time.Second)
				if err := os.Chtimes(path, quiet, quiet); err != nil {
					t.Fatal(err)
				}
				appended := false
				betweenStatAndRead = func(string) {
					if !appended {
						appended = true
						writer.appendBytes([]byte(finished))
					}
				}
				t.Cleanup(func() { betweenStatAndRead = func(string) {} })
			}
			reading := test.read(t, path, race)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reading.HasLast || reading.Last.Text != "finished" {
				t.Fatalf("newest entry %+v (has %v); the read missed the record appended under it", reading.Last,
					reading.HasLast)
			}
			if ended := info.ModTime().UnixNano(); reading.Meta.ModifiedUnixNS < ended {
				t.Fatalf("reading dated %v, %v before the turn's last record it read",
					time.Unix(0, reading.Meta.ModifiedUnixNS), time.Duration(ended-reading.Meta.ModifiedUnixNS))
			}
		})
	}
}

// TestReaderNeverServesARecordItDidNotRead (D7): a record appended after the
// incremental read reached the end of the file, before its closing stat, is
// not in this reading — and the next poll must read it, not answer from a
// cursor that claims the file's new size.
func TestReaderNeverServesARecordItDidNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	writer := &readerWriter{t: t, path: path, engine: pfmengine.Claude}
	writer.appendBytes([]byte(`{"type":"user","message":{"content":"run the suite"}}` + "\n"))
	appended := false
	betweenReadAndRestat = func(string) {
		if !appended {
			appended = true
			writer.appendBytes([]byte(`{"type":"assistant","message":{"model":"claude-a",` +
				`"content":[{"type":"text","text":"finished"}]}}` + "\n"))
		}
	}
	t.Cleanup(func() { betweenReadAndRestat = func(string) {} })
	reader := new(Reader)
	if _, err := reader.Read(context.Background(), path, string(pfmengine.Claude), TextCap); err != nil {
		t.Fatal(err)
	}
	if !appended {
		t.Fatal("the read never reached its closing stat")
	}
	reading, err := reader.Read(context.Background(), path, string(pfmengine.Claude), TextCap)
	if err != nil {
		t.Fatal(err)
	}
	if !reading.HasLast || reading.Last.Text != "finished" {
		t.Fatalf("the next read answered %+v; the record appended under the last read is missing", reading.Last)
	}
	assertReadsWhole(t, reader, path, pfmengine.Claude, "after the race")
}

// TestReaderSweepForgetsTheSeatsNoLongerRead: a transcript not read between
// two sweeps is dropped; one read is kept.
func TestReaderSweepForgetsTheSeatsNoLongerRead(t *testing.T) {
	directory := t.TempDir()
	reader := new(Reader)
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(`{"type":"user","message":{"content":"x"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Read(context.Background(), path, string(pfmengine.Claude), TextCap); err != nil {
			t.Fatal(err)
		}
	}
	reader.Sweep()
	if _, err := reader.Read(context.Background(), filepath.Join(directory, "a.jsonl"), string(pfmengine.Claude),
		TextCap); err != nil {
		t.Fatal(err)
	}
	reader.Sweep()
	if len(reader.cursors) != 1 {
		t.Fatalf("after a sweep %d cursors are held, want the one read since the last", len(reader.cursors))
	}
}
