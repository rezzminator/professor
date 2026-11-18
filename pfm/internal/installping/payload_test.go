package installping

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const testID = "3f9c2a7e-1b4d-4e8a-9c2f-6d1e0b7a5c43"

func TestBuildJudgesEachFieldAsTheCounterDoes(t *testing.T) {
	many := make([]string, 0, 33)
	for _, name := range "abcdefghijklmnopqrstuvwxyz0123456" {
		many = append(many, string(name))
	}
	for _, test := range []struct {
		name, id, version, goos, goarch, event string
		plugins                                []string
		wantPlugins                            []string
		wantErr                                string
	}{
		{
			name: "the contract example", id: testID, version: "0.42.0-alpha", goos: "darwin", goarch: "arm64",
			plugins: []string{"harvester", "flights", "harvester"}, event: EventDaily, wantPlugins: []string{"flights", "harvester"},
		},
		{
			name: "no plugins", id: testID, version: "v1.2.3", goos: "linux", goarch: "amd64", event: EventInstall,
			wantPlugins: []string{},
		},
		{
			name: "a name outside the pattern is dropped", id: testID, version: "1.2.3", goos: "linux", goarch: "amd64",
			plugins: []string{"Flights", "-x", strings.Repeat("a", 41), strings.Repeat("a", 40)}, event: EventDaily,
			wantPlugins: []string{strings.Repeat("a", 40)},
		},
		{
			name: "33 names keep the first 32", id: testID, version: "1.2.3", goos: "linux", goarch: "amd64",
			plugins: many, event: EventDaily, wantPlugins: strings.Split("0 1 2 3 4 5 6 a b c d e f g h i j k l m n o p q r s t u v w x y", " "),
		},
		{
			name: "an uppercase id", id: strings.ToUpper(testID), version: "1.2.3", goos: "linux", goarch: "amd64",
			event: EventDaily, wantErr: `install id "3F9C2A7E-1B4D-4E8A-9C2F-6D1E0B7A5C43" is not a lowercase UUIDv4`,
		},
		{
			name: "a dev version", id: testID, version: "dev", goos: "linux", goarch: "amd64", event: EventDaily,
			wantErr: `version "dev" is not [v]MAJOR.MINOR.PATCH[-suffix]`,
		},
		{
			name: "another OS", id: testID, version: "1.2.3", goos: "plan9", goarch: "amd64", event: EventDaily,
			wantErr: `OS "plan9" is not one the counter takes`,
		},
		{
			name: "a 17-char arch", id: testID, version: "1.2.3", goos: "linux", goarch: strings.Repeat("a", 17),
			event: EventDaily, wantErr: `arch "aaaaaaaaaaaaaaaaa" is not one the counter takes`,
		},
		{
			name: "another event", id: testID, version: "1.2.3", goos: "linux", goarch: "amd64", event: "uninstall",
			wantErr: `event "uninstall" is neither install nor daily`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := BuildPing(test.id, test.version, test.goos, test.goarch, test.plugins, test.event)
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("Build error = %v, want %q", err, test.wantErr)
				}
				return
			}
			want := Ping{
				ID: test.id, Product: "pfm", Version: test.version, OS: test.goos, Arch: test.goarch,
				Plugins: test.wantPlugins, Event: test.event,
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("Build = %+v, %v; want %+v", got, err, want)
			}
		})
	}
	ping, _ := BuildPing(testID, "1.2.3", "linux", "amd64", nil, EventDaily)
	raw, _ := json.Marshal(ping)
	want := `{"id":"` + testID + `","product":"pfm","version":"1.2.3","os":"linux","arch":"amd64","plugins":[],"event":"daily"}`
	if string(raw) != want {
		t.Fatalf("no plugins encodes as %s, want %s", raw, want)
	}
}
