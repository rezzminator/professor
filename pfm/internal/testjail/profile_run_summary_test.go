package testjail

import "testing"

func TestRenderUnitRun(t *testing.T) {
	const header = "package\tpid\tevent\texit_code\twall_s\tcpu_s\tmaxrss_kb\ttests\tdiagnosis\n"
	cases := []struct{ name, tsv, want string }{
		{
			"every result, the last row of a process standing for it",
			header +
				"internal_store\t11\tstarted-no-exit-recorded\t-1\t0\t0.01\t9000\t40\t-\n" +
				"cmd_pfm\t12\tstarted-no-exit-recorded\t-1\t0\t0.01\t9000\t300\t-\n" +
				"internal_ui\t13\tstarted-no-exit-recorded\t-1\t0\t0.01\t9000\t20\t-\n" +
				"internal_store\t11\texit\t0\t3.5\t5.25\t20000\t40\t-\n" +
				"internal_ui\t13\texit\t1\t1.25\t2\t15000\t20\tinternal_ui.13/exit/DIAGNOSIS.txt\n" +
				"internal_chat\t14\ttimeout-imminent\t-1\t590\t12\t30000\t80\tinternal_chat.14/timeout/DIAGNOSIS.txt\n" +
				"half a row\n",
			"# Unit run profile\n\n" +
				"- run: 20261113T170236Z-4242 · package processes: 4 · passed 1 · failed 1 · near timeout 1 · without an exit 1\n" +
				"- CPU: 19.3 s over every process · slowest: 590 s internal_chat\n" +
				"- per process: {package}.{pid}/summary.json · every event: packages.tsv\n" +
				"- unreadable rows in packages.tsv: 1\n\n" +
				"## Red\n\n| result | package | exit | diagnosis |\n|---|---|---|---|\n" +
				"| NEAR-TIMEOUT | internal_chat | -1 | internal_chat.14/timeout/DIAGNOSIS.txt |\n" +
				"| FAIL | internal_ui | 1 | internal_ui.13/exit/DIAGNOSIS.txt |\n" +
				"| NO-EXIT | cmd_pfm | -1 | - |\n\n" +
				"## Packages by wall\n\n| wall s | cpu s | tests | result | package |\n|---|---|---|---|---|\n" +
				"| 590 | 12 | 80 | NEAR-TIMEOUT | internal_chat |\n" +
				"| 3.5 | 5.25 | 40 | PASS | internal_store |\n" +
				"| 1.25 | 2 | 20 | FAIL | internal_ui |\n" +
				"| 0 | 0.01 | 300 | NO-EXIT | cmd_pfm |\n",
		},
		{
			"a header alone",
			header,
			"# Unit run profile\n\n" +
				"- run: 20261113T170236Z-4242 · package processes: 0 · passed 0 · failed 0 · near timeout 0 · without an exit 0\n" +
				"- CPU: 0.0 s over every process · slowest: none\n" +
				"- per process: {package}.{pid}/summary.json · every event: packages.tsv\n\n" +
				"## Red\n\n| result | package | exit | diagnosis |\n|---|---|---|---|\n| none | | | |\n\n" +
				"## Packages by wall\n\n| wall s | cpu s | tests | result | package |\n|---|---|---|---|---|\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderUnitRun("20261113T170236Z-4242", tc.tsv); got != tc.want {
				t.Fatalf("renderUnitRun =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
