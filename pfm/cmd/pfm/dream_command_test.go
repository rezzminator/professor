package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDreamCommandContextCancelsOnInterruptAndSIGTERM(t *testing.T) {
	for _, test := range []struct {
		name   string
		signal os.Signal
	}{
		{name: "interrupt", signal: os.Interrupt},
		{name: "sigterm", signal: syscall.SIGTERM},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, stop := dreamCommandContext()
			defer stop()

			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatalf("find current process: %v", err)
			}
			if err := process.Signal(test.signal); err != nil {
				t.Fatalf("send %s: %v", test.signal, err)
			}
			select {
			case <-ctx.Done():
				if !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("context error = %v, want canceled", ctx.Err())
				}
			case <-time.After(time.Second):
				t.Fatalf("context remained live after %s", test.signal)
			}
		})
	}
}

func TestDreamNightParsesDefaultsAndSelections(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want dreamNightOptions
	}{
		{
			name: "current repository defaults",
			args: []string{"night"},
			want: dreamNightOptions{
				RepoRoot:     "/work/current",
				AgentType:    defaultDreamAgent,
				RegistryBase: defaultDreamRegistry,
			},
		},
		{
			name: "bootstrap",
			args: []string{
				"night", "--repo", "/work/repository", "--resources", "/work/resources", "--agent", "QA-Agent",
				"--bootstrap-count", "17",
			},
			want: dreamNightOptions{
				RepoRoot:       "/work/repository",
				AgentType:      "QA-Agent",
				RegistryBase:   defaultDreamRegistry,
				ResourcesRoot:  "/work/resources",
				BootstrapCount: 17,
			},
		},
		{
			name: "corpus file",
			args: []string{"night", "--corpus-file", "/audit/paths.txt"},
			want: dreamNightOptions{
				RepoRoot:     "/work/current",
				AgentType:    defaultDreamAgent,
				RegistryBase: defaultDreamRegistry,
				CorpusFile:   "/audit/paths.txt",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := 0
			runtime := rejectingDreamRuntime(t)
			runtime.night = func(_ context.Context, got dreamNightOptions, progress, _ io.Writer) (string, error) {
				called++
				if got.StartedAt.IsZero() {
					t.Fatal("night StartedAt is zero")
				}
				got.StartedAt = time.Time{}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("night options = %+v, want %+v", got, test.want)
				}
				_, _ = io.WriteString(progress, "root-progress\n")
				return "night-result\n", nil
			}
			var stdout, stderr bytes.Buffer
			code := runDreamWith(
				context.Background(), test.args, strings.NewReader(""),
				&stdout, &stderr, runtime,
			)
			if code != 0 || called != 1 || stdout.String() != "root-progress\nnight-result\n" || stderr.Len() != 0 {
				t.Fatalf("code=%d called=%d stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDreamNightRejectsInvalidSurfaceBeforeCallingRoot(t *testing.T) {
	tests := [][]string{
		{"night", "extra"},
		{"night", "--bootstrap-count="},
		{"night", "--bootstrap-count", "0"},
		{"night", "--bootstrap-count", "-1"},
		{"night", "--bootstrap-count", "many"},
		{"night", "--bootstrap-count", "2", "--corpus-file", "/paths"},
		{"night", "--corpus-file="},
		{"night", "--corpus-file", "relative/paths"},
		{"night", "--unknown"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args[1:], "_"), func(t *testing.T) {
			called := false
			runtime := rejectingDreamRuntime(t)
			runtime.night = func(context.Context, dreamNightOptions, io.Writer, io.Writer) (string, error) {
				called = true
				return "", nil
			}
			var stdout, stderr bytes.Buffer
			code := runDreamWith(context.Background(), args, strings.NewReader(""), &stdout, &stderr, runtime)
			if code != 2 || called || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "usage: pfm dream night") {
				t.Fatalf(
					"args=%q code=%d called=%v stdout=%q stderr=%q",
					args,
					code,
					called,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}

func TestDreamDefaultRepositoryFailureNamesExplicitOverride(t *testing.T) {
	runtime := rejectingDreamRuntime(t)
	runtime.repositoryRoot = func() (string, error) {
		return "", errors.New("not inside a repository")
	}
	runtime.night = func(context.Context, dreamNightOptions, io.Writer, io.Writer) (string, error) {
		t.Fatal("night ran without a resolved repository")
		return "", nil
	}
	var stdout, stderr bytes.Buffer
	code := runDreamWith(
		context.Background(),
		[]string{"night"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		runtime,
	)
	if code != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "pass --repo ROOT") ||
		!strings.Contains(stderr.String(), "not inside a repository") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDreamRepositoriesFileUsesAbsoluteXDGOrHomeConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/config-root")
	if got := defaultDreamRepositoriesFile(); got != "/config-root/pfm/repos.list" {
		t.Fatalf("absolute XDG repos file = %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative-config")
	want := filepath.Join(defaultDreamHome(), ".config", "pfm", "repos.list")
	if got := defaultDreamRepositoriesFile(); got != want {
		t.Fatalf("relative XDG repos file = %q, want %q", got, want)
	}
}

func TestDreamSignedApplyCommandCarriesDevelopmentOverlay(t *testing.T) {
	options := dreamNightOptions{
		RepoRoot:      "/repo",
		AgentType:     "Explore",
		ResourcesRoot: "/resources",
	}
	want := "dreamer-night: signed apply command: pfm dream apply --repo /repo --agent Explore --resources /resources /stage\n"
	if got := renderDreamApplyCommand(options, "/stage"); got != want {
		t.Fatalf("signed apply command = %q, want %q", got, want)
	}
	wantConfigured := "dreamer-night: signed apply command: pfm --config '/machine config/pfm.json' dream apply --repo /repo --agent Explore --resources /resources /stage\n"
	if got := renderDreamApplyCommand(options, "/stage", "/machine config/pfm.json"); got != wantConfigured {
		t.Fatalf("configured signed apply command = %q, want %q", got, wantConfigured)
	}
	options.ResourcesRoot = ""
	if got := renderDreamApplyCommand(options, "/stage"); strings.Contains(got, "--resources") {
		t.Fatalf("embedded-only signed apply command carries overlay: %q", got)
	}
}

func TestDreamApplyInspectMorningAndMigrateDispatchExactOptions(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.apply = func(_ context.Context, got dreamApplyOptions) (string, error) {
			want := dreamApplyOptions{
				RepoRoot:      "/repo",
				AgentType:     "Explore",
				RegistryBase:  defaultDreamRegistry,
				ResourcesRoot: "/resources",
				Stage:         "/repo/.professor/stm/tmp/staging/explorer-one",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("apply options = %+v, want %+v", got, want)
			}
			return "applied\n", nil
		}
		assertDreamRun(t, []string{
			"apply", "--repo", "/repo", "--resources", "/resources", "--agent", "Explore",
			"/repo/.professor/stm/tmp/staging/explorer-one",
		}, runtime, "applied\n")
	})

	t.Run("inspect", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.inspect = func(got dreamInspectOptions) (string, error) {
			want := dreamInspectOptions{
				RepoRoot:      "/repo",
				AgentType:     "QA",
				RegistryBase:  defaultDreamRegistry,
				ResourcesRoot: "/resources",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("inspect options = %+v, want %+v", got, want)
			}
			return "REPO\t/repo\nORGAN\t/repo/.professor/stm\nREGISTRY\t/registry/-repo\n", nil
		}
		assertDreamRun(t, []string{"inspect", "--repo", "/repo", "--resources", "/resources", "--agent", "QA"}, runtime,
			"REPO\t/repo\nORGAN\t/repo/.professor/stm\nREGISTRY\t/registry/-repo\n")
	})

	t.Run("morning", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.morning = func(_ context.Context, got dreamMorningOptions) (dreamMorningOutput, error) {
			want := dreamMorningOptions{
				RegistryBase:     defaultDreamRegistry,
				ResourcesRoot:    "/resources",
				RepositoriesFile: "/config/repos.list",
			}
			if got != want {
				t.Fatalf("morning options = %+v, want %+v", got, want)
			}
			return dreamMorningOutput{Stdout: "morning-result\n"}, nil
		}
		assertDreamRun(t, []string{
			"morning", "--repos", "/config/repos.list", "--resources", "/resources",
		}, runtime, "morning-result\n")
	})

	t.Run("morning aggregate failure", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.morning = func(context.Context, dreamMorningOptions) (dreamMorningOutput, error) {
			return dreamMorningOutput{
				Stdout: "dreamer-morning: BEGIN repo=/repo agent=Explore\ndreamer-morning: END repo=/repo agent=Explore\n",
				Stderr: "dreamer-morning: FAIL repo=/repo agent=Explore: failed closed\n",
				Failed: true,
			}, nil
		}
		var stdout, stderr bytes.Buffer
		code := runDreamWith(
			context.Background(), []string{"morning"}, strings.NewReader(""),
			&stdout, &stderr, runtime,
		)
		if code != 1 || !strings.Contains(stdout.String(), "BEGIN repo=/repo") ||
			!strings.Contains(stderr.String(), "FAIL repo=/repo") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("morning organ unresolved exits nonzero", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.morning = func(context.Context, dreamMorningOptions) (dreamMorningOutput, error) {
			return dreamMorningOutput{
				Stdout: "dreamer-morning: BEGIN repo=/missing agent=Explore\ndreamer-morning: END repo=/missing agent=Explore\n",
				Stderr: "dreamer-night: FAIL: organ unresolved for listed repository /missing\ndreamer-morning: FAIL repo=/missing agent=Explore rc=1\n",
				Failed: true,
			}, nil
		}
		var stdout, stderr bytes.Buffer
		code := runDreamWith(
			context.Background(), []string{"morning"}, strings.NewReader(""),
			&stdout, &stderr, runtime,
		)
		if code != 1 || !strings.Contains(stderr.String(), "organ unresolved for listed repository /missing") ||
			!strings.Contains(stdout.String(), "END repo=/missing") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})

	t.Run("migrate anchors", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.migrate = func(got string) (string, error) {
			if got != "/repo/.professor/stm" {
				t.Fatalf("organ = %q", got)
			}
			return "MIGRATE PASS organ=/repo/.professor/stm maps=2 rewritten=1 rows=3\n", nil
		}
		assertDreamRun(t, []string{"migrate-anchors", "/repo/.professor/stm"}, runtime,
			"MIGRATE PASS organ=/repo/.professor/stm maps=2 rewritten=1 rows=3\n")
	})

	t.Run("migrate rejected file is still reported", func(t *testing.T) {
		runtime := rejectingDreamRuntime(t)
		runtime.migrate = func(string) (string, error) {
			return "MIGRATE FILE path=maps/bad.md outcome=REJECTED rows=0 reason=\"anchor row grammar mismatch\"\n",
				errors.New("migration outcome REJECTED for maps/bad.md")
		}
		var stdout, stderr bytes.Buffer
		code := runDreamWith(
			context.Background(),
			[]string{"migrate-anchors", "/repo/.professor/stm"},
			strings.NewReader(""),
			&stdout,
			&stderr,
			runtime,
		)
		if code != 1 || !strings.Contains(stdout.String(), "outcome=REJECTED") ||
			!strings.Contains(stderr.String(), "REJECTED for maps/bad.md") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

func TestDreamNonNightArityIsExact(t *testing.T) {
	for _, args := range [][]string{
		{"apply"},
		{"apply", "one", "two"},
		{"inspect", "extra"},
		{"morning", "extra"},
		{"migrate-anchors"},
		{"migrate-anchors", "one", "two"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runDreamWith(
				context.Background(),
				args,
				strings.NewReader(""),
				&stdout,
				&stderr,
				rejectingDreamRuntime(t),
			)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: pfm dream "+args[0]) {
				t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDreamHookReadsNativeContractAndWritesBytesUnchanged(t *testing.T) {
	now := time.Date(2026, 8, 13, 7, 0, 0, 123, time.FixedZone("test", 3600))
	for _, kind := range []dreamHookKind{
		dreamHookAgentInject,
		dreamHookCodexSubagentInject,
		dreamHookNudge,
	} {
		t.Run(string(kind), func(t *testing.T) {
			runtime := rejectingDreamRuntime(t)
			runtime.now = func() time.Time { return now }
			runtime.project = func() string { return "/repo" }
			runtime.hook = func(got dreamHookRequest) ([]byte, error) {
				// The nudge never consumes the payload, so it never reads the
				// stdin that carries it (RULING-13).
				var wantInput []byte
				if kind != dreamHookNudge {
					wantInput = []byte("{\"event\":true}\n")
				}
				want := dreamHookRequest{
					Kind:             kind,
					Input:            wantInput,
					ProjectDirectory: "/repo",
					Now:              now,
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("hook request = %+v, want %+v", got, want)
				}
				return []byte("\x00native\n"), nil
			}
			var stdout, stderr bytes.Buffer
			code := runDreamWith(
				context.Background(), []string{"hook", string(kind)},
				strings.NewReader("{\"event\":true}\n"), &stdout, &stderr, runtime,
			)
			if code != 0 || stdout.String() != "\x00native\n" || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestDreamHookRejectsUnknownKindAndReportsInputFailure(t *testing.T) {
	for _, args := range [][]string{{"hook"}, {"hook", "gate-pin"}, {"hook", "nudge", "extra"}} {
		var stdout, stderr bytes.Buffer
		code := runDreamWith(
			context.Background(),
			args,
			strings.NewReader(""),
			&stdout,
			&stderr,
			rejectingDreamRuntime(t),
		)
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: pfm dream hook") {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}

	runtime := rejectingDreamRuntime(t)
	runtime.hook = func(dreamHookRequest) ([]byte, error) {
		t.Fatal("hook called after stdin read failed")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	// agent-inject, not nudge: the nudge no longer reads stdin, so only a
	// payload-consuming kind still exercises the read-failure path.
	code := runDreamWith(
		context.Background(),
		[]string{"hook", "agent-inject"},
		failingReader{},
		&stdout,
		&stderr,
		runtime,
	)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "read stdin: injected read failure") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDreamHelpListsOnlySpecifiedSurfaceAndRootHelpListsDream(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDreamWith(
		context.Background(),
		[]string{"help"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		rejectingDreamRuntime(t),
	)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("dream help code=%d stderr=%q", code, stderr.String())
	}
	for _, command := range []string{"night", "apply", "inspect", "morning", "migrate-anchors", "hook"} {
		if !strings.Contains(stdout.String(), command) {
			t.Fatalf("dream help omitted %q: %q", command, stdout.String())
		}
	}
	for _, forbidden := range []string{"gate-pin", "gate-coverage", "gate-anchors", "gate-verdicts", "test-surfaces"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("dream help exposed forbidden command %q: %q", forbidden, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"help"},
		&stdout,
		&stderr,
	); code != 0 || stderr.Len() != 0 ||
		!strings.Contains(stdout.String(), "  dream ") {
		t.Fatalf("root help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDreamOperationErrorsNameExactCommand(t *testing.T) {
	runtime := rejectingDreamRuntime(t)
	runtime.night = func(context.Context, dreamNightOptions, io.Writer, io.Writer) (string, error) {
		return "should-not-print", errors.New("night failed closed")
	}
	var stdout, stderr bytes.Buffer
	code := runDreamWith(context.Background(), []string{"night"}, strings.NewReader(""), &stdout, &stderr, runtime)
	if code != 1 || stdout.Len() != 0 || stderr.String() != "pfm dream night: night failed closed\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func rejectingDreamRuntime(t *testing.T) dreamCommandRuntime {
	t.Helper()
	unexpected := func(name string) {
		t.Fatalf("unexpected dream runtime call: %s", name)
	}
	return dreamCommandRuntime{
		night: func(context.Context, dreamNightOptions, io.Writer, io.Writer) (string, error) {
			unexpected("night")
			return "", nil
		},
		apply: func(context.Context, dreamApplyOptions) (string, error) {
			unexpected("apply")
			return "", nil
		},
		inspect: func(dreamInspectOptions) (string, error) {
			unexpected("inspect")
			return "", nil
		},
		morning: func(context.Context, dreamMorningOptions) (dreamMorningOutput, error) {
			unexpected("morning")
			return dreamMorningOutput{}, nil
		},
		migrate: func(string) (string, error) {
			unexpected("migrate")
			return "", nil
		},
		hook: func(dreamHookRequest) ([]byte, error) {
			unexpected("hook")
			return nil, nil
		},
		now:            time.Now,
		project:        func() string { return "" },
		repositoryRoot: func() (string, error) { return "/work/current", nil },
	}
}

func assertDreamRun(t *testing.T, args []string, runtime dreamCommandRuntime, want string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runDreamWith(context.Background(), args, strings.NewReader(""), &stdout, &stderr, runtime)
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("injected read failure")
}

var _ io.Reader = failingReader{}

// blockingReader never returns from Read and never reaches EOF. It models the
// stdin a caller leaves open — an inherited pipe, a terminal, a wrapper that
// forgot the redirect.
type blockingReader struct{ released chan struct{} }

func (r blockingReader) Read([]byte) (int, error) {
	<-r.released
	return 0, io.EOF
}

var _ io.Reader = blockingReader{}

// The nudge must never read stdin. Before the fix, runDreamHook called
// io.ReadAll unconditionally, so a stdin that never EOFs hung the process
// forever — and SIGTERM could not reclaim it, which defeats every `timeout`
// wrapper including the one configured in ~/.codex/hooks.json.
func TestDreamHookNudgeDoesNotReadStdin(t *testing.T) {
	released := make(chan struct{})
	defer close(released)

	runtime := rejectingDreamRuntime(t)
	var seen []byte
	runtime.hook = func(request dreamHookRequest) ([]byte, error) {
		seen = request.Input
		return nil, nil
	}
	runtime.project = func() string { return "" }

	type outcome struct {
		code   int
		stderr string
	}
	done := make(chan outcome, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := runDreamHook(
			[]string{"nudge"},
			blockingReader{released: released},
			&stdout,
			&stderr,
			runtime,
		)
		done <- outcome{code: code, stderr: stderr.String()}
	}()

	select {
	case got := <-done:
		if got.code != 0 {
			t.Fatalf("nudge hook code=%d stderr=%q", got.code, got.stderr)
		}
		if len(seen) != 0 {
			t.Fatalf("nudge hook consumed stdin: %q", seen)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nudge hook blocked on stdin it never reads")
	}
}

// The two inject hooks genuinely consume the event JSON, so they still read.
func TestDreamHookInjectKindsStillReadStdin(t *testing.T) {
	for _, kind := range []string{"agent-inject", "codex-subagent-inject"} {
		t.Run(kind, func(t *testing.T) {
			runtime := rejectingDreamRuntime(t)
			var seen []byte
			runtime.hook = func(request dreamHookRequest) ([]byte, error) {
				seen = request.Input
				return nil, nil
			}
			runtime.project = func() string { return "" }

			var stdout, stderr bytes.Buffer
			code := runDreamHook(
				[]string{kind},
				strings.NewReader(`{"agent_type":"explorer"}`),
				&stdout,
				&stderr,
				runtime,
			)
			if code != 0 {
				t.Fatalf("%s code=%d stderr=%q", kind, code, stderr.String())
			}
			if string(seen) != `{"agent_type":"explorer"}` {
				t.Fatalf("%s did not receive the payload: %q", kind, seen)
			}
		})
	}
}
