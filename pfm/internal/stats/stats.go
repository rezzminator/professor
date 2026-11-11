// Package stats samples host, live-chat process-tree, and Docker cgroup
// pressure for the picker's lazy Stats tab. Resource counters never call a
// daemon; Docker names and images are resolved once per newly observed ID.
package stats

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

type Header struct {
	CPUPercent float64
	CPUValid   bool
	PSIPercent float64
	// PSIUnavailable marks a host with no CPU pressure reading: macOS, or a
	// Linux kernel built or booted without PSI.
	PSIUnavailable bool
	RAMPercent     float64
	SwapPercent    float64
	MemoryBytes    uint64
	// RAMUnread and SwapUnread mark a counter that could not be read (its
	// warning names why): the column is unknown, never a 0% reading.
	RAMUnread  bool
	SwapUnread bool
}

type Chat struct {
	// Seat is compose.LiveSeatKey: one row per live seat, never per server
	// socket, since every Herdr pane shares its server's socket.
	Seat       string
	Name       string
	Engine     string
	CPUPercent float64
	CPUValid   bool
	// NoProcess marks a live seat none of whose process roots is in the
	// process table: its CPU and memory are unknown, never zero.
	NoProcess bool
	// ProbeFailed marks a process-less seat whose backend probe failed (a
	// Herdr seat that could not be proven): its usage is unread, not absent.
	ProbeFailed     bool
	RSSBytes        uint64
	RAMPercent      float64
	GearCount       int
	TokenCount      int64
	TokensKnown     bool
	TokensPerMinute float64
	TokenRateValid  bool
	// Spark is the usage chart: the last few per-sample token deltas for this
	// chat, oldest first. Empty until a second sample proves a trend.
	Spark []int64
}

type Container struct {
	ID          string
	Name        string
	Image       string
	CPUPercent  float64
	CPUValid    bool
	MemoryBytes uint64
	LimitBytes  uint64
	// MemoryPercent is of LimitBytes, or of host RAM for an unlimited
	// container, as docker stats reports it.
	MemoryPercent float64
}

type Window struct {
	Name string
	// UsedPct is the window's utilization, or UnknownUsedPct when there is no
	// trustworthy reading to show.
	UsedPct   float64
	ResetAt   time.Time
	ResetNote string
}

// UnknownUsedPct marks a window with no current reading — today only a window
// whose resets_at has already passed, whose cached number describes quota that
// has since rolled over. Renderers show an em dash and no bar for it instead of
// a fabricated 0%.
const UnknownUsedPct = -1

type AccountLimits struct {
	Account int
	Emoji   string
	Engine  pfmengine.ID
	Label   string
	Absent  bool
	// Unsupported marks an engine with no usage source registered — a
	// structural "this engine has no limits concept", distinct from a fetch
	// error. The TUI renders nothing for it; CLI surfaces keep the row.
	Unsupported bool
	Plan        string
	ConfirmedAt time.Time
	Status      string
	// NoWindow marks an account whose plan reports no limit window: Status
	// is an informational note, not a failure.
	NoWindow bool
	Windows  []Window
}

type Snapshot struct {
	Header Header
	Chats  []Chat
	Docker []Container
	Limits []AccountLimits
	// DockerUnsupported marks a host with no cgroup filesystem to read
	// containers from (macOS), so an empty Docker list never reads as none.
	DockerUnsupported bool
	Ready             bool
	Warnings          []string
	SampleTime        int64
}

type dockerSample struct {
	usageMicros uint64
}

type rawSample struct {
	timeNS      int64
	systemTotal uint64
	systemIdle  uint64
	processes   map[int]gather.ProcessCounter
	docker      map[string]dockerSample
}

// Sampler keeps only the preceding raw counters needed for instantaneous
// deltas. It is safe for Bubble Tea commands to call serially or concurrently.
type Sampler struct {
	ProcRoot      string
	CgroupRoot    string
	CPUCount      int
	Clock         func() int64
	DockerInspect func(string) (name, image string, err error)
	Limits        *LimitsSampler

	mu               sync.Mutex
	previous         *rawSample
	tokenMu          sync.Mutex
	tokenCache       map[string]*tokenCacheEntry
	tokenRates       map[string]*tokenRateState
	tokenHistory     map[string]*tokenHistoryState
	tokenGeneration  uint64
	dockerMu         sync.Mutex
	dockerIdentities map[string]dockerIdentity
}

