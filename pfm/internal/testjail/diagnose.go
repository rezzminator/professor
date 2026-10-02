package testjail

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

// goroutine is one block of a debug=2 goroutine dump.
type goroutine struct {
	id      string
	state   string
	frames  []frame // innermost first
	created frame
}

type frame struct {
	fn   string
	file string // path:line
}

// parseGoroutines splits a debug=2 dump into goroutines. Lines it cannot place
// are kept out of the result rather than guessed at.
func parseGoroutines(dump string) []goroutine {
	var out []goroutine
	for _, block := range strings.Split(strings.TrimSpace(dump), "\n\n") {
		lines := strings.Split(block, "\n")
		head := lines[0]
		if !strings.HasPrefix(head, "goroutine ") || !strings.HasSuffix(head, ":") {
			continue
		}
		g := goroutine{}
		open, end := strings.Index(head, "["), strings.LastIndex(head, "]")
		if open <= 0 || end <= open {
			continue
		}
		g.id = strings.TrimSpace(head[len("goroutine "):open])
		g.state = head[open+1 : end]
		for i := 1; i < len(lines); i++ {
			fn := strings.TrimSpace(lines[i])
			file := ""
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") {
				file = strings.TrimSpace(lines[i+1])
				if sp := strings.LastIndex(file, " +0x"); sp > 0 {
					file = file[:sp]
				}
				i++
			}
			if rest, ok := strings.CutPrefix(fn, "created by "); ok {
				g.created = frame{fn: rest, file: file}
				continue
			}
			p := strings.LastIndex(fn, "(")
			if p <= 0 || !strings.HasSuffix(fn, ")") {
				continue // not a call line: skipped, never guessed at
			}
			g.frames = append(g.frames, frame{fn: fn[:p], file: file})
		}
		out = append(out, g)
	}
	return out
}

// modulePrefix is this module's import path plus "/", read off testjail's own
// symbol, so user frames are told from the standard library without GOROOT.
func modulePrefix() string {
	name := runtime.FuncForPC(reflect.ValueOf(parseGoroutines).Pointer()).Name()
	if i := strings.Index(name, "/internal/testjail"); i > 0 {
		return name[:i+1]
	}
	return ""
}

// userFrame is the innermost frame in this module, outside the profiler.
func (g goroutine) userFrame(prefix string) (frame, bool) {
	for _, f := range g.frames {
		if prefix != "" && strings.HasPrefix(f.fn, prefix) && !strings.Contains(f.fn, "/internal/testjail.") {
			return f, true
		}
	}
	return frame{}, false
}

// testName is the Test function this goroutine runs, if it is a test runner:
// its name (a closure or subtest body still names the Test function it sits
// in) and the innermost frame inside that function.
func (g goroutine) testName() (string, frame, bool) {
	runner := false
	for _, f := range g.frames {
		if f.fn == "testing.tRunner" {
			runner = true
		}
	}
	if !runner {
		return "", frame{}, false
	}
	for _, f := range g.frames {
		pkg, rest, _ := strings.Cut(shortFn(f.fn), ".")
		segs := strings.Split(rest, ".")
		for i, seg := range segs {
			if strings.HasPrefix(seg, "Test") && seg != "TestMain" {
				return pkg + "." + strings.Join(segs[:i+1], "."), f, true
			}
		}
	}
	return "", frame{}, false
}

// diagnose renders the bundle's one-screen reading: what was running, where
// each goroutine of this module sits, and what it waits on.
func diagnose(root, reason string, summary map[string]any, dump string) string {
	var b strings.Builder
	prefix := modulePrefix()
	gs := parseGoroutines(dump)
	fmt.Fprintf(&b, "DIAGNOSIS %s — package %v, pid %v\n", reason, summary["package"], summary["pid"])
	fmt.Fprintf(&b, "wall %vs of timeout %vs · cpu user %s sys %s · run delay %s · max rss %s\n",
		summary["wall_s"], summary["timeout_s"],
		measured(summary, "user_s", "s", "rusage_error"), measured(summary, "sys_s", "s", "rusage_error"),
		measured(summary, "run_delay_s", "s", "run_delay_error"), measured(summary, "maxrss_kb", " KB", "rusage_error"))
	fmt.Fprintf(&b, "VM pressure while this process ran: %s\n", psiLine(summary))
	fmt.Fprintln(&b, "\nTests running at capture:")
	tests := 0
	for _, g := range gs {
		if name, t, ok := g.testName(); ok {
			at := t
			if u, ok := g.userFrame(prefix); ok {
				at = u
			}
			fmt.Fprintf(&b, "  %s [%s] at %s (%s)\n", name, g.state, shortFile(at.file, root), shortFn(at.fn))
			tests++
		}
	}
	if tests == 0 {
		fmt.Fprintln(&b, "  none (every test had returned)")
	}
	groups := map[string][]string{}
	for _, g := range gs {
		if _, _, isTest := g.testName(); isTest {
			continue
		}
		u, ok := g.userFrame(prefix)
		if !ok {
			continue
		}
		key := fmt.Sprintf("[%s] at %s (%s)", g.state, shortFile(u.file, root), shortFn(u.fn))
		if g.created.fn != "" {
			key += fmt.Sprintf(", created by %s at %s", shortFn(g.created.fn), shortFile(g.created.file, root))
		}
		groups[key] = append(groups[key], g.id)
	}
	fmt.Fprintln(&b, "\nOther goroutines of this module:")
	if len(groups) == 0 {
		fmt.Fprintln(&b, "  none")
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := len(groups[keys[i]]), len(groups[keys[j]]); a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "  %d× %s\n", len(groups[k]), k)
	}
	fmt.Fprintf(
		&b,
		"\n%d goroutines in all: goroutines.txt · trace window: go tool trace trace.out · heap: go tool pprof heap.pprof\n",
		len(gs),
	)
	return b.String()
}

// shortFn keeps the last import-path element: fleet.Scan.
func shortFn(fn string) string {
	return fn[strings.LastIndex(fn, "/")+1:]
}

// measured renders summary[key] with its unit, or why the counter is missing:
// an unreadable counter is never shown as zero.
func measured(summary map[string]any, key, unit, errKey string) string {
	if v := summary[key]; v != nil {
		return fmt.Sprintf("%v%s", v, unit)
	}
	return fmt.Sprintf("not measured (%v)", summary[errKey])
}

// psiLine renders the stall-second deltas, or why they are missing.
func psiLine(summary map[string]any) string {
	psi, ok := summary["psi"].(map[string]float64)
	if !ok {
		return fmt.Sprintf("not measured (%v)", summary["psi_error"])
	}
	return fmt.Sprintf("stalled s — cpu some %.3f · io some %.3f full %.3f · memory some %.3f full %.3f",
		psi["cpu_some"], psi["io_some"], psi["io_full"], psi["memory_some"], psi["memory_full"])
}

// shortFile keeps the module-relative path: internal/fleet/scan.go:42. It
// strips the module root directory (an ordinary build records absolute paths)
// or else this module's import path (a -trimpath build records those); a path
// under neither is returned unchanged.
func shortFile(file, root string) string {
	if root != "" {
		if rest, ok := strings.CutPrefix(
			file,
			strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator),
		); ok {
			return rest
		}
	}
	if prefix := modulePrefix(); prefix != "" {
		if rest, ok := strings.CutPrefix(file, prefix); ok {
			return rest
		}
	}
	return file
}
