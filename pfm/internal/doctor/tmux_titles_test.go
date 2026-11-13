package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestTmuxTitlesDoctorReportsADisabledPolicy(t *testing.T) {
	machine := config.Config{
		Tmux:    config.Tmux{Titles: config.TmuxTitles{Enabled: false}},
		Sources: map[string]config.Source{"tmux.titles.enabled": config.SourceFile},
	}
	var stdout bytes.Buffer
	printTmuxTitlesDoctor(
		context.Background(), &stdout,
		paths.Values{TmuxDir: filepath.Join(t.TempDir(), "tmux-"+strconv.Itoa(os.Getuid()))}, machine,
	)
	want := "doctor: tmux titles policy=host-owned (config tmux.titles.enabled=false file)"
	if stdout.String() != `doctor: tmux titles policy=host-owned (config tmux.titles.enabled=false file)
doctor: tmux titles sockets=none live
` {
		t.Fatalf("report missing %q:\n%s", want, stdout.String())
	}
}
