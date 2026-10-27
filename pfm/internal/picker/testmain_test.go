package picker

import (
	"fmt"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 5 && os.Args[len(os.Args)-4] == "chat" && os.Args[len(os.Args)-3] == "end" &&
		os.Args[len(os.Args)-2] == "--native-end-worker" {
		marker := os.Getenv("PFM_PICKER_END_WORKER")
		if marker == "" {
			fmt.Fprintln(os.Stderr, "unowned picker end worker")
			os.Exit(2)
		}
		if err := os.WriteFile(
			marker+".tmp",
			[]byte(fmt.Sprintf("%d %s", os.Getpid(), os.Args[len(os.Args)-1])),
			0o600,
		); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.Rename(marker+".tmp", marker); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(testjail.Run(m))
}