func (sampler *Sampler) Sample(rows []compose.Row) (Snapshot, error) {
	return sampler.sample(rows, true)
}

// SampleResources returns the fast, local Stats view without waiting for
// provider quota calls. The UI uses it on the Stats tab.
func (sampler *Sampler) SampleResources(rows []compose.Row) (Snapshot, error) {
	return sampler.sample(rows, false)
}

// SampleLimits returns provider quota cards without requiring Linux resource
// counters. Provider failures remain visible in the returned cards and
// warnings; they do not make the snapshot unready.
func (sampler *Sampler) SampleLimits() Snapshot {
	now := clock.Real.Now().UnixNano()
	if sampler.Clock != nil {
		now = sampler.Clock()
	}
	var limits []AccountLimits
	var warnings []string
	if sampler.Limits != nil {
		limits, warnings = sampler.Limits.Sample(context.Background())
	}
	return Snapshot{Limits: limits, Ready: true, Warnings: warnings, SampleTime: now}
}

// SampleLiveLimits returns provider quota cards immediately. LimitsSampler
// keeps stale windows visible while it refreshes due accounts asynchronously;
// the caller's context bounds those refresh workers to its lifetime.
func (sampler *Sampler) SampleLiveLimits(ctx context.Context) Snapshot {
	now := clock.Real.Now().UnixNano()
	if sampler.Clock != nil {
		now = sampler.Clock()
	}
	var limits []AccountLimits
	var warnings []string
	if sampler.Limits != nil {
		limits, warnings = sampler.Limits.SampleLive(ctx)
	}
	return Snapshot{Limits: limits, Ready: true, Warnings: warnings, SampleTime: now}
}

func (sampler *Sampler) sample(rows []compose.Row, includeLimits bool) (Snapshot, error) {
	now := clock.Real.Now().UnixNano()
	if sampler.Clock != nil {
		now = sampler.Clock()
	}
	cpuCount := sampler.CPUCount
	if cpuCount <= 0 {
		cpuCount = runtime.NumCPU()
	}

	resources, err := readHostResources(sampler.ProcRoot, now, cpuCount)
	if err != nil {
		return Snapshot{}, err
	}
	total, idle := resources.total, resources.idle
	header, processes, warnings := resources.header, resources.processes, resources.warnings
	docker, dockerRaw, dockerSupported, dockerWarnings, err := readDockerResources(sampler.CgroupRoot)
	if err != nil {
		return Snapshot{}, err
	}
	for index := range docker {
		if docker[index].LimitBytes == 0 {
			docker[index].MemoryPercent = percent(docker[index].MemoryBytes, header.MemoryBytes)
		}
	}
	dockerWarnings = append(dockerWarnings, sampler.resolveDockerIdentities(docker, now)...)
	sort.Slice(docker, func(i, j int) bool {
		if docker[i].Name != docker[j].Name {
			return docker[i].Name < docker[j].Name
		}
		return docker[i].ID < docker[j].ID
	})
	warnings = append(warnings, dockerWarnings...)
	current := &rawSample{
		timeNS: now, systemTotal: total, systemIdle: idle,
		processes: processes, docker: dockerRaw,
	}

	sampler.mu.Lock()
	previous := sampler.previous
	sampler.previous = current
	sampler.mu.Unlock()

	// A zero previous total is a sample whose CPU counter was unreadable, no
	// baseline to take a delta from.
	if previous != nil && previous.systemTotal > 0 && total > previous.systemTotal {
		deltaTotal := total - previous.systemTotal
		if idle >= previous.systemIdle {
			deltaIdle := idle - previous.systemIdle
			// A disappearing Darwin process can remove its lifetime CPU from
			// the ps total. That interval has no honest host delta; leave CPU
			// unavailable instead of rendering the counter reset as 0%.
			if deltaIdle <= deltaTotal {
				header.CPUPercent = percent(deltaTotal-deltaIdle, deltaTotal)
				header.CPUValid = true
			}
		}
	}
	chats := chatTrees(rows, processes, previous, total, cpuCount, header.MemoryBytes)
	tokenWarnings := sampler.attachTokenUsage(rows, chats, now)
	warnings = append(warnings, tokenWarnings...)
	var limits []AccountLimits
	if includeLimits && sampler.Limits != nil {
		var limitWarnings []string
		limits, limitWarnings = sampler.Limits.Sample(context.Background())
		warnings = append(warnings, limitWarnings...)
	}
	if previous != nil && now > previous.timeNS {
		for index := range docker {
			prior, found := previous.docker[docker[index].ID]
			currentDocker, read := dockerRaw[docker[index].ID]
			if !found || !read || currentDocker.usageMicros < prior.usageMicros {
				continue
			}
			deltaMicros := currentDocker.usageMicros - prior.usageMicros
			elapsedMicros := uint64(now-previous.timeNS) / 1_000
			if elapsedMicros == 0 {
				continue
			}
			docker[index].CPUPercent = percent(deltaMicros, elapsedMicros)
			docker[index].CPUValid = true
		}
	}
	return Snapshot{
		Header: header, Chats: chats, Docker: docker, DockerUnsupported: !dockerSupported,
		Ready: previous != nil, Warnings: warnings, Limits: limits, SampleTime: now,
	}, nil
}

