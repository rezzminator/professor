package harness

import (
	"reflect"
	"testing"
)

func TestParsePackagesOrdersHighestCoverageFirst(t *testing.T) {
	out := "\tex.com/m/b\t\tcoverage: 12.5% of statements\n" +
		"\tex.com/m/a\t\tcoverage: 12.5% of statements\n" +
		"\tex.com/m/c\t\tcoverage: 100.0% of statements\n" +
		"\tex.com/m/d\t\tcoverage: 0.0% of statements\n"
	want := []PackageCover{
		{"ex.com/m/c", "100.0%"}, {"ex.com/m/a", "12.5%"}, {"ex.com/m/b", "12.5%"}, {"ex.com/m/d", "0.0%"},
	}
	if got := parsePackages(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePackages = %q, want %q", got, want)
	}
}

func TestParseFuncsLeadsWithTheUnreached(t *testing.T) {
	for _, row := range []struct {
		name, out, sorted, total, err string
	}{
		{
			"reached and unreached",
			"ex.com/m/a.go:3:\tf\t\t100.0%\nex.com/m/a.go:9:\tg\t\t0.0%\n" +
				"ex.com/m/b.go:4:\th\t\t50.0%\nex.com/m/b.go:8:\ti\t\t0.0%\ntotal:\t\t\t(statements)\t\t41.2%\n",
			"ex.com/m/a.go:9:\tg\t\t0.0%\nex.com/m/b.go:8:\ti\t\t0.0%\nex.com/m/b.go:4:\th\t\t50.0%\n" +
				"ex.com/m/a.go:3:\tf\t\t100.0%\ntotal:\t(statements)\t41.2%\n",
			"41.2%", "",
		},
		{
			"no total line", "ex.com/m/a.go:3:\tf\t\t100.0%\n", "", "",
			"cover: go tool cover -func printed no total line:\nex.com/m/a.go:3:\tf\t\t100.0%\n",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			sorted, total, err := parseFuncs(row.out)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if sorted != row.sorted || total != row.total || got != row.err {
				t.Fatalf("parseFuncs = %q, %q, %q; want %q, %q, %q", sorted, total, got, row.sorted, row.total, row.err)
			}
		})
	}
}

func TestParseWritersCountsByFirstTwoArgvWords(t *testing.T) {
	out := "Cover mode: atomic\n" +
		"data file c/covcounters.a.1.1 GOOS=linux GOARCH=amd64  program args: [/usr/local/bin/pfm statusline]\n" +
		"data file c/covcounters.a.2.2 GOOS=linux GOARCH=amd64  program args: [pfm internal herdr-state --engine x]\n" +
		"data file c/covcounters.a.3.3 GOOS=linux GOARCH=amd64  program args: [pfm statusline]\n" +
		"data file c/covcounters.a.4.4 GOOS=linux GOARCH=amd64  program args: [pfm]\n" +
		"data file c/covcounters.a.5.5 GOOS=linux GOARCH=amd64  program args: []\n" +
		"totalStmts: 0 coveredStmts: 0\n"
	want := []Writer{{"pfm statusline", 2}, {"pfm", 1}, {"pfm internal", 1}}
	if got := parseWriters(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWriters = %v, want %v", got, want)
	}
}