type hostResources struct {
	total     uint64
	idle      uint64
	header    Header
	processes map[int]gather.ProcessCounter
	warnings  []string
}

// readHostResources samples the host and its process table through gather.
// A host counter that cannot be read leaves that column unknown with a
// warning; it never aborts the sample of the chats and containers beside it.
// A zero total is the "no CPU counter" marker the deltas refuse.
func readHostResources(root string, now int64, cpuCount int) (hostResources, error) {
	var warnings []string
	cpu, err := gather.HostCPU(root)
	// A host with no tick counter (macOS) gets a total synthesized from the
	// process table below, so there the table's failure is a warning too.
	synthesized := errors.Is(err, errors.ErrUnsupported)
	if err != nil && !synthesized {
		cpu = gather.CPU{}
		warnings = append(warnings, err.Error())
	}
	header, headerWarnings := readHostHeader(root)
	warnings = append(warnings, headerWarnings...)
	processes, processWarnings, err := gather.ProcessCounters(root)
	if err != nil && !synthesized {
		return hostResources{}, err
	}
	if err != nil {
		// No process table is a warning and an empty table, not a failed
		// sample: the header stays, and the zero total is the "no CPU
		// counter" marker the deltas refuse.
		warnings = append(warnings, err.Error())
		return hostResources{header: header, processes: map[int]gather.ProcessCounter{}, warnings: warnings}, nil
	}
	warnings = append(warnings, processWarnings...)
	if synthesized {
		cpu = synthesizedCPU(processes, now, cpuCount)
	}
	return hostResources{
		total: cpu.Total, idle: cpu.Idle, header: header, processes: processes, warnings: warnings,
	}, nil
}

// synthesizedCPU derives host totals where the kernel keeps none. Processes
// report cumulative CPU in centiseconds; a wall-clock counter multiplied by
// the logical CPU count gives the matching host-total unit, so the shared
// delta math calculates both host and subtree CPU rates.
func synthesizedCPU(processes map[int]gather.ProcessCounter, now int64, cpuCount int) gather.CPU {
	var busy uint64
	for _, process := range processes {
		busy += process.CPUTicks
	}
	total := uint64(now / 10_000_000)
	if cpuCount > 0 {
		total *= uint64(cpuCount)
	}
	idle := uint64(0)
	if busy <= total {
		idle = total - busy
	}
	return gather.CPU{Total: total, Idle: idle}
}

// readHostHeader reads the memory and pressure columns. A counter it cannot read
// or parse leaves its columns unknown and returns a warning naming it; the
// sample goes on.
func readHostHeader(root string) (Header, []string) {
	var warnings []string
	header := Header{}
	memory, err := gather.HostMemory(root)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("read host memory counters: %v", err))
		header.RAMUnread, header.SwapUnread = true, true
	} else {
		warnings = append(warnings, memory.Warnings...)
		total, available := memory.TotalKB*1024, memory.AvailableKB*1024
		header.MemoryBytes = total
		switch {
		case memory.AvailableErr != nil:
			// No available-memory reading is an unknown RAM column, never 100% used.
			header.RAMUnread = true
			warnings = append(warnings, fmt.Sprintf("read host memory counters: %v", memory.AvailableErr))
		case total > 0 && available <= total:
			header.RAMPercent = percent(total-available, total)
		default:
			header.RAMUnread = true
			warnings = append(warnings, "read host memory counters: meminfo holds no usable MemTotal and MemAvailable")
		}
		if memory.SwapErr != nil {
			header.SwapUnread = true
			warnings = append(warnings, memory.SwapErr.Error())
		} else if memory.SwapTotalKB > 0 && memory.SwapFreeKB <= memory.SwapTotalKB {
			header.SwapPercent = percent(memory.SwapTotalKB-memory.SwapFreeKB, memory.SwapTotalKB)
		}
	}
	pressure, err := gather.HostCPUPressure(root)
	if err != nil {
		// No PSI (CONFIG_PSI off, or psi=0: absent or EOPNOTSUPP; macOS) is
		// one unavailable column, never a failed sample of the whole tab; any
		// other failure is the same column, with the failure named.
		header.PSIUnavailable = true
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.EOPNOTSUPP) &&
			!errors.Is(err, errors.ErrUnsupported) {
			warnings = append(warnings, err.Error())
		}
		return header, warnings
	}
	header.PSIPercent = pressure
	return header, warnings
}

type chatTree struct {
	chat        Chat
	roots       []int
	probeFailed bool
}

func chatTrees(
	rows []compose.Row,
	current map[int]gather.ProcessCounter,
	previous *rawSample,
	systemTotal uint64,
	cpuCount int,
	memoryTotal uint64,
) []Chat {
	bySeat := make(map[string]*chatTree)
	for index := range rows {
		row := &rows[index]
		if row.Socket == "" || !liveKind(row.Kind) {
			continue
		}
		seat := compose.LiveSeatKey(*row)
		tree := bySeat[seat]
		if tree == nil {
			tree = &chatTree{chat: Chat{Seat: seat, Name: row.Name, Engine: statsEngineName(row.Kind)}}
			bySeat[seat] = tree
		}
		tree.roots = append(tree.roots, row.PanePIDs...)
		if row.BackendError != "" {
			tree.probeFailed = true
		}
	}
	children := make(map[int][]int)
	for pid, process := range current {
		children[process.Parent] = append(children[process.Parent], pid)
	}
	chats := make([]Chat, 0, len(bySeat))
	for _, tree := range bySeat {
		// tmux is the direct parent of each pane root and belongs to the socket
		// tree too. Include it once per socket so the Stats row represents the
		// server, the engine pane, and every descendant—not merely the pane's
		// child subtree.
		roots := append([]int(nil), tree.roots...)
		for _, root := range tree.roots {
			process, found := current[root]
			if !found {
				continue
			}
			parent, found := current[process.Parent]
			if found && tmuxCommand(parent.Command) {
				roots = append(roots, process.Parent)
			}
		}
		tree.roots = roots
		visited := make(map[int]bool)
		stack := append([]int(nil), tree.roots...)
		rootSet := make(map[int]bool, len(tree.roots))
		for _, root := range tree.roots {
			rootSet[root] = true
		}
		var currentTicks, priorTicks uint64
		found, cpuUnknown := false, false
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[pid] {
				continue
			}
			visited[pid] = true
			process, present := current[pid]
			if !present {
				continue
			}
			found = true
			currentTicks += process.CPUTicks
			cpuUnknown = cpuUnknown || process.CPUUnknown
			tree.chat.RSSBytes += process.RSSBytes
			if previous != nil {
				if prior, found := previous.processes[pid]; found {
					priorTicks += prior.CPUTicks
					cpuUnknown = cpuUnknown || prior.CPUUnknown
				}
			}
			if !rootSet[pid] && !engineCommand(process.Command) && !shellCommand(process.Command) {
				tree.chat.GearCount++
			}
			stack = append(stack, children[pid]...)
		}
		if !found {
			tree.chat.NoProcess = true
			tree.chat.ProbeFailed = tree.probeFailed
			chats = append(chats, tree.chat)
			continue
		}
		if memoryTotal > 0 {
			tree.chat.RAMPercent = percent(tree.chat.RSSBytes, memoryTotal)
		}
		if previous != nil && previous.systemTotal > 0 && systemTotal > previous.systemTotal &&
			!cpuUnknown && currentTicks >= priorTicks {
			tree.chat.CPUPercent = percent(
				currentTicks-priorTicks,
				systemTotal-previous.systemTotal,
			) * float64(
				cpuCount,
			)
			tree.chat.CPUValid = true
		}
		chats = append(chats, tree.chat)
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i].Seat < chats[j].Seat })
	return chats
}

func liveKind(kind compose.Kind) bool {
	return kind == compose.LiveClaude || kind == compose.LiveCodex ||
		kind == compose.LiveOpenCode || kind == compose.LiveSplit ||
		kind == compose.Agent || kind == compose.Booting
}

// statsEngineName names the engine a live row's resource usage is charged to.
// Its fallback is CLAUDE, not "unknown", so a kind with no arm here does not
// read as unidentified — it reads as somebody else's chat, which is why every
// non-Claude live kind must be named explicitly.
func statsEngineName(kind compose.Kind) string {
	switch kind {
	case compose.LiveCodex:
		return pfmengine.MustLookup(pfmengine.Codex).LongName
	case compose.LiveOpenCode:
		return pfmengine.MustLookup(pfmengine.OpenCode).LongName
	case compose.LiveSplit:
		return "mixed"
	default:
		return pfmengine.MustLookup(pfmengine.Claude).LongName
	}
}

func engineCommand(command string) bool {
	name := strings.ToLower(filepath.Base(command))
	for _, id := range pfmengine.All() {
		if strings.Contains(name, pfmengine.MustLookup(id).Binary) {
			return true
		}
	}
	return false
}

func shellCommand(command string) bool {
	switch strings.ToLower(filepath.Base(command)) {
	case "bash", "zsh", "sh", "fish", "tmux":
		return true
	default:
		return false
	}
}

func tmuxCommand(command string) bool {
	return strings.HasPrefix(strings.ToLower(filepath.Base(command)), "tmux")
}

func readDocker(root string) ([]Container, map[string]dockerSample, []string, error) {
	containers := make([]Container, 0)
	raw := make(map[string]dockerSample)
	var warnings []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("walk Docker cgroup %s: %v", path, walkErr))
			return nil
		}
		if !entry.IsDir() || !dockerCgroup(path) {
			return nil
		}
		id := dockerID(filepath.Base(path))
		name := shortDockerID(id)
		// An unreadable CPU counter leaves this container's CPU unknown (no raw
		// sample, so CPUValid never sets); its memory still shows.
		usage, usageErr := readKeyUint(filepath.Join(path, "cpu.stat"), "usage_usec")
		if usageErr != nil {
			warnings = append(warnings, fmt.Sprintf("read Docker CPU %s: %v", name, usageErr))
		}
		memory, err := readUintFile(filepath.Join(path, "memory.current"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("read Docker memory %s: %v", name, err))
			return filepath.SkipDir
		}
		limitContent, err := os.ReadFile(filepath.Join(path, "memory.max"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("read Docker limit %s: %v", name, err))
			return filepath.SkipDir
		}
		limit := uint64(0)
		if strings.TrimSpace(string(limitContent)) != "max" {
			limit, err = strconv.ParseUint(strings.TrimSpace(string(limitContent)), 10, 64)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("parse Docker limit %s: %v", name, err))
				return filepath.SkipDir
			}
		}
		container := Container{ID: id, Name: name, Image: "?", MemoryBytes: memory, LimitBytes: limit}
		if limit > 0 {
			container.MemoryPercent = percent(memory, limit)
		}
		containers = append(containers, container)
		if usageErr == nil {
			raw[id] = dockerSample{usageMicros: usage}
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, nil, warnings, fmt.Errorf("walk Docker cgroups: %w", err)
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	return containers, raw, warnings, nil
}

func dockerCgroup(path string) bool {
	base := filepath.Base(path)
	parent := filepath.Base(filepath.Dir(path))
	return (strings.HasPrefix(base, "docker-") && strings.HasSuffix(base, ".scope")) ||
		(parent == "docker" && len(base) >= 12)
}

func dockerID(base string) string {
	return strings.TrimSuffix(strings.TrimPrefix(base, "docker-"), ".scope")
}

func shortDockerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func readKeyUint(path, key string) (uint64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == key {
			return strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("key %s is absent", key)
}

func readUintFile(path string) (uint64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
}

func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
